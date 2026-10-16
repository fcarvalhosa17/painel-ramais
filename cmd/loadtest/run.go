package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"painel-ramais/internal/amisim"
)

const (
	amiUser   = "painel-ramais"
	amiSecret = "segredo-de-teste"
)

type opts struct {
	daemon                  string
	modes                   []string
	exts, calls, burst      int
	duration, talk, dropFor time.Duration
	asterisk                string
	skipMatrix              bool
	daemonInfo              string
	fillRate                int
	fillTimeout             time.Duration
	replaySpeed             float64
	capturesDir             string
	heap                    bool          // mede heap pos-GC e perfis pprof (daemon compilado com -tags debug, com PAINEL_DEBUG_ADDR)
	warmup                  time.Duration // aquecimento depois do preenchimento de nomes, fora da regressao
	outDir                  string        // onde gravar CSV/pb.gz do heap
}

type Check struct {
	Name   string
	OK     bool
	Detail string
}

type Row struct {
	Scenario, Step string
	OK             bool
	Conv           time.Duration
	Detail         string
}

type RestoreRow struct {
	Scenario, Round string
	OK              bool
	Detail          string
}

type SnapMeasure struct {
	Exts, Calls, Raw, Gz int
	Fetch                time.Duration
	GzEncoded            bool
}

type DeltaStat struct {
	N                      int
	AvgBytes, AvgExts      float64
	P50, P99, Max          int
	BytesPerExt, BytesPerS float64
}

type BurstRes struct {
	N            int
	Emit         time.Duration
	Converge     time.Duration
	AllSeen      bool
	Stuck        int
	RestoreAll   bool
	RestoreAfter time.Duration
}

type DropRes struct {
	Ran                        bool
	DetectDown, RecoverAfter   time.Duration
	DownSamples                int
	MaxUnregWhileDown          int
	MinUnknownWhileDown        int
	MaxCallsWhileDown          int
	SnapshotsWhileDown         int
	EpochBefore, EpochDown     string
	EpochUp                    string
	AMIFlagDown                bool
	Reloads                    []string
	CloseCodes                 []int
	Resyncs                    int
	RegisteredAfter, ExtsAfter int
	WSConvergedAfter           bool
	Err                        string
	Samples                    []DropSample
	ConsistentAfter            time.Duration // da primeira deteccao ate o primeiro snapshot todo 'unknown'
	Regressed                  bool          // snapshot inconsistente depois de ter ficado consistente
	StaleSnapshots             int           // snapshots com ami_connected=false e ramais ainda com estado antigo
	EpochVisibleAfter          time.Duration // do inicio da queda ate o health mostrar epoch novo
	FlagStale                  int           // snapshots ainda com ami_connected=true depois de o health acusar a queda
	FlagFlipAfter              time.Duration // da primeira deteccao no health ate o snapshot passar a ami_connected=false
}

type DropSample struct {
	At                    time.Duration
	Unreg, Unknown, Calls int
	Registered            int
	Epoch                 string
	Consistent            bool
}

type FinalRes struct {
	Compared      int
	SnapDiffs     []Diff
	WSDiffs       []Diff
	SnapDiffExts  int
	WSDiffExts    int
	Pending       int
	Tainted       int
	Torn          int
	NoPeerOn      bool
	NoPeer        int
	NoPeerSamples []string
	Unexpected    int
	UnexpSamples  []string
	Graced        int
	CallsAtEnd    int
	Gaps          int
	ReloadsSteady int
}

type MemSample struct {
	T     time.Time
	Phase string
	RSS   uint64
	CPU   time.Duration
}

type MemRes struct {
	Err                                 string
	Boot, AfterSnap, LoadStart, LoadEnd float64 // MiB
	Peak                                float64
	SlopeMiBPerMin                      float64
	SteadyDur                           time.Duration
	CPUPct                              float64 // % de 1 nucleo no regime inteiro
	CPUFill, CPUPost                    float64 // % de 1 nucleo durante o preenchimento de nomes e depois dele
	RSSFillEnd                          float64
	Samples                             int
}

type HealthRes struct {
	Samples     int
	MaxEventsPS float64
	MaxQueue    int
	MaxLagMs    float64
	WSClients   int
	Overflows   int
	LastStatus  string
	Reconnects  int
	DepsSummary string
}

type ActionsRes struct {
	Counts     map[string]int
	Violations []string
	OnlyAllow  bool
}

type Result struct {
	Mode, Err, Skipped string
	Started            time.Time
	Wall               time.Duration
	DaemonVersion      string
	AsteriskVersion    string
	DetectedTech       string
	Boot               time.Duration
	Checks             []Check
	SnapIdle, SnapLoad SnapMeasure
	Phases             map[string]time.Duration
	Lat                map[string]Stats
	LatAll             Stats
	Deltas             map[string]DeltaStat
	Rows               []Row
	Restore            []RestoreRow
	Fill               FillRes
	Burst              BurstRes
	Drop               DropRes
	Final              FinalRes
	Mem                MemRes
	Heap               HeapRes
	Health             HealthRes
	Actions            ActionsRes
	NamesFilled        int
	IdleDiffs          int
	UAFilled           int
	NamesFinal         int
	UAFinal            int
	Notes              []string
}

