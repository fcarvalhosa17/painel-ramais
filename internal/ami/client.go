package ami

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultQueueSize é a capacidade da fila de eventos (CONTRATOS.md 11.3).
	DefaultQueueSize = 65536
	// DefaultPingInterval é o intervalo sugerido de keepalive.
	DefaultPingInterval = 30 * time.Second

	dialTimeout  = 5 * time.Second
	loginTimeout = 10 * time.Second
	writeTimeout = 10 * time.Second
)

// Erros do cliente.
var (
	ErrLoginFailed = errors.New("ami: login recusado")
	ErrClosed      = errors.New("ami: conexão encerrada")
)

// ActionError é a resposta "Response: Error" do Asterisk.
type ActionError struct{ Action, Message string }

func (e *ActionError) Error() string { return "ami: " + e.Action + ": " + e.Message }

// Config configura a conexão única.
type Config struct {
	Address  string
	Username string
	Secret   string
	// Dial é injetável para teste; padrão: TCP com timeout de 5 s.
	Dial func(ctx context.Context, addr string) (net.Conn, error)
	// QueueSize: capacidade da fila de eventos; zero = DefaultQueueSize.
	QueueSize int
	// PingInterval: keepalive; zero desliga. O motivo é concreto: o firewall do
	// Issabel regrava o iptables e pode deixar a conexão meio aberta.
	PingInterval time.Duration
	// Logf recebe avisos (opcional). Nunca recebe o segredo.
	Logf func(format string, args ...any)
	// OnRefused e chamado quando uma action e recusada pela allowlist (antes de tocar no
	// socket). Controle compensatorio da decisao 1: quem monta o Config liga isto a um log
	// de nivel Error. Recebe so o nome da action, nunca os parametros.
	OnRefused func(action string)
}

// Response é a resposta de uma action: cabeçalho e, nas actions de lista,
// os eventos da lista (sem o evento final "Complete").
type Response struct {
	Head  Message
	Items []Message
}

type pending struct {
	action string
	head   Message
	items  []Message
	done   chan error // buffer 1
}

// Client é a conexão AMI única. Uma goroutine lê o socket e nunca bloqueia:
// entrega ao canal Events sem esperar o consumidor.
type Client struct {
	conn net.Conn
	cfg  Config

	wmu sync.Mutex // serializa escrita
	seq atomic.Uint64

	mu     sync.Mutex
	pend   map[string]*pending
	events chan Message
	done   chan struct{}
	err    error
	once   sync.Once

	overflows atomic.Uint64
	resync    atomic.Bool
}

// Connect disca, lê o banner e faz Login. Em erro, a conexão é fechada.
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	dial := cfg.Dial
	if dial == nil {
		dial = func(ctx context.Context, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: dialTimeout}
			return d.DialContext(ctx, "tcp", addr)
		}
	}
	conn, err := dial(ctx, cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("ami: conectar %s: %w", cfg.Address, err)
	}
	p := NewParser(conn)
	_ = conn.SetReadDeadline(time.Now().Add(loginTimeout))
	banner, err := p.ReadBanner()
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil || !strings.HasPrefix(banner, "Asterisk Call Manager") {
		conn.Close()
		return nil, fmt.Errorf("ami: banner inválido de %s: %q (%v)", cfg.Address, banner, err)
	}
	size := cfg.QueueSize
	if size <= 0 {
		size = DefaultQueueSize
	}
	c := &Client{conn: conn, cfg: cfg, pend: map[string]*pending{},
		events: make(chan Message, size), done: make(chan struct{})}
	go c.readLoop(p)

	lctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	if _, err := c.Do(lctx, "Login", map[string]string{"Username": cfg.Username, "Secret": cfg.Secret}); err != nil {
		c.shutdown(ErrClosed)
		var ae *ActionError
		if errors.As(err, &ae) {
			return nil, fmt.Errorf("%w: %s", ErrLoginFailed, ae.Message)
		}
		return nil, err
	}
	if cfg.PingInterval > 0 {
		go c.pingLoop(cfg.PingInterval)
	}
	return c, nil
}

