package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---- tipos do contrato, decodificados como cliente de caixa preta

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Meta json.RawMessage `json:"meta"`
}

type Snapshot struct {
	Epoch        string  `json:"epoch"`
	Version      int64   `json:"version"`
	ServerTime   string  `json:"server_time"`
	Technology   *string `json:"technology"`
	AMIConnected bool    `json:"ami_connected"`
	Extensions   []View  `json:"extensions"`
}

type Health struct {
	Status        string `json:"status"`
	DaemonVersion string `json:"daemon_version"`
	UptimeS       int64  `json:"uptime_s"`
	AMI           struct {
		Connected       bool   `json:"connected"`
		Technology      string `json:"technology"`
		AsteriskVersion string `json:"asterisk_version"`
		Reconnects      int    `json:"reconnects"`
	} `json:"ami"`
	EventsPerS float64 `json:"events_per_s"`
	Queue      struct {
		Len       int `json:"len"`
		Cap       int `json:"cap"`
		Overflows int `json:"overflows"`
	} `json:"queue"`
	LagMs      float64 `json:"lag_ms"`
	Epoch      string  `json:"epoch"`
	Version    int64   `json:"version"`
	Extensions struct {
		Total, Registered, Unregistered, Unreachable, Unknown int
	} `json:"extensions"`
	ActiveCalls int `json:"active_calls"`
	WSClients   int `json:"ws_clients"`
	Deps        []struct {
		Name, Status, Detail string
	} `json:"deps"`
}

type deltaMsg struct {
	Type         string `json:"type"`
	Epoch        string `json:"epoch"`
	FromVersion  int64  `json:"from_version"`
	ToVersion    int64  `json:"to_version"`
	AMIConnected bool   `json:"ami_connected"`
	Extensions   []View `json:"extensions"`
	Reason       string `json:"reason"`
}

// ---- daemon como processo

const (
	testUser = "loadtest"
	testPass = "loadtest-senha-123"
)

type Daemon struct {
	bin, dir, cfg, addr string
	dbg                 string // endereco do endpoint de debug do daemon (so com -heap)
	cmd                 *exec.Cmd
	logf                *os.File
	cookie              string
	hc                  *http.Client
}

func freeAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	return l.Addr().String(), nil
}

func yq(p string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(p), "'", "''") + "'" }

