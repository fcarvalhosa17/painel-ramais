package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"painel-ramais/internal/model"
)

const (
	ringSize        = 40
	snapshotMaxAge  = time.Second // server_time do snapshot nunca fica mais velho que isso
	debugBatchLines = 20
)

// entry e um ramal da projecao publica: o JSON ja serializado serve ao snapshot
// e ao delta; a struct serve a /api/groups e /debug.
type entry struct {
	ext model.Extension
	raw []byte
}

type ringItem struct {
	from, to uint64
	n        int
	nums     []string
	data     []byte
	at       time.Time
}

// snapEntry e o snapshot serializado de uma versao; o gzip e feito sob demanda, uma vez.
type snapEntry struct {
	version uint64
	at      time.Time
	raw     []byte
	once    sync.Once
	gz      []byte
}

// Gzip devolve o corpo comprimido (calculado uma vez, fora da goroutine do hub).
func (s *snapEntry) Gzip() []byte {
	s.once.Do(func() {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		w.Write(s.raw)
		w.Close()
		s.gz = b.Bytes()
	})
	return s.gz
}

type groupsEntry struct {
	version uint64
	raw     []byte
}

// Pub e a foto leve (sem passar pela goroutine do hub) usada pelo /api/health.
type Pub struct {
	Epoch   string
	Version uint64
	Clients int
}

// Hub e o dono da projecao publica do estado, do ring buffer e dos clientes
// WebSocket (ADR-001). Toda mutacao acontece na goroutine de Run.
type Hub struct {
	log *slog.Logger
	now func() time.Time

	reqs    chan func()
	stopped chan struct{}
	pub     atomic.Pointer[Pub]

	// Campos abaixo: so a goroutine de Run.
	epoch      string
	version    uint64
	ami        bool
	tech       model.Technology
	exts       map[string]*entry
	order      []string
	orderDirty bool
	groups     []model.Group // catalogo completo (inclui grupo sem faixa e sem ramal)
	ring       []ringItem
	clients    map[*client]struct{}
	snap       *snapEntry
	grp        *groupsEntry
}

// NewHub cria o hub. Run precisa rodar antes de qualquer outra chamada.
func NewHub(log *slog.Logger, now func() time.Time) *Hub {
	if now == nil {
		now = time.Now
	}
	h := &Hub{log: log, now: now, reqs: make(chan func()), stopped: make(chan struct{}),
		exts: map[string]*entry{}, clients: map[*client]struct{}{}}
	h.pub.Store(&Pub{})
	return h
}

// Pub devolve epoch, versao e clientes sem bloquear.
func (h *Hub) Pub() Pub { return *h.pub.Load() }

// Run consome as saidas do estado ate ctx terminar; ao sair fecha os clientes com 1001.
func (h *Hub) Run(ctx context.Context, in <-chan model.Output) {
	defer close(h.stopped)
	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				c.kick(websocket.StatusGoingAway, "daemon parando")
			}
			return
		case o := <-in:
			h.handle(o)
		case f := <-h.reqs:
			f()
		}
	}
}

// do executa fn na goroutine do hub; false se o hub ja parou.
func (h *Hub) do(fn func()) bool {
	done := make(chan struct{})
	select {
	case h.reqs <- func() { fn(); close(done) }:
		<-done
		return true
	case <-h.stopped:
		return false
	}
}

func (h *Hub) publish() {
	h.pub.Store(&Pub{Epoch: h.epoch, Version: h.version, Clients: len(h.clients)})
}

func (h *Hub) handle(o model.Output) {
	switch v := o.(type) {
	case model.Reset:
		h.applyReset(v)
	case model.Batch:
		h.applyBatch(v)
	}
	h.publish()
}

func (h *Hub) applyReset(r model.Reset) {
	h.epoch, h.version, h.ami, h.tech, h.groups = r.Epoch, r.Version, r.AMIConnected, r.Technology, r.Groups
	h.exts = make(map[string]*entry, len(r.Extensions))
	for _, x := range r.Extensions {
		h.put(x)
	}
	h.orderDirty, h.ring, h.snap, h.grp = true, nil, nil, nil
	msg := reloadFrame("epoch_changed", h.epoch)
	for c := range h.clients {
		c.sendReload(msg)
		delete(h.clients, c)
	}
}

func (h *Hub) put(x model.Extension) {
	raw, err := json.Marshal(x)
	if err != nil {
		h.log.Error("serializar ramal", "number", x.Number, "erro", err.Error())
		return
	}
	if _, known := h.exts[x.Number]; !known {
		h.orderDirty = true
	}
	h.exts[x.Number] = &entry{ext: x, raw: raw}
}

