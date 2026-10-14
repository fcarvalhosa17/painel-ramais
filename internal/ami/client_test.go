package ami

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

// fakeAMI é um Asterisk mínimo: banner, lê actions, responde via handler.
type fakeAMI struct {
	ln   net.Listener
	mu   sync.Mutex
	conn net.Conn
	// handler devolve o texto cru a escrever para a action recebida.
	handler func(m Message) string
}

func newFake(t *testing.T, h func(m Message) string) *fakeAMI {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("sem loopback TCP: %v", err)
	}
	f := &fakeAMI{ln: ln, handler: h}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conn = c
		f.mu.Unlock()
		io.WriteString(c, "Asterisk Call Manager/5.0.1\r\n")
		p := NewParser(c)
		for {
			m, err := p.Next()
			if err != nil {
				return
			}
			if out := f.handler(m); out != "" {
				io.WriteString(c, out)
			}
		}
	}()
	return f
}

func (f *fakeAMI) send(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	io.WriteString(f.conn, s)
}

func okReply(m Message, extra string) string {
	return fmt.Sprintf("Response: Success\r\nActionID: %s\r\n%s\r\n", m.ActionID(), extra)
}

func loginHandler(m Message) string {
	switch m.Get("Action") {
	case "Login":
		if m.Get("Secret") == "ok" {
			return okReply(m, "Message: Authentication accepted\r\n")
		}
		return fmt.Sprintf("Response: Error\r\nActionID: %s\r\nMessage: Authentication failed\r\n\r\n", m.ActionID())
	case "Ping":
		return okReply(m, "Ping: Pong\r\n")
	}
	return okReply(m, "")
}

func connectFake(t *testing.T, f *fakeAMI, cfg Config) (*Client, error) {
	cfg.Address = f.ln.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return Connect(ctx, cfg)
}

func TestParserBlocoParcialUmByte(t *testing.T) {
	raw := "Event: PeerStatus\r\nPeer: SIP/2001\r\nPeerStatus: Registered\r\n\r\n" +
		"\r\n\r\nResponse: Success\r\nActionID: p1\r\nMessage: a: b: c\r\n\r\n"
	p := NewParser(iotest.OneByteReader(strings.NewReader(raw)))
	m1, err := p.Next()
	if err != nil || m1.Event() != "PeerStatus" || m1.Get("peer") != "SIP/2001" {
		t.Fatalf("m1=%v err=%v", m1.Fields, err)
	}
	m2, err := p.Next()
	if err != nil || !m2.IsResponse() || m2.Get("Message") != "a: b: c" {
		t.Fatalf("m2=%v err=%v", m2.Fields, err)
	}
	if _, err := p.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("esperava EOF, veio %v", err)
	}
}

func TestParserBlocoCortadoNoEOF(t *testing.T) {
	p := NewParser(strings.NewReader("Event: Hangup\r\nUniqueid: 1\r\n"))
	if _, err := p.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("esperava ErrUnexpectedEOF, veio %v", err)
	}
}

func TestParserAceitaLFPuro(t *testing.T) {
	ms, err := ParseAll(strings.NewReader("Event: A\nX: 1\n\nEvent: B\n\n"))
	if err != nil || len(ms) != 2 || ms[0].Get("x") != "1" {
		t.Fatalf("ms=%v err=%v", ms, err)
	}
}

func TestParserLinhaGrande(t *testing.T) {
	p := NewParser(strings.NewReader("Event: X\r\nBig: " + strings.Repeat("a", maxLine+10) + "\r\n\r\n"))
	if _, err := p.Next(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("esperava ErrLineTooLong, veio %v", err)
	}
}

