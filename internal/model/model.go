// Package model define os tipos compartilhados do coletor: o formato JSON de
// ramais e chamadas (CONTRATOS.md secao 2), os eventos normalizados (secao 11)
// e o contrato do coletor AMI consumido por cmd/painel-ramais.
package model

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Registration e o estado de registro de um ramal.
type Registration string

const (
	RegRegistered   Registration = "registered"
	RegUnregistered Registration = "unregistered"
	RegUnreachable  Registration = "unreachable"
	RegUnknown      Registration = "unknown"
)

// DeviceState e o estado do dispositivo (Asterisk, em minusculas).
type DeviceState string

const (
	DevUnknown     DeviceState = "unknown"
	DevNotInUse    DeviceState = "not_inuse"
	DevInUse       DeviceState = "inuse"
	DevBusy        DeviceState = "busy"
	DevInvalid     DeviceState = "invalid"
	DevUnavailable DeviceState = "unavailable"
	DevRinging     DeviceState = "ringing"
	DevRingInUse   DeviceState = "ringinuse"
	DevOnHold      DeviceState = "onhold"
)

// Technology e a tecnologia ativa da instalacao.
type Technology string

const (
	TechSIP   Technology = "sip"
	TechPJSIP Technology = "pjsip"
)

// Direction e a direcao de uma chamada, vista do ramal.
type Direction string

const (
	DirInternal Direction = "internal"
	DirInbound  Direction = "inbound"
	DirOutbound Direction = "outbound"
)

// CallState e o estado de uma chamada.
type CallState string

const (
	CallRinging CallState = "ringing"
	CallTalking CallState = "talking"
	CallHold    CallState = "hold"
)

// PeerKind classifica o outro lado da chamada.
type PeerKind string

const (
	PeerExtension PeerKind = "extension"
	PeerExternal  PeerKind = "external"
	PeerUnknown   PeerKind = "unknown"
)

// InternalEndpoints e a UNICA lista de endpoints internos do Issabel que nunca sao ramal nem
// tronco: ficam fora da contagem de deteccao de tecnologia, do estado e da tela. Comparacao
// sem diferenciar maiusculas, no nome inteiro. O Issabel cria "dummy_endpoint" so para o
// res_pjsip nao ficar sem endpoint. Outros nomes entram aqui com evidencia da captura.
var InternalEndpoints = []string{"dummy_endpoint"}

// IsInternalEndpoint diz se name esta em InternalEndpoints.
func IsInternalEndpoint(name string) bool {
	for _, n := range InternalEndpoints {
		if strings.EqualFold(name, n) {
			return true
		}
	}
	return false
}

// IsExtension e a regra unica de "isto e um ramal": nome nao vazio, fora de InternalEndpoints
// e casando o extension_pattern (ja ancorado). Estado, deteccao de tecnologia e preenchimento
// de nomes usam esta funcao.
func IsExtension(pattern *regexp.Regexp, name string) bool {
	return name != "" && !IsInternalEndpoint(name) && pattern.MatchString(name)
}

// NoGroup e o nome do grupo de ramais fora de qualquer faixa.
const NoGroup = "Sem grupo"

// Stamp e um instante serializado como RFC 3339 UTC com milissegundos.
type Stamp struct{ time.Time }

const stampLayout = "2006-01-02T15:04:05.000Z"

// NewStamp normaliza t (UTC, milissegundos), o que torna Stamp comparavel com ==.
func NewStamp(t time.Time) Stamp { return Stamp{t.UTC().Truncate(time.Millisecond)} }

func (s Stamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.UTC().Format(stampLayout))
}

func (s *Stamp) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	t, err := time.Parse(time.RFC3339Nano, str)
	if err != nil {
		return err
	}
	*s = NewStamp(t)
	return nil
}

// Peer e o outro lado de uma chamada.
type Peer struct {
	Kind   PeerKind `json:"kind"`
	Number *string  `json:"number"`
	Name   *string  `json:"name"`
}

// Call e uma chamada ativa de um ramal (CONTRATOS.md secao 2.2).
type Call struct {
	ID         string    `json:"id"`
	Direction  Direction `json:"direction"`
	State      CallState `json:"state"`
	Peer       Peer      `json:"peer"`
	StartedAt  Stamp     `json:"started_at"`
	AnsweredAt *Stamp    `json:"answered_at"`
}

// Extension e um ramal (CONTRATOS.md secao 2.1). Calls nunca e nil.
type Extension struct {
	Number       string       `json:"number"`
	Name         *string      `json:"name"`
	Technology   Technology   `json:"technology"`
	Group        string       `json:"group"`
	GroupManual  bool         `json:"group_manual"` // true: o grupo vem de atribuicao manual (vence a faixa)
	Registration Registration `json:"registration"`
	IP           *string      `json:"ip"`
	UserAgent    *string      `json:"user_agent"`
	LatencyMs    *int         `json:"latency_ms"`
	DeviceState  DeviceState  `json:"device_state"`
	Calls        []Call       `json:"calls"`
}

// StrPtr devolve nil para texto vazio (JSON null).
func StrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// GroupRange e uma faixa numerica de um grupo.
type GroupRange struct {
	Start, End int64
	Name       string
}

// Range e uma faixa numerica sem nome; Group e um grupo do catalogo (pode ter zero faixas).
type Range struct{ Start, End int64 }

type Group struct {
	Name   string
	Ranges []Range
}

// Counts agrega ramais por estado de registro; InCall conta ramais com pelo
// menos uma chamada.
type Counts struct {
	Registered   int
	Unregistered int
	Unreachable  int
	Unknown      int
	InCall       int
}

// Total soma os ramais pelos quatro estados de registro.
func (c Counts) Total() int { return c.Registered + c.Unregistered + c.Unreachable + c.Unknown }

// MaxNameRunes e o tamanho maximo de um nome de ramal (em caracteres).
const MaxNameRunes = 64

// CleanName normaliza um nome vindo de fora (display name do telefone e texto livre):
// tira caracteres de controle e de formatacao (categorias C*: C0/C1, bidi U+202A..202E e
// U+2066..2069, largura zero), troca qualquer espaco por espaco simples, apara as pontas
// e limita a MaxNameRunes caracteres (sem cortar no meio de um caractere).
func CleanName(s string) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = b.Len() > 0
		case unicode.In(r, unicode.C):
			// descartado
		default:
			if n >= MaxNameRunes {
				return b.String()
			}
			if space {
				if n+1 >= MaxNameRunes {
					return b.String()
				}
				b.WriteByte(' ')
				n++
				space = false
			}
			b.WriteRune(r)
			n++
		}
	}
	return b.String()
}