func (h *Hub) applyBatch(b model.Batch) {
	if b.Epoch != h.epoch {
		h.log.Error("lote de outro epoch descartado", "lote", b.Epoch, "hub", h.epoch)
		return
	}
	raws := make([][]byte, 0, len(b.Extensions))
	nums := make([]string, 0, len(b.Extensions)+len(b.Removed))
	for _, x := range b.Extensions {
		h.put(x)
		raws = append(raws, h.exts[x.Number].raw)
		nums = append(nums, x.Number)
	}
	if b.Groups != nil { // catalogo mudou (grupo criado/renomeado/editado/apagado): mesmo epoch, vai no delta
		h.groups = b.Groups
	}
	for _, n := range b.Removed { // ramal apagado no PBX: sai da projecao (e do snapshot) sem epoch novo
		if _, ok := h.exts[n]; ok {
			delete(h.exts, n)
			h.orderDirty = true
		}
		nums = append(nums, "-"+n)
	}
	h.version, h.ami = b.ToVersion, b.AMIConnected
	h.snap, h.grp = nil, nil
	frame := deltaFrame(b, raws, h.now())
	h.ring = append(h.ring, ringItem{from: b.FromVersion, to: b.ToVersion, n: len(raws), nums: nums, data: frame, at: h.now()})
	if len(h.ring) > ringSize {
		h.ring = append([]ringItem(nil), h.ring[len(h.ring)-ringSize:]...)
	}
	for c := range h.clients {
		if !c.send(frame) { // fila cheia: cliente lento sai, ninguem espera
			delete(h.clients, c)
			c.kick(websocket.StatusCode(closeSlow), "cliente lento")
		}
	}
}

// join aplica a tabela de decisao do hello (ADR-001, item 6). Devolve "" se o
// cliente entrou (com replay ja na fila) ou o motivo do reload (ja enfileirado).
func (h *Hub) join(c *client, epoch string, version uint64) string {
	reload := ""
	switch {
	case epoch != h.epoch:
		reload = "epoch_changed"
	case version > h.version:
		reload = "version_ahead"
	case version < h.version && (len(h.ring) == 0 || h.ring[0].from > version):
		reload = "version_gap"
	}
	if reload != "" {
		c.sendReload(reloadFrame(reload, h.epoch))
		return reload
	}
	for _, it := range h.ring {
		if it.to > version && !c.send(it.data) {
			c.kick(websocket.StatusCode(closeSlow), "cliente lento")
			return "slow"
		}
	}
	h.clients[c] = struct{}{}
	h.publish()
	return ""
}

// Join registra o cliente e responde ao hello. ok=false se o hub parou.
func (h *Hub) Join(c *client, epoch string, version uint64) (reason string, ok bool) {
	ok = h.do(func() { reason = h.join(c, epoch, version) })
	return reason, ok
}

// Leave tira o cliente da lista.
func (h *Hub) Leave(c *client) {
	h.do(func() {
		delete(h.clients, c)
		h.publish()
	})
}

// Clients devolve os clientes ligados (para revalidar sessoes fora do hub).
func (h *Hub) Clients() []*client {
	var out []*client
	h.do(func() {
		for c := range h.clients {
			out = append(out, c)
		}
	})
	return out
}

func (h *Hub) sortedOrder() []string {
	if h.orderDirty {
		h.order = h.order[:0]
		for n := range h.exts {
			h.order = append(h.order, n)
		}
		sort.Strings(h.order)
		h.orderDirty = false
	}
	return h.order
}

// Snapshot devolve o snapshot serializado da versao atual (cache de ate 1 s).
func (h *Hub) Snapshot() (*snapEntry, bool) {
	var s *snapEntry
	ok := h.do(func() {
		now := h.now()
		if h.snap == nil || now.Sub(h.snap.at) >= snapshotMaxAge {
			h.snap = h.buildSnapshot(now)
		}
		s = h.snap
	})
	return s, ok
}

func (h *Hub) buildSnapshot(now time.Time) *snapEntry {
	order := h.sortedOrder()
	var b bytes.Buffer
	b.Grow(len(order)*320 + 256)
	b.WriteString(`{"success":true,"data":{"epoch":`)
	b.Write(quote(h.epoch))
	b.WriteString(`,"version":` + strconv.FormatUint(h.version, 10))
	b.WriteString(`,"server_time":`)
	b.Write(quote(model.NewStamp(now).UTC().Format(stampLayout)))
	b.WriteString(`,"technology":`)
	b.Write(techJSON(h.tech))
	b.WriteString(`,"ami_connected":` + strconv.FormatBool(h.ami) + `,"extensions":[`)
	for i, n := range order {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(h.exts[n].raw)
	}
	b.WriteString(`],"groups":`)
	b.Write(catalogJSON(h.groups))
	b.WriteString(`},"error":null,"meta":{"count":` + strconv.Itoa(len(order)) + `}}`)
	return &snapEntry{version: h.version, at: now, raw: b.Bytes()}
}

