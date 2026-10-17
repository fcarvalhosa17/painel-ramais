package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Politicas da tela (plano da fase 2, C3). Nada inline e nada externo: a tela so carrega
// o que o proprio daemon serve.
const (
	cspTela = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; " +
		"font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'"
	cspAviso = "default-src 'none'; style-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

	// O Apache entrega o daemon em /painel-ramais/ (o mesmo Path do cookie); o daemon so ve a raiz.
	avisoURL      = cookiePath + "acesso#" // o # vazio impede herdar o fragmento #t=<token>
	emergenciaURL = cookiePath + "emergencia/"
)

var cspEmergencia = strings.Replace(cspTela, "frame-ancestors 'self'", "frame-ancestors 'none'", 1)

// assetTypes define o Content-Type pela extensao, sem depender do /etc/mime.types.
var assetTypes = map[string]string{
	".js":  "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8",
	".svg": "image/svg+xml",
}

// fontTypes vale so dentro de assets/fonts/: as fontes (woff2) e os textos de licenca (OFL) que vao com elas.
var fontTypes = map[string]string{
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
}

// fontTexts: os unicos .txt servidos em assets/fonts/, por nome exato (licencas OFL e origem das fontes).
var fontTexts = map[string]bool{"OFL-IBMPlex.txt": true, "OFL-BarlowSemiCondensed.txt": true, "ORIGEM.txt": true}

// fontCacheControl: a fonte so muda com versao nova do binario; uma semana de cache e a revalidacao por ETag cobrem a troca.
const fontCacheControl = "public, max-age=604800"

// page entrega GET e HEAD de uma rota da tela. exact != "" exige o caminho inteiro
// (o ServeMux casa "/" e "/emergencia/" como prefixo); fora disso, 404 no envelope.
func (s *Server) page(exact string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if (exact != "" && r.URL.Path != exact) || s.Static == nil {
			writeError(w, http.StatusNotFound, "not_found", "Rota inexistente")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Metodo nao permitido")
			return
		}
		h(w, r)
	}
}

// tela decide pelo Sec-Fetch-Dest: dentro do iframe (ou navegador antigo, sem o
// cabecalho; o script confere window.top) entrega a tela; qualquer outro destino vai ao aviso.
func (s *Server) tela(w http.ResponseWriter, r *http.Request) {
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "", "iframe":
		w.Header().Set("Vary", "Sec-Fetch-Dest")
		s.servePage(w, r, "index.html", cspTela, "SAMEORIGIN")
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Location", avisoURL)
		w.WriteHeader(http.StatusSeeOther)
	}
}

// acesso e o aviso "Acesse pelo menu PBX do Issabel" (sem script).
func (s *Server) acesso(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, r, "acesso.html", cspAviso, "SAMEORIGIN")
}

func (s *Server) emergenciaRedirect(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Location", emergenciaURL)
	w.WriteHeader(http.StatusMovedPermanently)
}

// emergencia entrega a mesma tela, abrivel na aba principal e nunca em iframe.
func (s *Server) emergencia(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, r, "index.html", cspEmergencia, "DENY")
}

func (s *Server) servePage(w http.ResponseWriter, r *http.Request, file, csp, frameOptions string) {
	body, err := fs.ReadFile(s.Static, file)
	if err != nil {
		s.staticError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", frameOptions)
	h.Set("Content-Security-Policy", csp)
	w.Write(body)
}

// asset serve assets/<arquivo> (so .js, .css e .svg) e assets/fonts/<arquivo> (so .woff2 e .txt),
// um nivel por pasta. Revalida por ETag.
// ponytail: le e calcula o hash a cada pedido (centenas de KiB, poucos clientes); cache em memoria se pesar.
func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	base, types := name, assetTypes
	if rest, isFont := strings.CutPrefix(name, "fonts/"); isFont {
		base, types = rest, fontTypes
	}
	ctype, ok := types[path.Ext(base)]
	if ok && base != name && path.Ext(base) == ".txt" && !fontTexts[base] {
		ok = false
	}
	if base == "" || base[0] == '.' || strings.ContainsAny(base, "/\\") || !ok || !fs.ValidPath("assets/"+name) {
		writeError(w, http.StatusNotFound, "not_found", "Rota inexistente")
		return
	}
	body, err := fs.ReadFile(s.Static, "assets/"+name)
	if err != nil {
		s.staticError(w, r, err)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "no-cache")
	if base != name {
		h.Set("Cache-Control", fontCacheControl)
	}
	h.Set("ETag", etag)
	if path.Ext(name) == ".svg" { // um .svg aberto como documento nao executa nem carrega nada
		h.Set("Content-Security-Policy", "default-src 'none'")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(body)
}

func (s *Server) staticError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "not_found", "Rota inexistente")
		return
	}
	s.internalError(w, r, err)
}
