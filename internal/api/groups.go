package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"painel-ramais/internal/store"
)

// maxGroupsCSVLines limita o CSV enviado pela tela (a CLI nao tem este teto): protege a memoria.
const maxGroupsCSVLines = 20000

// groupsImport: POST /api/groups/import (so administrador, C6). Mesma validacao e mesma
// transacao da CLI; depois de gravar, o daemon relê os grupos como no SIGHUP.
func (s *Server) groupsImport(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var in struct {
		CSV     *string `json:"csv"`
		Replace bool    `json:"replace"`
	}
	if !decodeBodyMax(w, r, &in, maxGroupsBodyBytes) {
		return
	}
	if in.CSV == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Campo csv obrigatorio")
		return
	}
	n := strings.Count(*in.CSV, "\n")
	if !strings.HasSuffix(*in.CSV, "\n") {
		n++ // ultima linha sem quebra; a quebra final nao abre uma linha vazia
	}
	if n > maxGroupsCSVLines {
		writeError(w, http.StatusBadRequest, "invalid_csv", fmt.Sprintf("CSV com %d linhas; o maximo pela tela e %d", n, maxGroupsCSVLines))
		return
	}
	rows, err := store.ParseGroupsCSV(strings.NewReader(*in.CSV))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_csv", err.Error())
		return
	}
	if err := s.Store.ImportGroups(r.Context(), rows, in.Replace, s.Now()); err != nil {
		if errors.Is(err, store.ErrOverlap) {
			writeError(w, http.StatusBadRequest, "invalid_csv", err.Error())
			return
		}
		s.internalError(w, r, err)
		return
	}
	s.log.Info("grupos importados", requestIDKey, requestID(r), "sub", sess.Subject, "rows", len(rows), "replace", in.Replace)
	if !s.reloadGroups(w, r) {
		return
	}
	writeOK(w, map[string]any{"rows": len(rows), "replace": in.Replace}, nil)
}

// groupsDelete: POST /api/groups/delete (so administrador, C6).
func (s *Server) groupsDelete(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeBodyMax(w, r, &in, maxGroupsBodyBytes) {
		return
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 64 {
		writeError(w, http.StatusBadRequest, "bad_request", "Nome do grupo deve ter de 1 a 64 caracteres")
		return
	}
	switch err := s.Store.DeleteGroup(r.Context(), in.Name); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Grupo inexistente")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.log.Info("grupo removido", requestIDKey, requestID(r), "sub", sess.Subject, "name", in.Name)
	if !s.reloadGroups(w, r) {
		return
	}
	writeOK(w, map[string]any{"name": in.Name}, nil)
}

// reloadGroups devolve false (e ja respondeu) se a recarga falhou: os grupos estao no
// banco, mas as telas abertas nao foram avisadas; repetir a operacao e seguro.
func (s *Server) reloadGroups(w http.ResponseWriter, r *http.Request) bool {
	if s.ReloadGroups == nil {
		return true
	}
	if err := s.ReloadGroups(r.Context()); err != nil {
		s.internalError(w, r, err)
		return false
	}
	return true
}