func (r *Result) check(name string, ok bool, f string, a ...any) {
	r.Checks = append(r.Checks, Check{name, ok, fmt.Sprintf(f, a...)})
}

// jitter espera um tempo aleatorio ate 300 ms: tira a sincronia entre o teste e o lote de 250 ms do daemon.
func (x *runner) jitter() { time.Sleep(time.Duration(rand.Int63n(int64(300 * time.Millisecond)))) }

func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type runner struct {
	o       opts
	r       *Result
	sim     *amisim.Sim
	trk     *Tracker
	d       *Daemon
	ws      *WSClient
	logf    func(string, ...any)
	mu      sync.Mutex
	mem     []MemSample
	hs      []Health
	pT      time.Time
	pName   string
	simHTTP string
	bootAt  time.Time
}

func (x *runner) phase(name string) {
	x.mu.Lock()
	if x.pName != "" {
		x.r.Phases[x.pName] += time.Since(x.pT)
	}
	x.pName, x.pT = name, time.Now()
	x.mu.Unlock()
	x.trk.SetPhase(name)
	x.logf("[%s] fase %s", x.r.Mode, name)
}

// runMode roda a bateria completa num modo (sip ou pjsip). Erros fatais ficam em Result.Err.
func runMode(o opts, mode string, logf func(string, ...any)) *Result {
	r := &Result{Mode: mode, Started: time.Now(), Phases: map[string]time.Duration{}, Lat: map[string]Stats{}, Deltas: map[string]DeltaStat{}}
	defer func() { r.Wall = time.Since(r.Started) }()
	sim, err := amisim.New(amisim.Config{Mode: mode, Exts: o.exts, User: amiUser, Secret: amiSecret, Asterisk: o.asterisk, Seed: 7})
	if err != nil {
		r.Err = err.Error()
		return r
	}
	if err := sim.Listen("127.0.0.1:0"); err != nil {
		r.Err = err.Error()
		return r
	}
	defer sim.Close()
	hl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.Err = err.Error()
		return r
	}
	hsrv := &http.Server{Handler: sim.Handler()}
	go hsrv.Serve(hl)
	defer hsrv.Close()

	x := &runner{o: o, r: r, sim: sim, trk: NewTracker(), logf: logf, simHTTP: hl.Addr().String()}
	sim.OnCommit = x.trk.Hook
	x.phase("boot")
	t0 := time.Now()
	d, err := startDaemon(o, sim.Addr(), amiUser, amiSecret, "auto")
	if err != nil {
		r.Err = "subir o daemon: " + err.Error()
		return r
	}
	x.d = d
	defer func() {
		d.Stop()
		if r.Err == "" {
			d.Cleanup()
		} else {
			r.Notes = append(r.Notes, "log do daemon preservado em "+d.dir)
		}
	}()
	stopSamp := make(chan struct{})
	var sampWG sync.WaitGroup
	sampWG.Add(1)
	go x.sampler(stopSamp, &sampWG)
	defer func() { close(stopSamp); sampWG.Wait(); x.summarizeMem() }()

	var h *Health
	if !waitUntil(180*time.Second, func() bool {
		h, err = d.Health()
		return err == nil && h.AMI.Connected && h.Extensions.Total >= o.exts
	}) {
		r.Err = fmt.Sprintf("daemon nao ficou pronto em 180 s (health=%+v err=%v); log: %s", h, err, d.LogTail())
		return r
	}
	r.Boot = time.Since(t0)
	x.bootAt = time.Now()
	r.DaemonVersion, r.AsteriskVersion, r.DetectedTech = h.DaemonVersion, h.AMI.AsteriskVersion, h.AMI.Technology
	r.check("modo auto detecta a tecnologia do simulador", h.AMI.Technology == mode, "health.ami.technology=%q esperado %q", h.AMI.Technology, mode)
	r.check("versao do Asterisk lida via CoreSettings", h.AMI.AsteriskVersion == o.asterisk, "health.ami.asterisk_version=%q esperado %q", h.AMI.AsteriskVersion, o.asterisk)

	x.idleSnapshot()
	x.phase("ws")
	x.ws = NewWSClient(d, x.trk, x.trk.Phase)
	x.ws.resyncCb = func() {
		x.trk.Grace(time.Now().Add(3 * time.Second))
		var withCalls []string
		for n, v := range x.ws.Snapshot() {
			if len(v.Calls) > 0 {
				withCalls = append(withCalls, n)
			}
		}
		x.trk.Taint(withCalls)
	}
	defer x.ws.Stop()
	if _, err := x.ws.Resync(); err != nil {
		r.Err = "abrir /ws: " + err.Error()
		return r
	}
	if !o.skipMatrix {
		x.scenarioMatrix()
		x.restoreRounds()
	}
	x.loadPhases()
	x.finish()
	return r
}

