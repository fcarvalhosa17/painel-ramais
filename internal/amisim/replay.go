package amisim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	// Declarado: internal/capture e so o leitor do formato de arquivo (CONTRATOS.md secao 18);
	// o simulador nao usa nenhum codigo de AMI, de estado ou de API do daemon.
	"painel-ramais/internal/capture"
)

// Modo de reproducao: serve ao daemon, por TCP, o que uma captura gravou.
//
//   - o banner e os eventos espontaneos saem na ordem e no tempo do arquivo (dividido por speed);
//   - cada resposta de action sai quando o daemon pede a action equivalente (mesmo nome e mesmo
//     Endpoint/Peer), em fila (a n-esima pergunta recebe a n-esima resposta gravada), com o ActionID
//     trocado pelo da pergunta;
//   - um evento so sai depois de o daemon ter feito tantas actions quantas o arquivo tinha antes dele;
//   - um banner no meio do arquivo e uma nova conexao (restart do Asterisk): cada conexao do daemon
//     recebe o trecho seguinte, e a anterior e fechada ao fim dos eventos do seu trecho;
//   - qualquer senha e aceita (o arquivo so tem "***").

type rgroup struct {
	frames [][]byte
	id     string
}

type revent struct {
	idx    int // posicao no arquivo (quadros)
	data   []byte
	gap    time.Duration
	prereq int
}

type rsegment struct {
	banner []byte
	resp   map[string][]*rgroup
	events []revent
	played bool
	lo, hi int // quadros do trecho no arquivo: [lo, hi]
}

// Replayer e o servidor de reproducao.
type Replayer struct {
	Meta, End map[string]any
	Frames    int
	speed     float64
	segs      []*rsegment
	mu        sync.Mutex
	conns     int
	served    map[*rsegment]int
	used      map[string]int
	log       *Sim
	ln        net.Listener
	pauses    []int
	pauseCh   chan int
	resume    chan struct{}
	done      chan struct{}
	doneOnce  sync.Once
}

func hdrOf(data []byte) map[string]string {
	h := map[string]string{}
	for _, ln := range strings.Split(string(data), "\r\n") {
		if k, v, ok := strings.Cut(ln, ":"); ok {
			k = strings.ToLower(strings.TrimSpace(k))
			if _, dup := h[k]; !dup {
				h[k] = strings.TrimSpace(v)
			}
		}
	}
	return h
}

func actionKey(h map[string]string) string {
	k := strings.ToLower(h["action"])
	for _, p := range []string{"endpoint", "peer", "module"} {
		if v := h[p]; v != "" {
			return k + "|" + strings.ToLower(v)
		}
	}
	return k
}

// NewReplayer le a captura. speed: 1 = tempo gravado, 10 = dez vezes mais rapido, 0 = sem esperas.
// pauses: quantidades de quadros do arquivo em que a reproducao para (ver PauseCh).
func NewReplayer(path string, speed float64, pauses []int) (*Replayer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	meta, frames, end, err := capture.Read(f)
	if err != nil {
		return nil, err
	}
	r := &Replayer{Meta: meta, End: end, Frames: len(frames), speed: speed, served: map[*rsegment]int{}, used: map[string]int{},
		log: &Sim{actCount: map[string]int{}}, pauseCh: make(chan int, 1), resume: make(chan struct{}, 1), done: make(chan struct{})}
	r.pauses = append(r.pauses, pauses...)
	sort.Ints(r.pauses)
	var seg *rsegment
	var byID map[string]*rgroup
	nActions := 0
	var prev time.Time
	for i, fr := range frames {
		gap := time.Duration(0)
		if !prev.IsZero() && fr.At.After(prev) {
			gap = fr.At.Sub(prev)
		}
		prev = fr.At
		if fr.Dir == capture.DirRecv && bytes.HasPrefix(fr.Data, []byte("Asterisk Call Manager")) {
			seg = &rsegment{banner: fr.Data, resp: map[string][]*rgroup{}, lo: i}
			r.segs = append(r.segs, seg)
			byID, nActions = map[string]*rgroup{}, 0
			continue
		}
		if seg == nil {
			continue
		}
		seg.hi = i
		h := hdrOf(fr.Data)
		if fr.Dir == capture.DirSent {
			nActions++
			g := &rgroup{id: h["actionid"]}
			if g.id != "" {
				byID[g.id] = g
			}
			k := actionKey(h)
			seg.resp[k] = append(seg.resp[k], g)
			continue
		}
		if g := byID[h["actionid"]]; g != nil && h["actionid"] != "" {
			g.frames = append(g.frames, fr.Data)
			continue
		}
		seg.events = append(seg.events, revent{idx: i, data: fr.Data, gap: gap, prereq: nActions})
	}
	if len(r.segs) == 0 {
		return nil, fmt.Errorf("captura sem banner: nada a reproduzir")
	}
	return r, nil
}

// PauseCh recebe N quando a reproducao para antes de emitir o primeiro evento com posicao >= N.
// Resume() continua.
func (r *Replayer) PauseCh() <-chan int { return r.pauseCh }
func (r *Replayer) Resume()             { r.resume <- struct{}{} }

// Done fecha quando o ultimo trecho terminou de emitir seus eventos.
func (r *Replayer) Done() <-chan struct{} { return r.done }

