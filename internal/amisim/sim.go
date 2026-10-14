// Package amisim e o simulador de AMI do Asterisk (13/16/18) usado como oraculo
// do teste de carga. Escrito a partir do protocolo AMI e de docs/CONTRATOS.md,
// sem importar nada do cliente (internal/ami) nem do estado (internal/state):
// o framing e a verdade do estado sao proprios, de proposito.
package amisim

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	ModeSIP   = "sip"
	ModePJSIP = "pjsip"
)

// Config do simulador. Zero value de cada campo vira o padrao em New.
type Config struct {
	Mode           string   // sip | pjsip
	Exts           int      // quantidade de ramais (padrao 10000)
	FirstExt       int      // primeiro numero (padrao 2000)
	Trunks         int      // quantidade de troncos (padrao 4)
	User, Secret   string   // credenciais do Login
	Read, Write    string   // classes do usuario AMI (padrao: contrato 7.1, read=system,call write=system,reporting)
	Asterisk       string   // versao reportada (padrao 18.20.0)
	Modules        []string // modulos carregados (padrao: so o da tecnologia)
	NoShowContacts bool     // simula versao sem PJSIPShowContacts
	Unfiltered     bool     // sem eventfilter: entrega tambem eventos fora da lista (ruido)
	Seed           int64
}

func (c *Config) defaults() {
	if c.Mode == "" {
		c.Mode = ModePJSIP
	}
	if c.Exts == 0 {
		c.Exts = 10000
	}
	if c.FirstExt == 0 {
		c.FirstExt = 2000
	}
	if c.Trunks == 0 {
		c.Trunks = 4
	}
	if c.User == "" {
		c.User = "painel-ramais"
	}
	if c.Secret == "" {
		c.Secret = "segredo-de-teste"
	}
	if c.Read == "" {
		c.Read = "system,call"
	}
	if c.Write == "" {
		c.Write = "system,reporting"
	}
	if c.Asterisk == "" {
		c.Asterisk = "18.20.0"
	}
	if len(c.Modules) == 0 {
		if c.Mode == ModeSIP {
			c.Modules = []string{"chan_sip.so"}
		} else {
			c.Modules = []string{"chan_pjsip.so"}
		}
	}
	if c.Seed == 0 {
		c.Seed = 1
	}
}

// Stamp recebe a hora (unix ns) em que o lote com o evento foi gravado no socket.
type Stamp struct{ At int64 }

type ext struct {
	num, name, ip, ua string
	idx               int
	reg               string // registered | unregistered | unreachable
	lat               int
	contact           string
	calls             []*tcall
	dev               string // ultimo device_state emitido
	busy              bool   // reservado por um script
	freeIdx           int    // posicao em Sim.free, -1 se fora
}

// tcall e a verdade de uma chamada de um ramal (modelo do simulador, nao do contrato).
type tcall struct {
	id, dir, state    string
	pk, pn, pname     string
	soft              bool // peer ambiguo (ex.: tocando via Local antes de atender)
	dest              bool // o canal do ramal e destino de Dial
	started, answered time.Time
}

type chanT struct {
	name, uid, linked        string
	created                  time.Time
	dest                     bool   // destino de um Dial (CoreShowChannels: AppDial, "(Outgoing Line)")
	dialData                 string // argumentos do Dial que este canal executa
	state                    int
	num, cname, cnum, cname2 string
	exten, ctx               string
	bridge                   string
	hold                     bool
	e                        *ext
	tc                       *tcall
	trunkDir, rnum, rname    string // so em canal de tronco
}

// Sim e o simulador. Todo o modelo e protegido por mu; eventos sao enfileirados
// por conexao sob mu, entao a ordem de gravacao e a ordem das mutacoes.
type Sim struct {
	cfg Config
	mu  sync.Mutex

	exts   []*ext
	byNum  map[string]*ext
	trunks []string
	free   []*ext
	chans  map[string]*chanT
	bridge map[string]int // bridge id -> numero de canais
	conns  map[*conn]struct{}
	seq    uint64
	t0     int64
	rng    *rand.Rand
	tech   string // "PJSIP" | "SIP"

	// OnCommit e chamado sob mu, antes de os eventos irem para o socket, a cada
	// mutacao de ramal que gerou eventos entregues a um cliente AMI. Stamp recebe
	// a hora (unix ns) em que o lote com o evento foi gravado no socket.
	OnCommit func(v ExtView, st *Stamp)

	ln       net.Listener
	addr     string
	lnMu     sync.Mutex
	down     bool
	actMu    sync.Mutex
	actCount map[string]int
	actViol  []ActionRec
}