// startDaemon monta config/segredos num diretorio temporario, cria o usuario de teste
// pela CLI, sobe `serve` e faz login. technology e escrito no config.yaml.
func startDaemon(o opts, amiAddr, amiUser, amiSecret, tech string) (*Daemon, error) {
	bin := o.daemon
	dir, err := os.MkdirTemp("", "loadtest-daemon-")
	if err != nil {
		return nil, err
	}
	d := &Daemon{bin: bin, dir: dir, cfg: filepath.Join(dir, "config.yaml"),
		hc: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableCompression: true, MaxIdleConnsPerHost: 4}}}
	if d.addr, err = freeAddr(); err != nil {
		return nil, err
	}
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return nil, err
	}
	amiSecretFile, authSecretFile := filepath.Join(dir, "ami-secret"), filepath.Join(dir, "secret")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*7 + 3)
	}
	if err := os.WriteFile(amiSecretFile, []byte(amiSecret+"\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(authSecretFile, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, err
	}
	yml := fmt.Sprintf("listen: %q\ntechnology: %s\ndata_dir: %s\nlog_level: info\nami:\n  address: %q\n  username: %s\n  secret_file: %s\n  name_fill_per_second: %d\nauth:\n  secret_file: %s\n  session_ttl: 12h\nhistory:\n  retention_days: 30\n",
		d.addr, tech, yq(data), amiAddr, amiUser, yq(amiSecretFile), o.fillRate, yq(authSecretFile))
	if err := os.WriteFile(d.cfg, []byte(yml), 0o600); err != nil {
		return nil, err
	}
	add := exec.Command(bin, "user", "add", testUser, "--password-stdin", "--config", d.cfg)
	add.Stdin = strings.NewReader(testPass + "\n")
	if out, err := add.CombinedOutput(); err != nil {
		var ee *exec.ExitError
		if !(errors.As(err, &ee) && ee.ExitCode() == 3) {
			return nil, fmt.Errorf("`user add` falhou: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	if d.logf, err = os.Create(filepath.Join(dir, "daemon.log")); err != nil {
		return nil, err
	}
	d.cmd = exec.Command(bin, "serve", "--config", d.cfg)
	if o.heap {
		if d.dbg, err = freeAddr(); err != nil {
			return nil, err
		}
		d.cmd.Env = append(os.Environ(), "PAINEL_DEBUG_ADDR="+d.dbg)
	}
	d.cmd.Stdout, d.cmd.Stderr = d.logf, d.logf
	if err := d.cmd.Start(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if code, _, _, err := d.do("GET", "/api/health", nil, false, false); err == nil && (code == 200 || code == 503) {
			break
		}
		if time.Now().After(deadline) {
			d.Stop()
			return nil, fmt.Errorf("daemon nao respondeu em 30 s; log: %s", d.LogTail())
		}
		time.Sleep(100 * time.Millisecond)
	}
	body, _ := json.Marshal(map[string]string{"username": testUser, "password": testPass})
	code, hdr, resp, err := d.do("POST", "/api/login", body, false, false)
	if err != nil || code != 200 {
		d.Stop()
		return nil, fmt.Errorf("login falhou: HTTP %d %v %s", code, err, resp)
	}
	for _, c := range (&http.Response{Header: hdr}).Cookies() {
		if c.Name == "painel_session" {
			d.cookie = c.Name + "=" + c.Value
		}
	}
	if d.cookie == "" {
		d.Stop()
		return nil, errors.New("login sem cookie painel_session")
	}
	return d, nil
}

func (d *Daemon) Pid() int { return d.cmd.Process.Pid }

func (d *Daemon) LogTail() string {
	b, _ := os.ReadFile(filepath.Join(d.dir, "daemon.log"))
	if len(b) > 1500 {
		b = b[len(b)-1500:]
	}
	return string(b)
}

func (d *Daemon) Stop() {
	if d.cmd != nil && d.cmd.Process != nil {
		d.cmd.Process.Kill()
		d.cmd.Wait()
	}
	if d.logf != nil {
		d.logf.Close()
	}
}

func (d *Daemon) Cleanup() { os.RemoveAll(d.dir) }

// do faz uma requisicao. gz pede Accept-Encoding: gzip. Devolve o corpo como veio no fio.
func (d *Daemon) do(method, path string, body []byte, gz, auth bool) (int, http.Header, []byte, error) {
	req, err := http.NewRequest(method, "http://"+d.addr+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if gz {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	if auth {
		req.Header.Set("Cookie", d.cookie)
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b, err
}

func (d *Daemon) Health() (*Health, error) {
	code, _, b, err := d.do("GET", "/api/health", nil, false, false)
	if err != nil {
		return nil, err
	}
	if code != 200 && code != 503 {
		return nil, fmt.Errorf("health: HTTP %d", code)
	}
	var e envelope
	var h Health
	if err := json.Unmarshal(b, &e); err != nil || json.Unmarshal(e.Data, &h) != nil {
		return nil, fmt.Errorf("health: JSON invalido: %.200s", b)
	}
	return &h, nil
}

type SnapInfo struct {
	Snap       *Snapshot
	Raw, Gz    int
	GzEncoded  bool
	Dur        time.Duration
	MetaCount  int
	Calls      int
	SortedOK   bool
	EnvelopeOK bool
}

// NameProgress le o snapshot (uma requisicao, sem gzip) e conta ramais com name preenchido e correto
// ("Ramal <numero>", o callerid que o simulador configura).
func (d *Daemon) NameProgress() (filled, correct, total int, err error) {
	code, _, raw, err := d.do("GET", "/api/snapshot", nil, false, true)
	if err != nil || code != 200 {
		return 0, 0, 0, fmt.Errorf("snapshot: HTTP %d %v", code, err)
	}
	var e envelope
	var s Snapshot
	if err := json.Unmarshal(raw, &e); err != nil || json.Unmarshal(e.Data, &s) != nil {
		return 0, 0, 0, fmt.Errorf("snapshot: JSON invalido")
	}
	for _, v := range s.Extensions {
		if v.Name != "" {
			filled++
			if v.Name == "Ramal "+v.Number {
				correct++
			}
		}
	}
	return filled, correct, len(s.Extensions), nil
}

// Snapshot busca /api/snapshot sem gzip (tamanho cru) e com gzip (tamanho comprimido).
func (d *Daemon) Snapshot() (*SnapInfo, error) {
	t0 := time.Now()
	code, _, raw, err := d.do("GET", "/api/snapshot", nil, false, true)
	dur := time.Since(t0)
	if err != nil || code != 200 {
		return nil, fmt.Errorf("snapshot: HTTP %d %v %.200s", code, err, raw)
	}
	var e envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	si := &SnapInfo{Raw: len(raw), Dur: dur, EnvelopeOK: e.Success && e.Error == nil}
	var s Snapshot
	if err := json.Unmarshal(e.Data, &s); err != nil {
		return nil, err
	}
	si.Snap = &s
	var m struct{ Count int }
	json.Unmarshal(e.Meta, &m)
	si.MetaCount = m.Count
	si.SortedOK = true
	for i, x := range s.Extensions {
		si.Calls += len(x.Calls)
		if i > 0 && s.Extensions[i-1].Number >= x.Number {
			si.SortedOK = false
		}
	}
	code, hdr, gzb, err := d.do("GET", "/api/snapshot", nil, true, true)
	if err != nil || code != 200 {
		return nil, fmt.Errorf("snapshot gzip: HTTP %d %v", code, err)
	}
	si.Gz = len(gzb)
	if si.GzEncoded = hdr.Get("Content-Encoding") == "gzip"; si.GzEncoded {
		zr, err := gzip.NewReader(bytes.NewReader(gzb))
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(io.Discard, zr); err != nil {
			return nil, err
		}
	}
	return si, nil
}

// ---- cliente WebSocket que aplica o protocolo do contrato

type WSClient struct {
	d   *Daemon
	trk *Tracker

	mu          sync.Mutex
	state       map[string]View
	epoch       string
	version     int64
	amiOK       bool
	conn        *wsConn
	gen         int
	lastDeltaAt time.Time

	reloads   []string // motivos
	closes    []int
	gaps      int
	resyncs   int
	resyncing bool
	stopped   bool

	deltas   []deltaRec
	phaseOf  func() string
	resyncCb func()
}

type deltaRec struct {
	phase string
	bytes int
	exts  int
	at    time.Time
}

func NewWSClient(d *Daemon, trk *Tracker, phase func() string) *WSClient {
	return &WSClient{d: d, trk: trk, state: map[string]View{}, phaseOf: phase}
}

func (c *WSClient) Stop() {
	c.mu.Lock()
	c.stopped = true
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
}

// Resync baixa o snapshot, adota-o como estado local e abre o WS com hello.
func (c *WSClient) Resync() (*SnapInfo, error) {
	si, err := c.d.Snapshot()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.state = make(map[string]View, len(si.Snap.Extensions))
	now := time.Now()
	for _, v := range si.Snap.Extensions {
		c.state[v.Number] = v
		c.trk.Observe(v, now, false)
		c.trk.SetBaseline(v)
	}
	c.epoch, c.version, c.amiOK = si.Snap.Epoch, si.Snap.Version, si.Snap.AMIConnected
	if c.conn != nil {
		c.conn.Close()
	}
	c.gen++
	gen := c.gen
	c.mu.Unlock()
	w, err := wsDial(c.d.addr, "/ws", c.d.cookie)
	if err != nil {
		return si, err
	}
	hello, _ := json.Marshal(map[string]any{"type": "hello", "epoch": si.Snap.Epoch, "version": si.Snap.Version})
	if err := w.WriteText(hello); err != nil {
		w.Close()
		return si, err
	}
	c.mu.Lock()
	c.conn = w
	c.mu.Unlock()
	go c.readLoop(w, gen)
	return si, nil
}

func (c *WSClient) autoResync(gen int) {
	c.mu.Lock()
	if c.stopped || c.resyncing || gen != c.gen {
		c.mu.Unlock()
		return
	}
	c.resyncing = true
	c.mu.Unlock()
	for try := 0; try < 100; try++ {
		c.mu.Lock()
		stop := c.stopped
		c.mu.Unlock()
		if stop {
			break
		}
		time.Sleep(100 * time.Millisecond)
		if _, err := c.Resync(); err == nil {
			c.mu.Lock()
			c.resyncs++
			c.mu.Unlock()
			if c.resyncCb != nil {
				c.resyncCb()
			}
			break
		}
	}
	c.mu.Lock()
	c.resyncing = false
	c.mu.Unlock()
}

func (c *WSClient) readLoop(w *wsConn, gen int) {
	for {
		b, err := w.ReadMessage()
		recv := time.Now()
		if err != nil {
			c.mu.Lock()
			current := gen == c.gen
			if ce, ok := err.(*closeError); ok && current {
				c.closes = append(c.closes, ce.Code)
			}
			c.mu.Unlock()
			if current {
				go c.autoResync(gen)
			}
			return
		}
		var m deltaMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		switch m.Type {
		case "reload":
			c.mu.Lock()
			c.reloads = append(c.reloads, m.Reason)
			c.mu.Unlock()
			c.trk.Reset()
		case "delta":
			c.applyDelta(m, len(b), recv)
		}
	}
}

func (c *WSClient) applyDelta(m deltaMsg, size int, recv time.Time) {
	c.mu.Lock()
	if m.Epoch != c.epoch || m.FromVersion > c.version {
		c.gaps++
		c.mu.Unlock()
		return
	}
	apply := m.ToVersion > c.version
	if apply {
		for _, v := range m.Extensions {
			c.state[v.Number] = v
		}
		c.version = m.ToVersion
		c.amiOK = m.AMIConnected
		c.lastDeltaAt = recv
		c.deltas = append(c.deltas, deltaRec{c.phaseOf(), size, len(m.Extensions), recv})
	}
	c.mu.Unlock()
	if apply {
		for _, v := range m.Extensions {
			c.trk.Observe(v, recv, true)
		}
	}
}

// View devolve o estado local de um ramal.
func (c *WSClient) View(num string) (View, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.state[num]
	return v, ok
}

func (c *WSClient) Snapshot() map[string]View {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := make(map[string]View, len(c.state))
	for k, v := range c.state {
		m[k] = v
	}
	return m
}

func (c *WSClient) Info() (epoch string, version int64, ami bool, last time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch, c.version, c.amiOK, c.lastDeltaAt
}

func (c *WSClient) Counters() (reloads []string, closes []int, gaps, resyncs int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.reloads...), append([]int(nil), c.closes...), c.gaps, c.resyncs
}

func (c *WSClient) Deltas() []deltaRec {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]deltaRec(nil), c.deltas...)
}