func (x *runner) idleSnapshot() {
	r := x.r
	x.phase("snapshot-ocioso")
	var si *SnapInfo
	var err error
	for i := 0; i < 3; i++ {
		if si, err = x.d.Snapshot(); err != nil {
			r.Err = err.Error()
			return
		}
	}
	s := si.Snap
	r.SnapIdle = SnapMeasure{Exts: len(s.Extensions), Calls: si.Calls, Raw: si.Raw, Gz: si.Gz, Fetch: si.Dur, GzEncoded: si.GzEncoded}
	r.check("snapshot: envelope {success,data,error,meta} e meta.count", si.EnvelopeOK && si.MetaCount == len(s.Extensions), "success=%v meta.count=%d ramais=%d", si.EnvelopeOK, si.MetaCount, len(s.Extensions))
	r.check("snapshot: ramais ordenados por number (string)", si.SortedOK, "")
	r.check("snapshot: Content-Encoding gzip com Accept-Encoding gzip", si.GzEncoded, "")
	r.check("snapshot: ami_connected=true e technology correta", s.AMIConnected && s.Technology != nil && *s.Technology == x.r.Mode, "ami=%v tech=%s", s.AMIConnected, deref(s.Technology))
	r.check(fmt.Sprintf("snapshot: %d ramais (sem troncos)", x.o.exts), len(s.Extensions) == x.o.exts, "%d ramais, esperado %d", len(s.Extensions), x.o.exts)
	truth := x.sim.Truth()
	got := map[string]View{}
	for _, v := range s.Extensions {
		got[v.Number] = v
		if v.Name != "" {
			r.NamesFilled++
		}
		if v.UserAgent != "" {
			r.UAFilled++
		}
	}
	bad := 0
	for _, t := range truth {
		g, ok := got[t.Number]
		if !ok || len(compareView(g, t, x.r.Mode)) > 0 {
			bad++
		}
	}
	r.IdleDiffs = bad
	r.check("snapshot ocioso == verdade do simulador (registro, dispositivo, grupo)", bad == 0, "%d ramais divergentes de %d", bad, len(truth))
}

// converge espera o estado local (WS) dos ramais igualar a verdade.
func (x *runner) converge(nums []string, timeout time.Duration) (bool, time.Duration, string) {
	t0 := time.Now()
	var last string
	ok := waitUntil(timeout, func() bool {
		last = ""
		tr := x.sim.TruthOf(nums...)
		for _, n := range nums {
			g, _ := x.ws.View(n)
			if ds := compareView(g, tr[n], x.r.Mode); len(ds) > 0 {
				last = ds[0].Ext + ": " + ds[0].What
				return false
			}
		}
		return true
	})
	return ok, time.Since(t0), last
}

func (x *runner) scenarioMatrix() {
	r := x.r
	x.phase("isolada")
	var regs []string
	for _, t := range x.sim.Truth() {
		if t.Registration == "registered" && len(t.Calls) == 0 {
			regs = append(regs, t.Number)
		}
	}
	pos := 0
	for i, kind := range amisim.Kinds {
		need := amisim.ExtsNeeded(kind)
		exts := regs[pos : pos+need]
		pos += need
		sc, err := x.sim.NewScript(kind, amisim.Parts{Exts: exts, Trunk: fmt.Sprintf("trunk%d", i%4+1), XNum: fmt.Sprintf("119%08d", 12345600+i), XName: "ACME LTDA", DID: "30001000"}, nil)
		if err != nil {
			r.Rows = append(r.Rows, Row{kind, "montar", false, 0, err.Error()})
			continue
		}
		for si, st := range sc.Steps {
			x.jitter()
			sc.Do(si)
			ok, dt, why := x.converge(exts, 6*time.Second)
			r.Rows = append(r.Rows, Row{kind, st.Name, ok, dt, why})
		}
		sc.Abort()
		ok, dt, why := x.converge(exts, 6*time.Second)
		r.Rows = append(r.Rows, Row{kind, "limpo", ok, dt, why})
	}
}

func (x *runner) sampler(stop chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	tk := time.NewTicker(2 * time.Second)
	defer tk.Stop()
	n := 0
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			rss, cpu, err := procStats(x.d.Pid())
			if err != nil {
				x.mu.Lock()
				x.r.Mem.Err = err.Error()
				x.mu.Unlock()
				continue
			}
			x.mu.Lock()
			x.mem = append(x.mem, MemSample{T: time.Now(), Phase: x.trk.Phase(), RSS: rss, CPU: cpu})
			x.mu.Unlock()
			n++
			if n%2 == 0 {
				if h, err := x.d.Health(); err == nil {
					x.mu.Lock()
					x.hs = append(x.hs, *h)
					x.mu.Unlock()
				}
			}
		}
	}
}

