// Package capture grava e le capturas de AMI real (formato em CONTRATOS.md secao 18):
// quadros brutos com data e direcao, com segredos sempre mascarados e, opcionalmente,
// numeros, nomes, usuarios, hosts e IPs trocados por valores ficticios (LGPD). Nao importa
// pacote interno.
package capture

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// SanitizeOptions controla a limpeza dos quadros.
type SanitizeOptions struct {
	Anonymize bool
	// ExternalDigits: sequencia de digitos com pelo menos este tamanho e numero externo
	// (padrao 7); ramais internos costumam ter ate 6.
	ExternalDigits int
}

// Sanitizer mascara segredos e, se pedido, anonimiza de forma consistente: o mesmo
// valor original vira sempre o mesmo ficticio durante a captura.
//
// A anonimizacao e por EXCLUSAO: todo valor passa pela limpeza de numeros longos, IPv4,
// IPv6 e hosts de URI, exceto as chaves estruturais da lista segura (Event, Uniqueid,
// Timestamp...). Nomes, usuarios, contas e hosts tem tratamento proprio por chave.
type Sanitizer struct {
	opt     SanitizeOptions
	numbers map[string]string
	names   map[string]string
	idents  map[string]string
	hosts   map[string]string
	ips     map[string]string
}

// NewSanitizer cria o limpador.
func NewSanitizer(opt SanitizeOptions) *Sanitizer {
	if opt.ExternalDigits < 1 {
		opt.ExternalDigits = 7
	}
	return &Sanitizer{opt: opt, numbers: map[string]string{}, names: map[string]string{},
		idents: map[string]string{}, hosts: map[string]string{}, ips: map[string]string{}}
}

const maskedValue = "***"

var (
	secretKey = regexp.MustCompile(`(?i)secret|password|passwd|md5|challenge|token|key$`)
	// safeKey: campos estruturais do protocolo, que nao carregam dado pessoal e cujos
	// numeros longos (Uniqueid, Timestamp, RoundtripUsec...) nao podem ser trocados.
	safeKey = regexp.MustCompile(`(?i)^(event|response|action|actionid|uniqueid|linkedid|destuniqueid|destlinkedid|bridge.*|` +
		`timestamp|privilege|eventlist|listitems|duration|.*usec|.*time|.*date|uptime|.*count|sequence.*|` +
		`channelstate|channelstatedesc|state|status|cause|causetxt|priority|amiversion|asteriskversion|` +
		`devicestate|dialstatus|context|language|useragent)$`)
	// nameKey: campos que carregam nome de pessoa.
	nameKey = regexp.MustCompile(`(?i)idname$|linename$|^(fullname|displayname|cnam)$`)
	// identKey: usuario, conta e caixa postal (texto livre que identifica alguem ou um cliente).
	identKey = regexp.MustCompile(`(?i)^(username|authusername|authuser|user|accountcode|mailbox)$`)
	// hostKey: nomes de maquina/dominio.
	hostKey = regexp.MustCompile(`(?i)^(host|hostname|domain|fromdomain|realm|server|outboundproxy)$`)
	// nameVar: variaveis de canal cujo Value e um nome.
	nameVar = regexp.MustCompile(`(?i)name|cnam|callerid|display`)

	ipv4 = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	// IPv6 completo (8 grupos) ou comprimido (com "::"). Hora (14:30:00) tem so 3 grupos e
	// nunca casa.
	ipv6 = regexp.MustCompile(`(?i)(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}|` +
		`(?:[0-9a-f]{1,4}:){1,7}:(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){0,6})?|` +
		`::(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){0,6})`)
	uriHost       = regexp.MustCompile(`@([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+)`)
	callerIDValue = regexp.MustCompile(`^\s*"?(.*?)"?\s*<([^>]*)>\s*$`)
	digitRun      = regexp.MustCompile(`\d+`)
)

type frameCtx struct {
	login    bool   // quadro de Action: Login (o Username e o da conta do painel)
	variable string // valor de "Variable", que diz o que o "Value" guarda
}

// Frame limpa um quadro bruto (linhas "Chave: valor" terminadas em CRLF). Linhas sem ":"
// (banner) passam intactas, salvo IPs na anonimizacao.
func (s *Sanitizer) Frame(raw []byte) []byte {
	lines := bytes.Split(raw, []byte("\r\n"))
	var ctx frameCtx
	for _, ln := range lines {
		k, v, ok := strings.Cut(string(ln), ":")
		switch {
		case !ok:
		case strings.EqualFold(strings.TrimSpace(k), "action") && strings.EqualFold(strings.TrimSpace(v), "login"):
			ctx.login = true
		case strings.EqualFold(strings.TrimSpace(k), "variable"):
			ctx.variable = strings.TrimSpace(v)
		}
	}
	for i, ln := range lines {
		k, v, ok := strings.Cut(string(ln), ":")
		if !ok {
			if s.opt.Anonymize {
				lines[i] = []byte(s.replaceIPs(string(ln)))
			}
			continue
		}
		lines[i] = []byte(k + ":" + s.value(strings.TrimSpace(k), v, ctx))
	}
	return bytes.Join(lines, []byte("\r\n"))
}