func TestDoLoginListaEErro(t *testing.T) {
	f := newFake(t, func(m Message) string {
		switch m.Get("Action") {
		case "SIPpeers":
			id := m.ActionID()
			return okReply(m, "EventList: start\r\nMessage: Peer status list will follow\r\n") +
				"Event: PeerEntry\r\nActionID: " + id + "\r\nObjectName: 2001\r\n\r\n" +
				"Event: PeerEntry\r\nActionID: " + id + "\r\nObjectName: 2002\r\n\r\n" +
				"Event: PeerlistComplete\r\nEventList: Complete\r\nActionID: " + id + "\r\nListItems: 2\r\n\r\n"
		case "ModuleCheck":
			return "Response: Error\r\nActionID: " + m.ActionID() + "\r\nMessage: Module not loaded\r\n\r\n"
		}
		return loginHandler(m)
	})
	c, err := connectFake(t, f, Config{Username: "u", Secret: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := c.Do(ctx, "SIPpeers", nil)
	if err != nil || len(r.Items) != 2 || r.Items[1].Get("ObjectName") != "2002" {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	loaded, err := c.ModuleLoaded(ctx, "chan_pjsip.so")
	if err != nil || loaded {
		t.Fatalf("loaded=%v err=%v", loaded, err)
	}
}

func TestLoginRecusado(t *testing.T) {
	f := newFake(t, loginHandler)
	_, err := connectFake(t, f, Config{Username: "u", Secret: "errado"})
	if !errors.Is(err, ErrLoginFailed) {
		t.Fatalf("esperava ErrLoginFailed, veio %v", err)
	}
	if strings.Contains(err.Error(), "errado") {
		t.Fatal("erro vaza o segredo")
	}
}

func TestActionForaDaListaEErro(t *testing.T) {
	f := newFake(t, loginHandler)
	c, err := connectFake(t, f, Config{Username: "u", Secret: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, a := range []string{"Command", "Originate", "Redirect", "Hangup", "Atxfer", "Setvar", "Reload",
		"ModuleLoad", "UpdateConfig", "GetConfig", "Filter", "login", "Nao Existe"} {
		if _, err := c.Do(context.Background(), a, nil); !errors.Is(err, ErrActionNotAllowed) {
			t.Errorf("%s: esperava ErrActionNotAllowed, veio %v", a, err)
		}
	}
	if _, err := c.Do(context.Background(), "Ping", map[string]string{"X": "a\r\nAction: Command"}); err == nil {
		t.Error("injeção de linha em parâmetro deveria falhar")
	}
}

func TestAllowlistExata(t *testing.T) {
	want := []string{"CoreSettings", "CoreShowChannels", "DeviceStateList", "Login", "Logoff", "ModuleCheck",
		"PJSIPShowAors", "PJSIPShowContacts", "PJSIPShowEndpoint", "PJSIPShowEndpoints", "Ping", "SIPpeers", "SIPshowpeer"}
	if got := AllowedActions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("allowlist mudou:\n got %v\nwant %v", got, want)
	}
}

// Consumidor parado: o leitor não pode bloquear. Com fila de 8 e 500 eventos,
// o Ping tem de continuar respondendo, o descarte é contado e o resync sinalizado.
func TestLeitorNaoBloqueiaComConsumidorParado(t *testing.T) {
	f := newFake(t, loginHandler)
	c, err := connectFake(t, f, Config{Username: "u", Secret: "ok", QueueSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, "Event: Hangup\r\nUniqueid: %d\r\n\r\n", i)
	}
	f.send(sb.String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.Do(ctx, "Ping", nil); err != nil { // resposta chega depois dos 500: leitor andou
		t.Fatalf("Ping travou com consumidor parado: %v", err)
	}
	if got := c.Overflows(); got != 492 {
		t.Fatalf("overflows=%d, esperava 492", got)
	}
	if c.QueueLen() != 8 || c.TakeResync() {
		t.Fatalf("fila cheia não deve liberar resync (len=%d)", c.QueueLen())
	}
	for i := 0; i < 8; i++ {
		<-c.Events()
	}
	if !c.TakeResync() || c.TakeResync() {
		t.Fatal("resync deve sair exatamente uma vez, com a fila vazia")
	}
}

func TestQuedaFechaEventsEErr(t *testing.T) {
	f := newFake(t, loginHandler)
	c, err := connectFake(t, f, Config{Username: "u", Secret: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.conn.Close()
	f.mu.Unlock()
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done não fechou")
	}
	if _, ok := <-c.Events(); ok || c.Err() == nil {
		t.Fatalf("Events deveria fechar e Err vir preenchido (err=%v)", c.Err())
	}
	if _, err := c.Do(context.Background(), "Ping", nil); err == nil {
		t.Fatal("Do em conexão morta deveria falhar")
	}
}

func TestPingSemRespostaDerrubaConexao(t *testing.T) {
	f := newFake(t, func(m Message) string {
		if m.Get("Action") == "Ping" {
			return "" // silêncio: conexão meio aberta
		}
		return loginHandler(m)
	})
	c, err := connectFake(t, f, Config{Username: "u", Secret: "ok", PingInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ping mudo deveria derrubar a conexão")
	}
}

func TestBackoff(t *testing.T) {
	b := DefaultBackoff()
	var got []time.Duration
	for i := 0; i < 8; i++ {
		got = append(got, b.Next())
	}
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("passo %d: %v", i, got)
		}
	}
	b.Reset()
	if b.Next() != time.Second {
		t.Fatal("Reset não voltou ao mínimo")
	}
}

func TestConnectRetryReconectaQuandoServidorVolta(t *testing.T) {
	f := newFake(t, loginHandler)
	addr := f.ln.Addr().String()
	attempts := 0
	cfg := Config{Address: addr, Username: "u", Secret: "ok",
		Dial: func(ctx context.Context, a string) (net.Conn, error) {
			attempts++
			if attempts < 3 {
				return nil, errors.New("recusado")
			}
			return net.Dial("tcp", a)
		}}
	b := &Backoff{Min: time.Millisecond, Max: 4 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fails := 0
	c, err := ConnectRetry(ctx, cfg, b, func(error, time.Duration) { fails++ })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if fails != 2 || attempts != 3 {
		t.Fatalf("fails=%d attempts=%d", fails, attempts)
	}
}

// Detecção ponta a ponta contra o AMI de mentira: CoreSettings e ModuleCheck.
func TestDetectTechnologyPeloAMI(t *testing.T) {
	list := func(m Message, event string, names ...string) string {
		id := m.ActionID()
		out := okReply(m, "EventList: start\r\n")
		for _, n := range names {
			out += "Event: " + event + "\r\nActionID: " + id + "\r\nObjectName: " + n + "\r\n\r\n"
		}
		return out + "Event: Complete\r\nEventList: Complete\r\nActionID: " + id + "\r\n\r\n"
	}
	loaded := map[string]bool{"chan_pjsip.so": true, "chan_sip.so": true}
	f := newFake(t, func(m Message) string {
		switch m.Get("Action") {
		case "CoreSettings":
			return okReply(m, "AsteriskVersion: 18.20.0\r\n")
		case "ModuleCheck":
			if loaded[m.Get("Module")] {
				return okReply(m, "Version: 18.20.0\r\n")
			}
			return "Response: Error\r\nActionID: " + m.ActionID() + "\r\nMessage: Module not loaded\r\n\r\n"
		case "PJSIPShowEndpoints":
			return list(m, "EndpointList", "trunk1")
		case "SIPpeers":
			return list(m, "PeerEntry", "2001", "2002", "trunk1")
		}
		return loginHandler(m)
	})
	c, err := connectFake(t, f, Config{Username: "u", Secret: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if v, err := c.CoreVersion(ctx); err != nil || CheckVersion(v) != nil {
		t.Fatalf("v=%q err=%v", v, err)
	}
	pattern := regexp.MustCompile(`^(?:[0-9]+)$`)
	d, err := c.DetectTechnology(ctx, "auto", pattern)
	if err != nil || d.Technology != "sip" || d.Warning == "" {
		t.Fatalf("ambos carregados, sip tem 2 ramais e pjsip 0: %+v err=%v", d, err)
	}
}

// Regressao: shutdown (Close, ping, escrita) vindo de outra goroutine nao pode
// fechar Events enquanto o readLoop ainda envia nele ("send on closed channel").
func TestShutdownWhileReaderIsFlooding(t *testing.T) {
	for i := 0; i < 40; i++ {
		f := newFake(t, loginHandler)
		c, err := Connect(context.Background(), Config{Address: f.ln.Addr().String(), Username: "u", Secret: "ok", QueueSize: 2})
		if err != nil {
			t.Fatal(err)
		}
		stop := make(chan struct{})
		go func() { // o servidor nunca para de mandar eventos
			ev := "Event: Newstate\r\nUniqueid: 1\r\n\r\n"
			for {
				select {
				case <-stop:
					return
				default:
					f.mu.Lock()
					cn := f.conn
					f.mu.Unlock()
					if _, err := io.WriteString(cn, ev+ev+ev+ev); err != nil {
						return
					}
				}
			}
		}()
		time.Sleep(time.Duration(i%5) * time.Millisecond)
		var wg sync.WaitGroup
		for j := 0; j < 3; j++ {
			wg.Add(1)
			go func() { defer wg.Done(); c.shutdown(errors.New("teste")) }()
		}
		c.Close()
		wg.Wait()
		close(stop)
		select { // Events fecha depois que o leitor termina
		case <-time.After(2 * time.Second):
			t.Fatal("Events nao fechou")
		case <-func() chan struct{} {
			d := make(chan struct{})
			go func() {
				for range c.Events() {
				}
				close(d)
			}()
			return d
		}():
		}
	}
}

// Controle compensatorio da decisao 1 (write = system,reporting): action fora da lista nunca chega ao socket.
func TestRefusedActionsNeverReachTheSocket(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	f := newFake(t, func(m Message) string {
		mu.Lock()
		seen = append(seen, m.Get("Action"))
		mu.Unlock()
		return loginHandler(m)
	})
	var logged []string
	c, err := connectFake(t, f, Config{Username: "u", Secret: "ok", Logf: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	bad := []string{"Command", "Originate", "Redirect", "Hangup", "Atxfer", "Setvar", "Reload", "ModuleLoad", "ModuleUnload",
		"UpdateConfig", "GetConfig", "GetConfigJSON", "Filter", "MixMonitor", "DBPut", "DBGet", "DBDel", "PJSIPNotify", "PJSIPQualify",
		"SIPnotify", "SIPqualifypeer", "Park", "ExtensionState", "QueueAdd", "AGI", "Bridge", "BlindTransfer", "Events", "Monitor",
		"CoreShowChannel", "ping", "Ping ", ""}
	for _, a := range bad {
		if _, err := c.Do(context.Background(), a, nil); !errors.Is(err, ErrActionNotAllowed) {
			t.Errorf("%q: esperava ErrActionNotAllowed, veio %v", a, err)
		}
	}
	// Cabecalho reservado nos parametros: nao deixa o chamador trocar a action escrita.
	for _, k := range []string{"Action", "action", "ActionID"} {
		if _, err := c.Do(context.Background(), "Ping", map[string]string{k: "Command"}); err == nil {
			t.Errorf("parametro %s deveria ser recusado", k)
		}
	}
	if _, err := c.Do(context.Background(), "Ping", nil); err != nil { // a conexao segue boa
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for _, a := range seen {
		if a != "Login" && a != "Ping" {
			t.Errorf("action %q chegou ao socket", a)
		}
	}
	if len(logged) < len(bad) || !strings.Contains(logged[0], "ERRO action recusada") {
		t.Errorf("recusas sem log de erro: %d logs", len(logged))
	}
}