const mib = 1 << 20

func (x *runner) summarizeMem() {
	x.mu.Lock()
	defer x.mu.Unlock()
	m := &x.r.Mem
	var steady []MemSample
	for _, s := range x.mem {
		if s.RSS == 0 {
			continue
		}
		m.Samples++
		if v := float64(s.RSS) / mib; v > m.Peak {
			m.Peak = v
		}
		if m.Boot == 0 {
			m.Boot = float64(s.RSS) / mib
		}
		if s.Phase == "carga" || s.Phase == "carga-pos" {
			steady = append(steady, s)
		}
	}
	if len(steady) >= 2 {
		a, b := steady[0], steady[len(steady)-1]
		m.LoadStart, m.LoadEnd = float64(a.RSS)/mib, float64(b.RSS)/mib
		m.SteadyDur = b.T.Sub(a.T)
		if m.SteadyDur > 0 {
			m.CPUPct = float64(b.CPU-a.CPU) / float64(m.SteadyDur) * 100
		}
		m.CPUFill = cpuPct(steady, "carga")
		m.CPUPost = cpuPct(steady, "carga-pos")
		for _, s := range steady {
			if s.Phase == "carga" {
				m.RSSFillEnd = float64(s.RSS) / mib
			}
		}
		// regressao linear RSS(MiB) x tempo(min), descartando o primeiro terco (aquecimento)
		tail := steady[len(steady)/3:]
		if len(tail) >= 3 {
			var sx, sy, sxx, sxy float64
			for _, s := range tail {
				xv := s.T.Sub(tail[0].T).Minutes()
				yv := float64(s.RSS) / mib
				sx, sy, sxx, sxy = sx+xv, sy+yv, sxx+xv*xv, sxy+xv*yv
			}
			n := float64(len(tail))
			if den := n*sxx - sx*sx; den != 0 {
				m.SlopeMiBPerMin = (n*sxy - sx*sy) / den
			}
		}
	}
	for _, s := range x.mem {
		if s.Phase == "snapshot-ocioso" || s.Phase == "ws" || s.Phase == "isolada" {
			m.AfterSnap = float64(s.RSS) / mib
		}
	}
	// saude
	hr := &x.r.Health
	for _, h := range x.hs {
		hr.Samples++
		if h.EventsPerS > hr.MaxEventsPS {
			hr.MaxEventsPS = h.EventsPerS
		}
		if h.Queue.Len > hr.MaxQueue {
			hr.MaxQueue = h.Queue.Len
		}
		if h.LagMs > hr.MaxLagMs {
			hr.MaxLagMs = h.LagMs
		}
		if h.WSClients > hr.WSClients {
			hr.WSClients = h.WSClients
		}
		hr.Overflows, hr.LastStatus, hr.Reconnects = h.Queue.Overflows, h.Status, h.AMI.Reconnects
		var ds []string
		for _, d := range h.Deps {
			ds = append(ds, d.Name+"="+d.Status)
		}
		hr.DepsSummary = strings.Join(ds, ", ")
	}
}

func (x *runner) loadPhases() {
	r, o := x.r, x.o
	tm := amisim.DefaultTiming
	tm.TalkMin, tm.TalkMax = o.talk/2, o.talk*3/2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x.phase("rampa")
	wg := x.sim.StartLoad(ctx, o.calls, tm)
	ramp := tm.RingMax + tm.TalkMin + 2*time.Second
	time.Sleep(ramp)
	x.phase("snapshot-carga")
	if si, err := x.d.Snapshot(); err == nil {
		r.SnapLoad = SnapMeasure{Exts: len(si.Snap.Extensions), Calls: si.Calls, Raw: si.Raw, Gz: si.Gz, Fetch: si.Dur, GzEncoded: si.GzEncoded}
	} else {
		r.Notes = append(r.Notes, "snapshot sob carga falhou: "+err.Error())
	}
	x.regime()

	// rajada
	x.phase("rajada")
	x.jitter()
	t0 := time.Now()
	nums := x.sim.Burst(o.burst)
	r.Burst.N, r.Burst.Emit = len(nums), time.Since(t0)
	r.Burst.AllSeen = waitUntil(60*time.Second, func() bool {
		for _, n := range nums {
			if v, _ := x.ws.View(n); v.Registration != "unregistered" {
				return false
			}
		}
		return true
	})
	r.Burst.Converge = time.Since(t0)
	for _, n := range nums {
		if v, _ := x.ws.View(n); v.Registration != "unregistered" {
			r.Burst.Stuck++
		}
	}
	time.Sleep(2 * time.Second)
	x.phase("restauracao")
	x.jitter()
	t1 := time.Now()
	x.sim.Restore(nums)
	r.Burst.RestoreAll = waitUntil(60*time.Second, func() bool {
		for _, n := range nums {
			if v, _ := x.ws.View(n); v.Registration != "registered" {
				return false
			}
		}
		return true
	})
	r.Burst.RestoreAfter = time.Since(t1)
	time.Sleep(2 * time.Second)

	x.dropPhase()

	x.phase("pos-queda")
	time.Sleep(5 * time.Second)
	cancel()
	wg.Wait()
	x.phase("encerrando")
	if !waitUntil(30*time.Second, x.sim.Idle) {
		r.Notes = append(r.Notes, "simulador ainda tinha canais vivos 30 s depois de parar a carga")
	}
}

