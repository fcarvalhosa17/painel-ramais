package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"painel-ramais/internal/amisim"
)

func TestPercentile(t *testing.T) {
	var d []time.Duration
	for i := 1; i <= 100; i++ {
		d = append(d, time.Duration(i)*time.Millisecond)
	}
	s := statsOf(d)
	if s.P50 != 50*time.Millisecond || s.P99 != 99*time.Millisecond || s.Max != 100*time.Millisecond || s.N != 100 {
		t.Fatalf("%+v", s)
	}
	if z := statsOf(nil); z.N != 0 || z.P99 != 0 {
		t.Fatalf("vazio: %+v", z)
	}
}

func mkView(n, reg, dev string, calls ...amisim.CallView) View {
	return View{Number: n, Registration: reg, DeviceState: dev, Calls: calls}
}

func stamp(at time.Time) *amisim.Stamp { return &amisim.Stamp{At: at.UnixNano()} }

func TestTrackerLatenciaECoalescencia(t *testing.T) {
	trk := NewTracker()
	trk.SetPhase("p")
	t0 := time.Now()
	a := mkView("1", "registered", "not_inuse")
	b := mkView("1", "registered", "inuse", amisim.CallView{ID: "9.1", Direction: "internal", State: "talking", Peer: amisim.PeerView{Kind: "extension", Number: "2"}})
	c := mkView("1", "unregistered", "unavailable")
	trk.SetBaseline(a)
	trk.Hook(b, stamp(t0))
	trk.Hook(c, stamp(t0.Add(100*time.Millisecond)))
	// o daemon coalesceu b e c: entrega c (estado final) 400 ms depois do primeiro
	trk.Observe(c, t0.Add(400*time.Millisecond), true)
	l := trk.Latencies("p")
	if len(l) != 2 || l[0] != 400*time.Millisecond || l[1] != 300*time.Millisecond {
		t.Fatalf("latencias: %v", l)
	}
	if trk.PendingCount() != 0 {
		t.Fatal("pendencias sobraram")
	}
	// reenvio do mesmo estado nao e inesperado
	trk.Observe(c, t0.Add(time.Second), true)
	if n, _ := trk.Unexpected(); n != 0 {
		t.Fatalf("reenvio contado como inesperado: %d", n)
	}
}

func TestTrackerDetectaEstadoErrado(t *testing.T) {
	trk := NewTracker()
	good := mkView("1", "registered", "inuse", amisim.CallView{ID: "9.1", Direction: "internal", State: "talking", Peer: amisim.PeerView{Kind: "extension", Number: "2"}})
	trk.SetBaseline(mkView("1", "registered", "not_inuse"))
	trk.Hook(good, stamp(time.Now()))
	wrong := good
	wrong.Calls = []amisim.CallView{{ID: "9.1", Direction: "internal", State: "talking", Peer: amisim.PeerView{Kind: "extension", Number: "3"}}}
	trk.Observe(wrong, time.Now(), true)
	n, s := trk.Unexpected()
	if n != 1 || len(s) != 1 {
		t.Fatalf("outro lado errado deveria ser inesperado: %d %v", n, s)
	}
	if len(trk.Latencies("")) != 0 || trk.PendingCount() != 1 {
		t.Fatal("estado errado nao pode resolver a pendencia")
	}
}

func TestTrackerPeerAmbiguoNaoPesa(t *testing.T) {
	trk := NewTracker()
	truth := mkView("1", "registered", "inuse", amisim.CallView{ID: "9.1", Direction: "internal", State: "ringing", Ambiguous: true, Peer: amisim.PeerView{Kind: "extension", Number: "2"}})
	trk.Hook(truth, stamp(time.Now()))
	got := mkView("1", "registered", "inuse", amisim.CallView{ID: "9.1", Direction: "internal", State: "ringing", Peer: amisim.PeerView{Kind: "unknown", Number: "600"}})
	trk.Observe(got, time.Now(), true)
	if n, _ := trk.Unexpected(); n != 0 || trk.PendingCount() != 0 {
		t.Fatalf("peer ambiguo deveria casar: unexpected=%d pend=%d", n, trk.PendingCount())
	}
}

func TestCompareView(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	want := View{Number: "1", Registration: "registered", IP: "192.0.2.1", DeviceState: "inuse",
		Calls: []amisim.CallView{{ID: "9.1", Direction: "inbound", State: "talking", StartedAt: now, AnsweredAt: now, Peer: amisim.PeerView{Kind: "external", Number: "119"}}}}
	got := want
	got.Technology, got.Groups = "pjsip", []string{"Sem grupo"}
	if d := compareView(got, want, "pjsip"); len(d) != 0 {
		t.Fatalf("iguais divergiram: %v", d)
	}
	got.Calls = []amisim.CallView{{ID: "9.1", Direction: "outbound", State: "talking", StartedAt: now, AnsweredAt: now, Peer: amisim.PeerView{Kind: "external", Number: "119"}}}
	if d := compareView(got, want, "pjsip"); len(d) != 1 || !strings.Contains(d[0].What, "outbound") {
		t.Fatalf("direcao errada nao detectada: %v", d)
	}
	got.Calls = nil
	if d := compareView(got, want, "pjsip"); len(d) != 1 {
		t.Fatalf("chamada ausente nao detectada: %v", d)
	}
}