// ConnectRetry tenta Connect até conseguir ou o ctx acabar, esperando conforme
// o backoff. Login recusado também é repetido: o IssabelPBX regera o
// manager.conf e o usuário pode voltar sozinho. onFail é opcional.
func ConnectRetry(ctx context.Context, cfg Config, b *Backoff, onFail func(err error, wait time.Duration)) (*Client, error) {
	for {
		c, err := Connect(ctx, cfg)
		if err == nil {
			b.Reset()
			return c, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		wait := b.Next()
		if onFail != nil {
			onFail(err, wait)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Events é a fila de eventos ao vivo. É fechada quando a conexão cai
// (esvazie e depois consulte Err).
func (c *Client) Events() <-chan Message { return c.events }

// Done fecha quando a conexão termina.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err devolve o motivo do término (nil enquanto conectado).
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Overflows conta eventos descartados por fila cheia.
func (c *Client) Overflows() uint64 { return c.overflows.Load() }

// QueueLen e QueueCap descrevem a fila de eventos.
func (c *Client) QueueLen() int { return len(c.events) }

// QueueCap devolve a capacidade da fila de eventos.
func (c *Client) QueueCap() int { return cap(c.events) }

// TakeResync devolve true uma vez depois de um descarte, quando a fila já
// esvaziou: é o sinal para o supervisor refazer o estado (SyncStarted overflow).
func (c *Client) TakeResync() bool {
	if len(c.events) != 0 {
		return false
	}
	return c.resync.CompareAndSwap(true, false)
}

// Do envia uma action da allowlist e espera a resposta (e a lista inteira,
// nas actions de lista). "Response: Error" vira *ActionError.
func (c *Client) Do(ctx context.Context, action string, params map[string]string) (Response, error) {
	if err := validate(action, params); err != nil {
		switch { // recusada ANTES de tocar no socket
		case c.cfg.OnRefused != nil:
			c.cfg.OnRefused(action)
		case c.cfg.Logf != nil:
			c.cfg.Logf("ami: ERRO action recusada (fora da allowlist) action=%q", action)
		}
		return Response{}, err
	}
	id := "p" + strconv.FormatUint(c.seq.Add(1), 10)
	p := &pending{action: action, done: make(chan error, 1)}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return Response{}, err
	}
	c.pend[id] = p
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pend, id); c.mu.Unlock() }()

	if err := c.write(action, id, params); err != nil {
		return Response{}, err
	}
	select {
	case err := <-p.done:
		if err != nil {
			return Response{}, err
		}
		return Response{Head: p.head, Items: p.items}, nil
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-c.done:
		return Response{}, c.Err()
	}
}

func (c *Client) write(action, id string, params map[string]string) error {
	var sb strings.Builder
	sb.WriteString("Action: " + action + "\r\nActionID: " + id + "\r\n")
	for k, v := range params {
		sb.WriteString(k + ": " + v + "\r\n")
	}
	sb.WriteString("\r\n")
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := c.conn.Write([]byte(sb.String())); err != nil {
		c.shutdown(fmt.Errorf("ami: escrita: %w", err))
		return c.Err()
	}
	return nil
}

// readLoop e o unico produtor de c.events: so ele o fecha (shutdown vem de qualquer goroutine).
func (c *Client) readLoop(p *Parser) {
	defer close(c.events)
	for {
		m, err := p.Next()
		if err != nil {
			c.shutdown(fmt.Errorf("ami: leitura: %w", err))
			return
		}
		c.dispatch(m)
	}
}

// dispatch entrega respostas a quem espera e eventos à fila, sem bloquear.
func (c *Client) dispatch(m Message) {
	if id := m.ActionID(); id != "" {
		c.mu.Lock()
		p := c.pend[id]
		c.mu.Unlock()
		if p != nil {
			p.accept(m)
			return
		}
	}
	if m.IsResponse() {
		return // resposta tardia de action que já desistiu
	}
	select {
	case c.events <- m:
	default:
		c.overflows.Add(1)
		if c.resync.CompareAndSwap(false, true) && c.cfg.Logf != nil {
			c.cfg.Logf("ami: fila de eventos cheia (%d); descartando e marcando ressincronização", cap(c.events))
		}
	}
}

// accept roda só na goroutine leitora; done tem buffer 1 e recebe no máximo
// um valor (depois do primeiro, o Do já desistiu de olhar o pending).
func (p *pending) accept(m Message) {
	if len(p.done) > 0 {
		return
	}
	if m.IsResponse() {
		p.head = m
		switch {
		case strings.EqualFold(m.Get("response"), "Error"):
			p.done <- &ActionError{Action: p.action, Message: m.Get("message")}
		case strings.EqualFold(m.Get("eventlist"), "start"):
			// lista: segue coletando até o evento "Complete"
		default:
			p.done <- nil
		}
		return
	}
	if strings.EqualFold(m.Get("eventlist"), "Complete") {
		p.done <- nil
		return
	}
	p.items = append(p.items, m)
}

// shutdown encerra uma vez: guarda o erro, fecha Done e a conexão (o que faz o readLoop sair
// e fechar Events).
func (c *Client) shutdown(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		c.conn.Close()
		close(c.done)
	})
}

// Close faz Logoff (melhor esforço) e fecha a conexão.
func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, _ = c.Do(ctx, "Logoff", nil)
	cancel()
	c.shutdown(ErrClosed)
	return nil
}

func (c *Client) pingLoop(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), every/2+time.Second)
			_, err := c.Do(ctx, "Ping", nil)
			cancel()
			if err != nil {
				c.shutdown(fmt.Errorf("ami: ping sem resposta: %w", err))
				return
			}
		}
	}
}