func (x *runner) dropPhase() {
	r, o := x.r, x.o
	dr := &r.Drop
	dr.Ran = true
	x.phase("queda")
	epoch0, _, _, _ := x.ws.Info()
	rel0, cl0, _, rs0 := x.ws.Counters()
	dr.EpochBefore = epoch0
	x.trk.Reset()
	tDrop := time.Now()
	if err := x.sim.Down(o.dropFor); err != nil {
		dr.Err = err.Error()
		return
	}
	dr.MinUnknownWhileDown = 1 << 30
	var lastSnap, firstDown time.Time
	sawDown, recovered, wasConsistent := false, false, false
	deadline := tDrop.Add(o.dropFor + 60*time.Second)
	for time.Now().Before(deadline) && !recovered {
		h, err := x.d.Health()
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if dr.EpochVisibleAfter == 0 && h.Epoch != "" && h.Epoch != epoch0 {
			dr.EpochVisibleAfter = time.Since(tDrop)
		}
		switch {
		case !h.AMI.Connected:
			if !sawDown {
				sawDown, dr.DetectDown, firstDown = true, time.Since(tDrop), time.Now()
			}
			dr.DownSamples++
			if time.Since(lastSnap) > 200*time.Millisecond {
				lastSnap = time.Now()
				x.dropSample(dr, firstDown, &wasConsistent)
			}
		case sawDown:
			recovered = true
			dr.RecoverAfter = time.Since(tDrop) - o.dropFor
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !sawDown {
		dr.Err = "o daemon nunca reportou ami.connected=false"
		return
	}
	if !recovered {
		dr.Err = "o daemon nao reconectou ao AMI em 60 s depois da volta do simulador"
		return
	}
	// o cliente WS tem de ter recarregado e estar no epoch novo
	dr.WSConvergedAfter = waitUntil(20*time.Second, func() bool {
		h, err := x.d.Health()
		if err != nil {
			return false
		}
		e, _, ami, _ := x.ws.Info()
		return e == h.Epoch && ami
	})
	time.Sleep(3500 * time.Millisecond)
	x.trk.Reset()
	if si, err := x.d.Snapshot(); err == nil {
		dr.EpochUp = si.Snap.Epoch
		dr.ExtsAfter = len(si.Snap.Extensions)
		for _, v := range si.Snap.Extensions {
			if v.Registration == "registered" {
				dr.RegisteredAfter++
			}
		}
	}
	rel1, cl1, _, rs1 := x.ws.Counters()
	dr.Reloads, dr.CloseCodes, dr.Resyncs = rel1[len(rel0):], cl1[len(cl0):], rs1-rs0
}

// dropSample le um snapshot com o AMI fora e classifica: consistente = ami_connected=false,
// todos 'unknown', nenhuma chamada. Antes do primeiro consistente e transitorio (estado antigo);
// depois dele, qualquer inconsistencia e regressao.
func (x *runner) dropSample(dr *DropRes, firstDown time.Time, wasConsistent *bool) {
	si, err := x.d.Snapshot()
	if err != nil {
		return
	}
	if si.Snap.AMIConnected {
		dr.FlagStale++
		return
	}
	if dr.SnapshotsWhileDown == 0 {
		dr.FlagFlipAfter = time.Since(firstDown)
	}
	smp := DropSample{At: time.Since(firstDown), Epoch: si.Snap.Epoch, Calls: si.Calls}
	for _, v := range si.Snap.Extensions {
		switch v.Registration {
		case "unregistered":
			smp.Unreg++
		case "unknown":
			smp.Unknown++
		case "registered":
			smp.Registered++
		}
	}
	smp.Consistent = smp.Unreg == 0 && smp.Registered == 0 && smp.Unknown == len(si.Snap.Extensions) && smp.Calls == 0
	dr.Samples = append(dr.Samples, smp)
	dr.SnapshotsWhileDown++
	dr.EpochDown = si.Snap.Epoch
	switch {
	case smp.Consistent && !*wasConsistent:
		*wasConsistent, dr.ConsistentAfter = true, smp.At
	case !smp.Consistent && *wasConsistent:
		dr.Regressed = true
	case !smp.Consistent:
		dr.StaleSnapshots++
	}
	if *wasConsistent {
		if smp.Unreg > dr.MaxUnregWhileDown {
			dr.MaxUnregWhileDown = smp.Unreg
		}
		if smp.Unknown < dr.MinUnknownWhileDown {
			dr.MinUnknownWhileDown = smp.Unknown
		}
		if smp.Calls > dr.MaxCallsWhileDown {
			dr.MaxCallsWhileDown = smp.Calls
		}
	}
}

func (x *runner) finish() {
	r := x.r
	x.phase("final")
	// espera o WS assentar
	var h *Health
	waitUntil(30*time.Second, func() bool {
		var err error
		if h, err = x.d.Health(); err != nil {
			return false
		}
		_, v, _, last := x.ws.Info()
		return v == h.Version && time.Since(last) > 1500*time.Millisecond
	})
	truth := x.sim.Truth()
	fin := &r.Final
	fin.Compared = len(truth)
	si, err := x.d.Snapshot()
	if err != nil {
		r.Err = "snapshot final: " + err.Error()
		return
	}
	snap := map[string]View{}
	for _, v := range si.Snap.Extensions {
		snap[v.Number] = v
		fin.CallsAtEnd += len(v.Calls)
		if v.Name != "" {
			r.NamesFinal++
		}
		if v.UserAgent != "" {
			r.UAFinal++
		}
	}
	wsv := x.ws.Snapshot()
	for _, t := range truth {
		if ds := compareView(snap[t.Number], t, r.Mode); len(ds) > 0 {
			fin.SnapDiffExts++
			if len(fin.SnapDiffs) < 10 {
				fin.SnapDiffs = append(fin.SnapDiffs, ds...)
			}
		}
		if ds := compareView(wsv[t.Number], t, r.Mode); len(ds) > 0 {
			fin.WSDiffExts++
			if len(fin.WSDiffs) < 10 {
				fin.WSDiffs = append(fin.WSDiffs, ds...)
			}
		}
	}
	fin.Pending = x.trk.PendingCount()
	fin.Unexpected, fin.UnexpSamples = x.trk.Unexpected()
	fin.NoPeerOn, fin.NoPeer, fin.NoPeerSamples = x.trk.NoPeer()
	fin.Graced = x.trk.Graced()
	fin.Tainted = x.trk.Tainted()
	fin.Torn = x.trk.Torn()
	_, _, fin.Gaps, _ = x.ws.Counters()
	x.phase("fim")

	// latencias e deltas por fase
	for _, ph := range []string{"isolada", "carga", "carga-pos", "rajada", "restauracao", "queda"} {
		if l := x.trk.Latencies(ph); len(l) > 0 {
			r.Lat[ph] = statsOf(l)
		}
	}
	r.LatAll = statsOf(x.trk.AllLatencies("isolada", "carga", "carga-pos", "rajada", "restauracao"))
	byPhase := map[string][]deltaRec{}
	for _, d := range x.ws.Deltas() {
		byPhase[d.phase] = append(byPhase[d.phase], d)
	}
	for ph, ds := range byPhase {
		var sizes []int
		var tb, te float64
		for _, d := range ds {
			sizes = append(sizes, d.bytes)
			tb += float64(d.bytes)
			te += float64(d.exts)
		}
		sort.Ints(sizes)
		st := DeltaStat{N: len(ds), AvgBytes: tb / float64(len(ds)), AvgExts: te / float64(len(ds)),
			P50: sizes[(len(sizes)-1)/2], P99: sizes[int(float64(len(sizes)-1)*0.99)], Max: sizes[len(sizes)-1]}
		if te > 0 {
			st.BytesPerExt = tb / te
		}
		if dur := r.Phases[ph]; dur > 0 {
			st.BytesPerS = tb / dur.Seconds()
		}
		r.Deltas[ph] = st
	}

	// log de actions do simulador pelo endpoint HTTP exclusivo
	x.fetchActions()
}

// fetchActions le o log de actions do simulador pelo endpoint HTTP exclusivo dele.
func (x *runner) fetchActions() {
	resp, err := http.Get("http://" + x.simHTTP + "/actions")
	if err != nil {
		x.r.Err = "GET /actions do simulador: " + err.Error()
		return
	}
	defer resp.Body.Close()
	var a struct {
		Counts     map[string]int `json:"counts"`
		Violations []struct {
			Action string `json:"action"`
		} `json:"violations"`
		AllowlistOnly bool `json:"allowlist_only"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		x.r.Err = "GET /actions do simulador: " + err.Error()
		return
	}
	x.r.Actions.Counts, x.r.Actions.OnlyAllow = a.Counts, a.AllowlistOnly
	for _, v := range a.Violations {
		x.r.Actions.Violations = append(x.r.Actions.Violations, v.Action)
	}
}

func deref(p *string) string {
	if p == nil {
		return "null"
	}
	return *p
}

// stepIndex acha a fase pelo nome (a primeira que existir); -1 se nenhuma.
func stepIndex(sc *amisim.Script, names ...string) int {
	for _, n := range names {
		for i, st := range sc.Steps {
			if st.Name == n {
				return i
			}
		}
	}
	return -1
}

// restoreRounds deixa todos os cenarios parados no mesmo ponto, derruba a conexao AMI
// (o daemon reconecta e refaz o snapshot com as chamadas em andamento) e confere o estado
// restaurado com a verdade. Tres pontos: tocando, atendida e depois da transferencia.
func (x *runner) restoreRounds() {
	r := x.r
	x.phase("restauracao-snapshot")
	var regs []string
	for _, t := range x.sim.Truth() {
		if t.Registration == "registered" && len(t.Calls) == 0 {
			regs = append(regs, t.Number)
		}
	}
	type live struct {
		kind string
		sc   *amisim.Script
		done int
		nums []string
	}
	var ls []*live
	pos := 0
	for i, kind := range amisim.Kinds {
		need := amisim.ExtsNeeded(kind)
		exts := regs[pos : pos+need]
		pos += need
		sc, err := x.sim.NewScript(kind, amisim.Parts{Exts: exts, Trunk: fmt.Sprintf("trunk%d", i%4+1), XNum: fmt.Sprintf("119%08d", 22345600+i), XName: "ACME LTDA", DID: "30001000"}, nil)
		if err != nil {
			r.Restore = append(r.Restore, RestoreRow{kind, "montar", false, err.Error()})
			continue
		}
		ls = append(ls, &live{kind: kind, sc: sc, done: -1, nums: exts})
	}
	last := func(l *live) []string {
		switch {
		case strings.HasPrefix(l.kind, "blind"):
			return []string{"target-answer"}
		case strings.HasPrefix(l.kind, "attended-merge"):
			return []string{"complete-merge"}
		case strings.HasPrefix(l.kind, "attended-link"):
			return []string{"complete-link"}
		case l.kind == "internal-hold":
			return []string{"hold"}
		case strings.HasPrefix(l.kind, "local"):
			return []string{"optimize"}
		case l.kind == "internal-noanswer":
			return []string{"ring"}
		}
		return []string{"answer"}
	}
	rounds := []struct {
		name string
		pick func(l *live) []string
	}{
		{"tocando", func(l *live) []string { return []string{"ring"} }},
		{"atendida", func(l *live) []string {
			if l.kind == "internal-noanswer" {
				return []string{"ring"}
			}
			return []string{"answer"}
		}},
		{"apos transferencia/hold/otimizacao", last},
	}
	var all []string
	for _, l := range ls {
		all = append(all, l.nums...)
	}
	for _, rd := range rounds {
		for _, l := range ls {
			if idx := stepIndex(l.sc, rd.pick(l)...); idx > l.done {
				for l.done < idx {
					l.done++
					l.sc.Do(l.done)
				}
			}
		}
		if ok, _, why := x.converge(all, 10*time.Second); !ok {
			r.Notes = append(r.Notes, "antes da reconexao (rodada "+rd.name+") o estado ao vivo ja divergia: "+why)
		}
		h0, _ := x.d.Health()
		e0, _, _, _ := x.ws.Info()
		x.sim.KickAll()
		back := waitUntil(60*time.Second, func() bool {
			h, err := x.d.Health()
			if err != nil || !h.AMI.Connected || (h0 != nil && h.AMI.Reconnects <= h0.AMI.Reconnects) {
				return false
			}
			e, _, ami, _ := x.ws.Info()
			return ami && e != e0 && e == h.Epoch
		})
		time.Sleep(500 * time.Millisecond)
		for _, l := range ls {
			ok, detail := back, "daemon nao reconectou/ressincronizou em 60 s"
			if back {
				tr := x.sim.TruthOf(l.nums...)
				ok, detail = true, ""
				for _, n := range l.nums {
					want := tr[n]
					for i := range want.Calls { // o CoreShowChannels nao traz Hold: limite do protocolo
						if want.Calls[i].State == "hold" {
							want.Calls[i].State = "talking"
						}
					}
					got, _ := x.ws.View(n)
					if ds := compareView(got, want, r.Mode); len(ds) > 0 {
						ok = false
						detail += ds[0].Ext + ": " + ds[0].What + "; "
					}
				}
			}
			r.Restore = append(r.Restore, RestoreRow{l.kind, rd.name, ok, detail})
		}
	}
	for _, l := range ls {
		l.sc.Abort()
	}
	x.converge(all, 10*time.Second)
}

func cpuPct(ss []MemSample, phase string) float64 {
	var a, b *MemSample
	for i := range ss {
		if ss[i].Phase == phase {
			if a == nil {
				a = &ss[i]
			}
			b = &ss[i]
		}
	}
	if a == nil || b == a || !b.T.After(a.T) {
		return 0
	}
	return float64(b.CPU-a.CPU) / float64(b.T.Sub(a.T)) * 100
}

type FillSample struct {
	At     time.Duration // desde o daemon pronto
	Filled int
}

type FillRes struct {
	Total, StartFilled, EndFilled, EndCorrect int
	Complete                                  bool
	DoneAfterBoot                             time.Duration // do daemon pronto ate o ultimo nome
	DoneAfterRegime                           time.Duration // do inicio do regime sob carga ate o ultimo nome
	RegimeDur                                 time.Duration
	rateCfg                                   int
	Rate                                      float64 // nomes por segundo (medido, entre a primeira e a ultima amostra)
	Samples                                   []FillSample
}

// regimeEvery: intervalo do laco do regime; variavel so para o teste.
var regimeEvery = 10 * time.Second

// regime: carga sob preenchimento de nomes ("carga") ate todos os nomes chegarem (ou fillTimeout),
// depois mais o.duration em regime com nomes completos ("carga-pos").
func (x *runner) regime() {
	r, o := x.r, x.o
	x.phase("carga")
	t0 := time.Now()
	f := &r.Fill
	f.rateCfg = o.fillRate
	post := time.Time{}
	var heap []HeapSample
	var heapErr string
	var heapNext, warmEnd time.Time
	var inicioDone bool
	heapAt := func(now time.Time) { // 1 amostra de heap pos-GC; Warmed depois do aquecimento
		in, al, err := x.d.HeapStats()
		if err != nil {
			heapErr = err.Error()
			return
		}
		heap = append(heap, HeapSample{T: now, Inuse: in, Alloc: al, Warmed: !now.Before(warmEnd)})
	}
	for {
		// Fim do regime checado ANTES do NameProgress: ele faz GET /api/snapshot e deixa o cache
		// de snapshot (~3 MiB) residente; a amostra final logo depois dele entrava como degrau.
		if f.Complete && time.Since(post) >= o.warmup+o.duration {
			break
		}
		if o.heap && f.Complete {
			now := time.Now()
			if heapNext.IsZero() {
				heapNext, warmEnd = now, now.Add(o.warmup)
			}
			if !now.Before(heapNext) {
				heapAt(now)
				heapNext = heapNext.Add(heapEvery)
			}
			if !inicioDone && !now.Before(warmEnd) {
				inicioDone = true
				if err := x.d.HeapProfile(filepath.Join(o.outDir, "inicio-"+r.Mode+".pb.gz")); err != nil {
					heapErr = err.Error()
				}
			}
		}
		filled, correct, total, err := x.d.NameProgress()
		if err == nil {
			f.Total = total
			f.Samples = append(f.Samples, FillSample{time.Since(x.bootAt), filled})
			if len(f.Samples) == 1 {
				f.StartFilled = filled
			}
			f.EndFilled, f.EndCorrect = filled, correct
			if !f.Complete && total > 0 && correct == total {
				f.Complete = true
				f.DoneAfterBoot, f.DoneAfterRegime = time.Since(x.bootAt), time.Since(t0)
				x.phase("carga-pos")
				post = time.Now()
				x.trk.CountNoPeerFrom(post.Add(o.warmup))
			}
		}
		if !f.Complete && time.Since(t0) >= o.fillTimeout {
			x.logf("[%s] preenchimento de nomes nao terminou em %s", r.Mode, o.fillTimeout)
			break
		}
		time.Sleep(regimeEvery)
	}
	f.RegimeDur = time.Since(t0)
	x.trk.StopNoPeer(time.Now())
	if o.heap {
		heapAt(time.Now())
		if err := x.d.HeapProfile(filepath.Join(o.outDir, "fim-"+r.Mode+".pb.gz")); err != nil {
			heapErr = err.Error()
		}
		if !f.Complete {
			heapErr = firstStr(heapErr, "preenchimento de nomes nao terminou: sem aquecimento nem regime medido")
		}
		r.Heap = evalHeap(heap, heapErr)
		if err := writeHeapCSV(o.outDir, r.Mode, heap); err != nil {
			r.Heap.Err = firstStr(r.Heap.Err, err.Error())
		}
	}
	if n := len(f.Samples); n >= 2 {
		end := n - 1
		for i, sm := range f.Samples { // ate a primeira amostra com todos os nomes
			if sm.Filled >= f.Total {
				end = i
				break
			}
		}
		a, b := f.Samples[0], f.Samples[end]
		if b.At > a.At {
			f.Rate = float64(b.Filled-a.Filled) / (b.At - a.At).Seconds()
		}
	}
}