// servidor WS minimo (so no teste) para validar o cliente: handshake, frame mascarado,
// ping/pong, mensagem fragmentada e fechamento com codigo.
func TestWSClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var gotHello atomic.Value
	go func() {
		nc, _ := ln.Accept()
		defer nc.Close()
		br := bufio.NewReader(nc)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + wsGUID))
		nc.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"))
		if req.Header.Get("Cookie") != "painel_session=abc" {
			return
		}
		// le o hello mascarado
		var h [2]byte
		io.ReadFull(br, h[:])
		n := int(h[1] & 0x7f)
		var mask [4]byte
		io.ReadFull(br, mask[:])
		p := make([]byte, n)
		io.ReadFull(br, p)
		for i := range p {
			p[i] ^= mask[i%4]
		}
		gotHello.Store(string(p))
		nc.Write([]byte{0x89, 0})                // ping
		nc.Write([]byte{0x01, 3, 'a', 'b', 'c'}) // texto, nao final
		nc.Write([]byte{0x80, 2, 'd', 'e'})      // continuacao final
		big := strings.Repeat("x", 70000)        // frame com tamanho de 8 bytes
		hdr := []byte{0x81, 127, 0, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint64(hdr[2:], uint64(len(big)))
		nc.Write(append(hdr, big...))
		nc.Write([]byte{0x88, 2, 0x0f, 0xa0}) // close 4000
		time.Sleep(200 * time.Millisecond)
	}()
	w, err := wsDial(ln.Addr().String(), "/ws", "painel_session=abc")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.WriteText([]byte(`{"type":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	w.SetReadDeadline(time.Now().Add(3 * time.Second))
	m, err := w.ReadMessage()
	if err != nil || string(m) != "abcde" {
		t.Fatalf("fragmentada: %q %v", m, err)
	}
	m, err = w.ReadMessage()
	if err != nil || len(m) != 70000 {
		t.Fatalf("grande: %d %v", len(m), err)
	}
	_, err = w.ReadMessage()
	ce, ok := err.(*closeError)
	if !ok || ce.Code != 4000 {
		t.Fatalf("fechamento: %v", err)
	}
	if gotHello.Load() != `{"type":"hello"}` {
		t.Fatalf("hello recebido: %v", gotHello.Load())
	}
}

func TestWSRecusaUpgrade(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		nc, _ := ln.Accept()
		defer nc.Close()
		http.ReadRequest(bufio.NewReader(nc))
		nc.Write([]byte("HTTP/1.1 401 Unauthorized\r\nContent-Length: 2\r\n\r\n{}"))
	}()
	if _, err := wsDial(ln.Addr().String(), "/ws", ""); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("esperava erro 401, veio %v", err)
	}
}

func TestProcStatsDoProprioProcesso(t *testing.T) {
	rss, _, err := procStats(osGetpid())
	if err != nil || rss < 1<<20 {
		t.Fatalf("rss=%d err=%v", rss, err)
	}
}

func osGetpid() int { return os.Getpid() }

func TestTornAceitaDispositivoDeOutroInstante(t *testing.T) {
	call := amisim.CallView{ID: "9.1", Direction: "internal", State: "talking", Peer: amisim.PeerView{Kind: "extension", Number: "2"}}
	idle := mkView("1", "registered", "not_inuse")
	busy := mkView("1", "registered", "inuse", call)
	// chamada terminou, mas o DeviceStateChange ainda nao foi processado
	if !torn(mkView("1", "registered", "inuse"), []View{busy, idle}) {
		t.Fatal("dispositivo antigo com chamadas novas deveria ser aceito")
	}
	// peer errado nao e rasgo de instante: continua inesperado
	wrong := call
	wrong.Peer.Number = "3"
	if torn(mkView("1", "registered", "inuse", wrong), []View{busy, idle}) {
		t.Fatal("outro lado errado nao pode ser aceito")
	}
	// dispositivo que nunca existiu
	if torn(mkView("1", "registered", "ringing"), []View{busy, idle}) {
		t.Fatal("dispositivo inexistente nao pode ser aceito")
	}
}

func TestLinregEVeredito(t *testing.T) {
	// y = 2x + 1 exato: inclinacao 2, R2 1
	sl, r2 := linreg([]float64{0, 1, 2, 3}, []float64{1, 3, 5, 7})
	if sl < 1.999 || sl > 2.001 || r2 < 0.999 {
		t.Fatalf("slope=%v r2=%v", sl, r2)
	}
	if sl, r2 = linreg([]float64{0, 1, 2}, []float64{5, 5, 5}); sl != 0 || r2 != 1 {
		t.Fatalf("constante: slope=%v r2=%v", sl, r2)
	}
	mk := func(n int, mibPerHour float64, warmed bool) []HeapSample {
		t0 := time.Now()
		var ss []HeapSample
		for i := 0; i < n; i++ {
			b := uint64((100 + mibPerHour*float64(i)/60) * mib)
			ss = append(ss, HeapSample{T: t0.Add(time.Duration(i) * time.Minute), Inuse: b, Alloc: b, Warmed: warmed})
		}
		return ss
	}
	if h := evalHeap(mk(120, 0.2, true), ""); h.Verdict != "APROVA" || h.N != 120 || h.InuseSlope < 0.19 || h.InuseSlope > 0.21 {
		t.Fatalf("0,2 MiB/h deveria APROVAR: %+v", h)
	}
	if h := evalHeap(mk(120, 5, true), ""); h.Verdict != "REPROVA" {
		t.Fatalf("5 MiB/h deveria REPROVAR: %+v", h)
	}
	if h := evalHeap(mk(5, 0, true), ""); h.Verdict != "REPROVA" {
		t.Fatalf("poucas amostras deveria REPROVAR: %+v", h)
	}
	// amostras de aquecimento nao entram
	if h := evalHeap(mk(120, 50, false), ""); h.N != 0 || h.Verdict != "REPROVA" {
		t.Fatalf("aquecimento fora da regressao: %+v", h)
	}
}

func TestTrackerContaEmConversaSemOutroLado(t *testing.T) {
	trk := NewTracker()
	trk.SetPhase("carga-pos")
	t0 := time.Now()
	semLado := mkView("1", "registered", "inuse", amisim.CallView{ID: "9.1", Direction: "internal", State: "talking", Peer: amisim.PeerView{Kind: "unknown"}})
	comLado := mkView("2", "registered", "inuse", amisim.CallView{ID: "9.2", Direction: "inbound", State: "talking", Peer: amisim.PeerView{Kind: "external", Number: "11999"}})
	tocando := mkView("3", "registered", "inuse", amisim.CallView{ID: "9.3", Direction: "internal", State: "ringing", Peer: amisim.PeerView{Kind: "unknown"}})
	if on, n, _ := trk.NoPeer(); on || n != 0 {
		t.Fatalf("desligado por padrao: %v %d", on, n)
	}
	trk.CountNoPeerFrom(t0.Add(time.Second))
	trk.Observe(semLado, t0, true) // durante o aquecimento: nao conta
	trk.Observe(comLado, t0.Add(2*time.Second), true)
	trk.Observe(tocando, t0.Add(2*time.Second), true)
	if _, n, _ := trk.NoPeer(); n != 0 {
		t.Fatalf("so talking sem outro lado conta: %d", n)
	}
	trk.Observe(semLado, t0.Add(2*time.Second), false) // snapshot (measure=false): nao conta
	trk.Observe(semLado, t0.Add(3*time.Second), true)
	trk.Observe(semLado, t0.Add(4*time.Second), true) // mesma chamada: uma vez so
	on, n, s := trk.NoPeer()
	if !on || n != 1 || len(s) != 1 {
		t.Fatalf("queria 1 ocorrencia: on=%v n=%d %v", on, n, s)
	}
}

func TestTrackerNoPeerIgnoraTaintedEForaDoRegime(t *testing.T) {
	trk := NewTracker()
	t0 := time.Now()
	trk.CountNoPeerFrom(t0)
	mk := func(num, id string) View {
		return mkView(num, "registered", "inuse", amisim.CallView{ID: id, Direction: "internal", State: "talking", Peer: amisim.PeerView{Kind: "unknown"}})
	}
	trk.tainted["1"] = true
	trk.Observe(mk("1", "9.1"), t0.Add(time.Second), true)
	if _, n, _ := trk.NoPeer(); n != 0 {
		t.Fatalf("tainted nao conta: %d", n)
	}
	trk.StopNoPeer(t0.Add(2 * time.Second))
	trk.Observe(mk("2", "9.2"), t0.Add(3*time.Second), true)
	if _, n, _ := trk.NoPeer(); n != 0 {
		t.Fatalf("depois do fim do regime nao conta: %d", n)
	}
	trk.Observe(mk("3", "9.3"), t0.Add(time.Second), true)
	if _, n, _ := trk.NoPeer(); n != 1 {
		t.Fatalf("dentro do regime conta: %d", n)
	}
}
