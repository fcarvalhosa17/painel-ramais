package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"painel-ramais/internal/store"
)

const (
	helloTimeout   = 5 * time.Second
	writeTimeout   = 10 * time.Second
	pingInterval   = 25 * time.Second // abaixo do Timeout do Apache
	clientQueueLen = 64
	readLimit      = 1024
)

type outMsg struct {
	data   []byte
	code   websocket.StatusCode // != 0: fecha depois de escrever data
	reason string
}

// client e uma conexao WebSocket registrada no hub. O hub so enfileira (sem
// bloquear); uma goroutine escritora esvazia a fila com timeout por escrita.
type client struct {
	out     chan outMsg
	closed  chan struct{}
	done    chan struct{} // fecha quando a escritora termina
	once    sync.Once
	code    websocket.StatusCode
	reason  string
	hash    [32]byte
	expires time.Time
}

func newClient(hash [32]byte, expires time.Time) *client {
	return &client{out: make(chan outMsg, clientQueueLen), closed: make(chan struct{}), done: make(chan struct{}), hash: hash, expires: expires}
}

// send enfileira sem bloquear; false se a fila esta cheia.
func (c *client) send(data []byte) bool {
	select {
	case c.out <- outMsg{data: data}:
		return true
	default:
		return false
	}
}

// sendReload enfileira o reload seguido do fechamento 4000; fila cheia vira cliente lento.
func (c *client) sendReload(data []byte) {
	select {
	case c.out <- outMsg{data: data, code: closeReload, reason: "recarregue o snapshot"}:
	default:
		c.kick(websocket.StatusCode(closeSlow), "cliente lento")
	}
}

// kick manda fechar a conexao com o codigo dado (a primeira chamada vale).
func (c *client) kick(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		c.code, c.reason = code, reason
		close(c.closed)
	})
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request, sess store.Session) {
	// O ReadTimeout do http.Server vale para a conexao inteira; o WS vive muito alem dele.
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return // Accept ja respondeu (ex.: 403 de Origin)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(readLimit)
	epoch, version, err := readHello(r.Context(), conn)
	if err != nil {
		conn.Close(closeProtocol, "hello invalido")
		return
	}
	c := newClient(sess.IDHash, sess.ExpiresAt)
	reason, ok := s.hub.Join(c, epoch, version)
	if !ok {
		conn.Close(websocket.StatusGoingAway, "daemon parando")
		return
	}
	s.log.Info("ws conectado", "request_id", requestID(r), "sub", sess.Subject, "reload", reason)
	defer s.hub.Leave(c)
	go s.writer(conn, c)
	// Depois do hello o cliente nao manda dado; Read segue tratando ping/pong/close.
	if _, _, err := conn.Read(r.Context()); err == nil {
		c.kick(closeProtocol, "mensagem inesperada")
	} else {
		c.kick(websocket.StatusNormalClosure, "")
	}
	<-c.done
}

func readHello(ctx context.Context, conn *websocket.Conn) (string, uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageText {
		return "", 0, errors.New("sem hello")
	}
	var m struct {
		Type    string  `json:"type"`
		Epoch   *string `json:"epoch"`
		Version *uint64 `json:"version"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil || m.Type != "hello" || m.Epoch == nil || m.Version == nil {
		return "", 0, errors.New("hello invalido")
	}
	return *m.Epoch, *m.Version, nil
}

func (s *Server) writer(conn *websocket.Conn, c *client) {
	defer close(c.done)
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	expiry := time.NewTimer(c.expires.Sub(s.Now())) // relogio do servidor (injetavel), nao o de parede
	defer expiry.Stop()
	renew := time.NewTicker(s.renewEvery())
	defer renew.Stop()
	for {
		select {
		case m := <-c.out:
			if !s.write(conn, c, m) {
				return
			}
		case <-ping.C:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := conn.Ping(ctx)
			cancel()
			if err != nil {
				conn.Close(websocket.StatusCode(closeSlow), "sem resposta ao ping")
				return
			}
		case <-renew.C:
			if !s.renewSession(conn, c, expiry) {
				return
			}
		case <-expiry.C:
			s.closeSession(conn, c, "expired")
			return
		case <-c.closed:
			if c.code == closeSession {
				s.closeSession(conn, c, c.reason)
				return
			}
			conn.Close(c.code, c.reason)
			return
		}
	}
}

// write escreve uma mensagem; false se a conexao acabou.
func (s *Server) write(conn *websocket.Conn, c *client, m outMsg) bool {
	if m.data != nil {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err := conn.Write(ctx, websocket.MessageText, m.data)
		cancel()
		if err != nil {
			conn.Close(websocket.StatusCode(closeSlow), "escrita lenta")
			return false
		}
	}
	if m.code != 0 {
		conn.Close(m.code, m.reason)
		return false
	}
	return true
}

// renewEvery: a sessao e estendida varias vezes por periodo de inatividade.
func (s *Server) renewEvery() time.Duration {
	if s.RenewEvery > 0 {
		return s.RenewEvery
	}
	return max(s.SessionTTL/6, time.Second)
}

// renewSession estende a sessao enquanto o WebSocket esta ativo (tela de NOC nunca expira).
// false se a sessao acabou: o aviso e o fechamento 4001 ja foram feitos.
func (s *Server) renewSession(conn *websocket.Conn, c *client, expiry *time.Timer) bool {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	now := s.Now()
	err := s.Store.RenewSession(ctx, c.hash, now, now.Add(s.SessionTTL))
	switch {
	case err == nil:
		c.expires = now.Add(s.SessionTTL)
		if !expiry.Stop() {
			select {
			case <-expiry.C:
			default:
			}
		}
		expiry.Reset(s.SessionTTL)
		return true
	case errors.Is(err, store.ErrNotFound):
		reason := "revoked"
		if !now.Before(c.expires) {
			reason = "expired"
		}
		s.closeSession(conn, c, reason)
		return false
	}
	s.log.Warn("ws: renovar sessao", "erro", err.Error()) // banco ocupado: tenta no proximo ciclo
	return true
}

// closeSession avisa explicitamente (mensagem tipada) e fecha com 4001, para a tela
// mostrar "sessao encerrada" em vez de congelar.
func (s *Server) closeSession(conn *websocket.Conn, c *client, reason string) {
	s.write(conn, c, outMsg{data: sessionFrame(reason), code: closeSession, reason: "sessao " + reason})
}