// Listen abre a porta AMI.
func (r *Replayer) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	r.ln = ln
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(nc)
		}
	}()
	return nil
}

func (r *Replayer) Addr() string { return r.ln.Addr().String() }
func (r *Replayer) Close()       { r.ln.Close() }

// Actions devolve as actions recebidas e as violacoes da allowlist.
func (r *Replayer) Actions() (map[string]int, []ActionRec) { return r.log.Actions() }

type rconn struct {
	nc net.Conn
	mu sync.Mutex
}

func (c *rconn) write(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.nc.Write(b)
	return err
}

func (r *Replayer) serve(nc net.Conn) {
	defer nc.Close()
	r.mu.Lock()
	n := r.conns
	r.conns++
	si := n
	if si >= len(r.segs) {
		si = len(r.segs) - 1
	}
	seg := r.segs[si]
	first := !seg.played
	seg.played = true
	r.mu.Unlock()
	c := &rconn{nc: nc}
	c.write(seg.banner)
	if first {
		go r.emit(c, seg, si == len(r.segs)-1)
	}
	br := bufio.NewReaderSize(nc, 64<<10)
	h := map[string]string{}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(h) > 0 {
				r.answer(c, seg, h)
				h = map[string]string{}
			}
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			k = strings.ToLower(strings.TrimSpace(k))
			if _, dup := h[k]; !dup {
				h[k] = strings.TrimSpace(v)
			}
		}
	}
}

func (r *Replayer) answer(c *rconn, seg *rsegment, h map[string]string) {
	raw := h["action"]
	canon, ok := allowlist[strings.ToLower(raw)]
	if !ok {
		r.log.logAction(raw, false)
		c.write(kvMsg("Response: Error\r\n"+idLine(h["actionid"]), []string{"Message", "Permission denied"}))
		return
	}
	r.log.logAction(canon, true)
	r.mu.Lock()
	r.served[seg]++
	key := actionKey(h)
	q := seg.resp[key]
	k := key + fmt.Sprintf("#%p", seg)
	var g *rgroup
	if len(q) > 0 {
		i := r.used[k]
		if i >= len(q) {
			i = len(q) - 1 // fila esgotada: repete a ultima resposta gravada
		} else {
			r.used[k]++
		}
		g = q[i]
	}
	r.mu.Unlock()
	id := h["actionid"]
	if g == nil || len(g.frames) == 0 {
		switch canon {
		case "Ping":
			c.write(kvMsg("Response: Success\r\n"+idLine(id), []string{"Ping", "Pong"}))
		case "Logoff":
			c.write(kvMsg("Response: Goodbye\r\n"+idLine(id), []string{"Message", "Thanks for all the fish."}))
		case "Login":
			c.write(kvMsg("Response: Success\r\n"+idLine(id), []string{"Message", "Authentication accepted"}))
		default:
			c.write(kvMsg("Response: Error\r\n"+idLine(id), []string{"Message", "Nao ha resposta gravada na captura para " + key}))
		}
		return
	}
	var buf bytes.Buffer
	for _, fr := range g.frames {
		buf.Write(swapActionID(fr, g.id, id))
	}
	c.write(buf.Bytes())
}

// swapActionID troca (ou tira) a linha ActionID de um quadro gravado.
func swapActionID(fr []byte, old, id string) []byte {
	if old == "" {
		return fr
	}
	from := []byte("ActionID: " + old + "\r\n")
	to := []byte{}
	if id != "" {
		to = []byte("ActionID: " + id + "\r\n")
	}
	return bytes.Replace(fr, from, to, 1)
}

func (r *Replayer) servedCount(seg *rsegment) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.served[seg]
}

func (r *Replayer) emit(c *rconn, seg *rsegment, last bool) {
	fired := map[int]bool{}
	for _, ev := range seg.events {
		for r.servedCount(seg) < ev.prereq {
			time.Sleep(2 * time.Millisecond)
		}
		for _, n := range r.pauses {
			if n >= seg.lo && ev.idx >= n && !fired[n] {
				fired[n] = true
				r.pauseCh <- n
				<-r.resume
			}
		}
		if r.speed > 0 && ev.gap > 0 {
			time.Sleep(time.Duration(float64(ev.gap) / r.speed))
		}
		if c.write(ev.data) != nil {
			return
		}
	}
	for _, n := range r.pauses { // pausas depois do ultimo evento do trecho
		if n >= seg.lo && n <= seg.hi+1 && !fired[n] {
			fired[n] = true
			r.pauseCh <- n
			<-r.resume
		}
	}
	if last {
		r.doneOnce.Do(func() { close(r.done) })
		return
	}
	time.Sleep(200 * time.Millisecond)
	c.nc.Close() // fim do trecho: o Asterisk reiniciou
}

// Checkpoint e o estado esperado depois de Quadros quadros da captura ("com quem fala").
type Checkpoint struct {
	Quadros int                `json:"quadros"`
	Rotulo  string             `json:"rotulo"`
	Ramais  map[string]ExtView `json:"ramais"`
}

// Expected e o arquivo <captura>.esperado.json: o oraculo de uma captura.
type Expected struct {
	Tecnologia  string       `json:"tecnologia"`
	Checkpoints []Checkpoint `json:"checkpoints"`
}

// LoadExpected le o oraculo de uma captura.
func LoadExpected(path string) (*Expected, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e Expected
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	return &e, nil
}