// value devolve o valor (com o espaco inicial original) ja limpo.
func (s *Sanitizer) value(key, v string, ctx frameCtx) string {
	if secretKey.MatchString(key) {
		if strings.TrimSpace(v) == "" {
			return v
		}
		return " " + maskedValue
	}
	if !s.opt.Anonymize {
		return v
	}
	body := strings.TrimSpace(v)
	if body == "" || safeKey.MatchString(key) {
		return v
	}
	switch {
	case strings.EqualFold(key, "callerid") || strings.EqualFold(key, "connectedline"):
		body = s.callerID(body)
	case nameKey.MatchString(key), strings.EqualFold(key, "value") && nameVar.MatchString(ctx.variable):
		body = s.name(body)
	case identKey.MatchString(key) && !ctx.login:
		body = s.ident(body)
	case hostKey.MatchString(key):
		body = s.host(body)
	default:
		body = s.numbersIn(body)
		body = s.hostsInURIs(body)
	}
	return " " + s.replaceIPs(body)
}

// callerID trata `"Maria Souza" <2001>`: troca o nome e os numeros longos.
func (s *Sanitizer) callerID(v string) string {
	m := callerIDValue.FindStringSubmatch(v)
	if m == nil {
		return s.numbersIn(v)
	}
	name := s.name(m[1])
	if name != "" {
		name = `"` + name + `" `
	}
	return name + "<" + s.numbersIn(m[2]) + ">"
}

func (s *Sanitizer) name(n string) string {
	n = strings.Trim(strings.TrimSpace(n), `"`)
	if n == "" || n == "<unknown>" || n == "unknown" {
		return n
	}
	return s.pseudo(s.names, n, "Pessoa %03d")
}

// ident troca usuario/conta por pseudonimo; ramal curto (so digitos) e numero externo
// longo seguem as regras de numero.
func (s *Sanitizer) ident(v string) string {
	if isDigits(v) {
		return s.numbersIn(v)
	}
	return s.pseudo(s.idents, v, "Conta %03d")
}

// host troca nome de maquina por host-N.invalid (IPs ficam para replaceIPs).
func (s *Sanitizer) host(v string) string {
	if ipv4.MatchString(v) || ipv6.MatchString(v) || v == "localhost" {
		return v
	}
	return s.pseudo(s.hosts, strings.ToLower(v), "host-%03d.invalid")
}

func (s *Sanitizer) pseudo(m map[string]string, orig, format string) string {
	if f, ok := m[orig]; ok {
		return f
	}
	f := fmt.Sprintf(format, len(m)+1)
	m[orig] = f
	return f
}

func isDigits(v string) bool {
	if v == "" {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// numbersIn troca cada sequencia de digitos longa por um numero ficticio de mesmo
// tamanho. Sufixos de canal ("PJSIP/2001-00000012") ficam como estao.
func (s *Sanitizer) numbersIn(v string) string {
	idx := digitRun.FindAllStringIndex(v, -1)
	if len(idx) == 0 {
		return v
	}
	var b strings.Builder
	last := 0
	for _, loc := range idx {
		run := v[loc[0]:loc[1]]
		b.WriteString(v[last:loc[0]])
		if len(run) >= s.opt.ExternalDigits && !(loc[0] > 0 && v[loc[0]-1] == '-') {
			b.WriteString(s.fakeNumber(run))
		} else {
			b.WriteString(run)
		}
		last = loc[1]
	}
	b.WriteString(v[last:])
	return b.String()
}

// fakeNumber mantem o tamanho e o primeiro digito (0 de saida, 0800 etc.) e troca o resto
// por um contador: unico por original, sempre o mesmo para o mesmo numero.
func (s *Sanitizer) fakeNumber(orig string) string {
	if f, ok := s.numbers[orig]; ok {
		return f
	}
	n := len(s.numbers) + 1
	f := orig[:1] + fmt.Sprintf("%0*d", len(orig)-1, n%pow10(len(orig)-1))
	for i := 1; s.used(f); i++ { // estouro do contador: nao reutiliza ficticio
		f = orig[:1] + fmt.Sprintf("%0*d", len(orig)-1, (n+i)%pow10(len(orig)-1))
	}
	s.numbers[orig] = f
	return f
}

func (s *Sanitizer) used(f string) bool {
	for _, v := range s.numbers {
		if v == f {
			return true
		}
	}
	return false
}

func pow10(n int) int {
	p := 1
	for i := 0; i < n && i < 17; i++ {
		p *= 10
	}
	return p
}

// hostsInURIs troca o host de "usuario@host.dominio.com" (SIP URI, Contact, Via).
func (s *Sanitizer) hostsInURIs(v string) string {
	return uriHost.ReplaceAllStringFunc(v, func(m string) string {
		h := m[1:]
		if ipv4.MatchString(h) { // IP e tratado por replaceIPs
			return m
		}
		return "@" + s.host(h)
	})
}

// replaceIPs troca IPv4 e IPv6 (menos loopback e enderecos nulos), de forma consistente.
func (s *Sanitizer) replaceIPs(v string) string {
	v = ipv4.ReplaceAllStringFunc(v, func(ip string) string {
		if strings.HasPrefix(ip, "127.") || ip == "0.0.0.0" {
			return ip
		}
		return s.fakeIP(ip, func(n int) string { return fmt.Sprintf("10.200.%d.%d", n/250%256, n%250+1) })
	})
	return ipv6.ReplaceAllStringFunc(v, func(ip string) string {
		if ip == "::" || ip == "::1" {
			return ip
		}
		return s.fakeIP(strings.ToLower(ip), func(n int) string { return fmt.Sprintf("fd00:200::%x", n+1) })
	})
}

func (s *Sanitizer) fakeIP(orig string, mk func(n int) string) string {
	if f, ok := s.ips[orig]; ok {
		return f
	}
	f := mk(len(s.ips))
	s.ips[orig] = f
	return f
}