// New cria o simulador com a populacao inicial: ~91% registrados, ~7% nao
// registrados, ~2% inalcancaveis.
func New(cfg Config) (*Sim, error) {
	cfg.defaults()
	if cfg.Mode != ModeSIP && cfg.Mode != ModePJSIP {
		return nil, fmt.Errorf("modo invalido %q (use sip ou pjsip)", cfg.Mode)
	}
	s := &Sim{
		cfg:      cfg,
		byNum:    make(map[string]*ext, cfg.Exts),
		chans:    map[string]*chanT{},
		bridge:   map[string]int{},
		conns:    map[*conn]struct{}{},
		rng:      rand.New(rand.NewSource(cfg.Seed)),
		t0:       time.Now().Unix(),
		actCount: map[string]int{},
		tech:     "PJSIP",
	}
	if cfg.Mode == ModeSIP {
		s.tech = "SIP"
	}
	uas := []string{"Yealink SIP-T46U 108.86.0.20", "Grandstream GXP1625 1.0.11.23", "Linphone/5.0.0", "Zoiper rv2.10.19.4", "Fanvil X3S 2.12.1", "Cisco/SPA504G-7.6.2"}
	for i := 0; i < cfg.Exts; i++ {
		n := strconv.Itoa(cfg.FirstExt + i)
		e := &ext{
			num: n, name: "Ramal " + n, idx: i, freeIdx: -1,
			ip:  fmt.Sprintf("10.%d.%d.%d", 1+i/62500, 1+(i/250)%250, 1+i%250),
			ua:  uas[i%len(uas)],
			lat: 2 + s.rng.Intn(40),
		}
		switch r := s.rng.Intn(100); {
		case r < 91:
			e.reg = "registered"
		case r < 98:
			e.reg = "unregistered"
		default:
			e.reg = "unreachable"
		}
		e.contact = s.contactID(e)
		e.dev = s.devState(e)
		s.exts = append(s.exts, e)
		s.byNum[n] = e
		s.addFree(e)
	}
	for i := 1; i <= cfg.Trunks; i++ {
		s.trunks = append(s.trunks, "trunk"+strconv.Itoa(i))
	}
	return s, nil
}

func (s *Sim) contactID(e *ext) string {
	return fmt.Sprintf("%s;@%012x", e.num, uint64(e.idx)*2654435761+uint64(s.cfg.Seed))
}

func (s *Sim) loaded(mod string) bool {
	for _, m := range s.cfg.Modules {
		if m == mod {
			return true
		}
	}
	return false
}

// ---- verdade (visao no formato do contrato, calculada pelo modelo do simulador)

type PeerView struct {
	Kind   string `json:"kind"`
	Number string `json:"number"`
	Name   string `json:"name"`
}

type CallView struct {
	ID         string   `json:"id"`
	Direction  string   `json:"direction"`
	State      string   `json:"state"`
	Peer       PeerView `json:"peer"`
	StartedAt  string   `json:"started_at"`
	AnsweredAt string   `json:"answered_at"`
	Ambiguous  bool     `json:"peer_ambiguous,omitempty"`
}

type ExtView struct {
	Number       string     `json:"number"`
	Name         string     `json:"name"`
	Technology   string     `json:"technology"`
	Group        string     `json:"group,omitempty"`
	Registration string     `json:"registration"`
	IP           string     `json:"ip"`
	UserAgent    string     `json:"user_agent"`
	LatencyMs    *int       `json:"latency_ms"`
	DeviceState  string     `json:"device_state"`
	Calls        []CallView `json:"calls"`
}

const tsFmt = "2006-01-02T15:04:05.000Z"

func (s *Sim) view(e *ext) ExtView {
	v := ExtView{Number: e.num, Name: e.name, Technology: s.cfg.Mode, Registration: e.reg, DeviceState: e.dev, Calls: []CallView{}}
	if e.reg != "unregistered" {
		v.IP, v.UserAgent = e.ip, e.ua
		l := e.lat
		if e.reg == "registered" {
			v.LatencyMs = &l
		}
	}
	for _, c := range e.calls {
		cv := CallView{ID: c.id, Direction: c.dir, State: c.state, StartedAt: c.started.UTC().Format(tsFmt),
			Peer: PeerView{Kind: c.pk, Number: c.pn, Name: c.pname}, Ambiguous: c.soft}
		if !c.answered.IsZero() {
			cv.AnsweredAt = c.answered.UTC().Format(tsFmt)
		}
		v.Calls = append(v.Calls, cv)
	}
	sort.Slice(v.Calls, func(i, j int) bool { return v.Calls[i].ID < v.Calls[j].ID })
	return v
}

// Fingerprint resume o que o teste compara ramal a ramal: registro, estado do
// dispositivo e, por chamada, id/direcao/estado/outro lado (sem horarios).
// Chamada com peer ambiguo entra sem o outro lado.
func Fingerprint(v ExtView) uint64 {
	h := fnv.New64a()
	calls := append([]CallView(nil), v.Calls...)
	sort.Slice(calls, func(i, j int) bool { return calls[i].ID < calls[j].ID })
	h.Write([]byte(v.Registration + "|" + v.DeviceState))
	for _, c := range calls {
		h.Write([]byte("|" + c.ID + "," + c.Direction + "," + c.State))
		if !c.Ambiguous {
			h.Write([]byte("," + c.Peer.Kind + "," + c.Peer.Number))
		}
	}
	return h.Sum64()
}