// Groups devolve o corpo de /api/groups da versao atual.
func (h *Hub) Groups() ([]byte, bool) {
	var raw []byte
	ok := h.do(func() {
		if h.grp == nil || h.grp.version != h.version {
			h.grp = &groupsEntry{version: h.version, raw: h.buildGroups()}
		}
		raw = h.grp.raw
	})
	return raw, ok
}

// ExtInfo e o que o relatorio de registro (CONTRATOS 4.7) le da projecao: nome, grupos atuais e o
// inicio do intervalo fora do ar ainda aberto. Groups e DownSince sao somente leitura.
type ExtInfo struct {
	Name      string
	Groups    []string
	DownSince *time.Time
}

func infoOf(e *entry) ExtInfo {
	i := ExtInfo{Groups: e.ext.Groups, DownSince: e.ext.DownSince}
	if e.ext.Name != nil {
		i.Name = *e.ext.Name
	}
	return i
}

// Infos devolve nome, grupos e DownSince de todos os ramais da projecao atual. false se o hub parou.
// ponytail: copia o mapa inteiro a cada chamada (10 mil ramais, uma chamada a cada 5 s por aba aberta
// custa microssegundos); se a medicao reprovar, vira um indice atualizado no applyBatch.
func (h *Hub) Infos() (map[string]ExtInfo, bool) {
	var out map[string]ExtInfo
	ok := h.do(func() {
		out = make(map[string]ExtInfo, len(h.exts))
		for n, e := range h.exts {
			out[n] = infoOf(e)
		}
	})
	return out, ok
}

// Info devolve os dados de um ramal (present = existe na projecao). ok false se o hub parou.
func (h *Hub) Info(number string) (info ExtInfo, present, ok bool) {
	ok = h.do(func() {
		if e := h.exts[number]; e != nil {
			info, present = infoOf(e), true
		}
	})
	return info, present, ok
}

type rangeJSON struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type groupJSON struct {
	Name       string      `json:"name"`
	Ranges     []rangeJSON `json:"ranges"`
	Extensions []string    `json:"extensions"`
}

func (h *Hub) buildGroups() []byte {
	by := map[string]*groupJSON{}
	var names []string
	get := func(name string) *groupJSON {
		g := by[name]
		if g == nil {
			g = &groupJSON{Name: name, Ranges: []rangeJSON{}, Extensions: []string{}}
			by[name] = g
			names = append(names, name)
		}
		return g
	}
	for _, cg := range h.groups {
		g := get(cg.Name)
		for _, r := range cg.Ranges {
			g.Ranges = append(g.Ranges, rangeJSON{r.Start, r.End})
		}
	}
	for _, n := range h.sortedOrder() {
		for _, name := range h.exts[n].ext.Groups { // N:N: o ramal aparece em cada grupo dele
			g := get(name)
			g.Extensions = append(g.Extensions, n)
		}
	}
	if g := by[model.NoGroup]; g != nil && len(g.Extensions) == 0 {
		delete(by, model.NoGroup)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == model.NoGroup) != (names[j] == model.NoGroup) {
			return names[j] == model.NoGroup
		}
		return names[i] < names[j]
	})
	out := make([]*groupJSON, 0, len(names))
	for _, n := range names {
		if g := by[n]; g != nil {
			out = append(out, g)
		}
	}
	return mustJSON(envelope{Success: true, Data: out, Meta: map[string]int{"count": len(out)}})
}

// DebugInfo e o que a pagina /debug mostra.
type DebugInfo struct {
	Epoch   string
	Version uint64
	Clients int
	Batches []ringItem
	Calls   []DebugCall
}

// DebugCall e uma chamada ativa para a pagina /debug.
type DebugCall struct {
	Extension string
	Call      model.Call
}

// Debug devolve os ultimos lotes e as chamadas ativas.
func (h *Hub) Debug() (DebugInfo, bool) {
	var d DebugInfo
	ok := h.do(func() {
		d = DebugInfo{Epoch: h.epoch, Version: h.version, Clients: len(h.clients)}
		from := 0
		if len(h.ring) > debugBatchLines {
			from = len(h.ring) - debugBatchLines
		}
		d.Batches = append(d.Batches, h.ring[from:]...)
		for _, n := range h.sortedOrder() {
			for _, c := range h.exts[n].ext.Calls {
				d.Calls = append(d.Calls, DebugCall{Extension: n, Call: c})
			}
		}
	})
	return d, ok
}