// FingerprintLike calcula a impressao de v ignorando o outro lado das chamadas
// que a verdade `like` marca como ambiguas.
func FingerprintLike(v, like ExtView) uint64 {
	amb := map[string]bool{}
	for _, c := range like.Calls {
		if c.Ambiguous {
			amb[c.ID] = true
		}
	}
	v.Calls = append([]CallView(nil), v.Calls...)
	for i := range v.Calls {
		v.Calls[i].Ambiguous = amb[v.Calls[i].ID]
	}
	return Fingerprint(v)
}

// Truth devolve a verdade de todos os ramais, ordenada por numero (string).
func (s *Sim) Truth() []ExtView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ExtView, 0, len(s.exts))
	for _, e := range s.exts {
		out = append(out, s.view(e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// TruthOf devolve a verdade de ramais especificos.
func (s *Sim) TruthOf(nums ...string) map[string]ExtView {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := map[string]ExtView{}
	for _, n := range nums {
		if e := s.byNum[n]; e != nil {
			m[n] = s.view(e)
		}
	}
	return m
}

// Counts devolve ramais por registro, ramais em chamada e chamadas ativas (canais de ramal).
func (s *Sim) Counts() (reg map[string]int, inCall, calls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg = map[string]int{}
	for _, e := range s.exts {
		reg[e.reg]++
		if len(e.calls) > 0 {
			inCall++
		}
		calls += len(e.calls)
	}
	return
}

// ActiveChannels e o numero de canais vivos (inclui troncos e Local).
func (s *Sim) ActiveChannels() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.chans)
}

func (s *Sim) Mode() string { return s.cfg.Mode }

// devState calcula o estado do dispositivo a partir da verdade das chamadas.
func (s *Sim) devState(e *ext) string {
	ringing, inuse, hold := 0, 0, 0
	for _, c := range e.calls {
		switch {
		case c.state == "ringing" && c.dest:
			ringing++
		case c.state == "hold":
			hold++
		default:
			inuse++
		}
	}
	switch {
	case ringing > 0 && inuse+hold > 0:
		return "ringinuse"
	case ringing > 0:
		return "ringing"
	case inuse > 0:
		return "inuse"
	case hold > 0:
		return "onhold"
	}
	if e.reg == "registered" {
		return "not_inuse"
	}
	return "unavailable"
}

// ---- reserva de ramais para scripts

func (s *Sim) addFree(e *ext) {
	e.freeIdx = len(s.free)
	s.free = append(s.free, e)
}

func (s *Sim) takeFree(e *ext) {
	i := e.freeIdx
	last := s.free[len(s.free)-1]
	s.free[i] = last
	last.freeIdx = i
	s.free = s.free[:len(s.free)-1]
	e.freeIdx = -1
	e.busy = true
}

func (s *Sim) pickFree() *ext {
	for try := 0; try < 200 && len(s.free) > 0; try++ {
		e := s.free[s.rng.Intn(len(s.free))]
		if e.reg == "registered" {
			s.takeFree(e)
			return e
		}
	}
	return nil
}

func (s *Sim) releaseExt(e *ext) {
	if e.busy {
		e.busy = false
		s.addFree(e)
	}
}

// ---- registro

// SetReg muda o registro de um ramal (uma mutacao propria, com eventos AMI).
func (s *Sim) SetReg(num, to string) bool {
	ok := false
	s.tx(func(t *Tx) {
		if e := s.byNum[num]; e != nil && e.reg != to {
			t.setReg(e, to)
			ok = true
		}
	})
	return ok
}

// Burst desregistra n ramais registrados escolhidos ao acaso, o mais rapido
// possivel, uma mutacao por ramal (cada uma com a propria marca de tempo).
func (s *Sim) Burst(n int) []string {
	s.mu.Lock()
	var cand []*ext
	for _, e := range s.exts {
		if e.reg == "registered" {
			cand = append(cand, e)
		}
	}
	s.rng.Shuffle(len(cand), func(i, j int) { cand[i], cand[j] = cand[j], cand[i] })
	if n > len(cand) {
		n = len(cand)
	}
	cand = cand[:n]
	s.mu.Unlock()
	nums := make([]string, 0, n)
	for _, e := range cand {
		s.tx(func(t *Tx) {
			if e.reg == "registered" {
				t.setReg(e, "unregistered")
			}
		})
		nums = append(nums, e.num)
	}
	return nums
}

// Restore volta a registrar os ramais informados.
func (s *Sim) Restore(nums []string) {
	for _, n := range nums {
		s.SetReg(n, "registered")
	}
}

// Flap faz n ramais registrados alternarem para unreachable.
func (s *Sim) Flap(n int) {
	s.mu.Lock()
	var c []*ext
	for _, e := range s.exts {
		if e.reg == "registered" && len(c) < n {
			c = append(c, e)
		}
	}
	s.mu.Unlock()
	for _, e := range c {
		s.SetReg(e.num, "unreachable")
	}
}

// EmitReload publica o evento Reload (o Asterisk recarregou a configuracao).
func (s *Sim) EmitReload() {
	s.tx(func(t *Tx) { t.ev("Reload", "system,all", "Module", "res_pjsip", "Status", "Enabled") })
}
