package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Cliente WebSocket minimo (RFC 6455) so com stdlib: o teste de carga nao pode
// compartilhar codigo com o servidor (github.com/coder/websocket) que ele valida.

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsConn struct {
	nc net.Conn
	br *bufio.Reader
}

// wsDial abre /ws em addr (host:port) com o cabecalho Cookie informado.
func wsDial(addr, path, cookie string) (*wsConn, error) {
	nc, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	rand.Read(key)
	k := base64.StdEncoding.EncodeToString(key)
	req := "GET " + path + " HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + k + "\r\nSec-WebSocket-Version: 13\r\n"
	if cookie != "" {
		req += "Cookie: " + cookie + "\r\n"
	}
	req += "\r\n"
	nc.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := nc.Write([]byte(req)); err != nil {
		nc.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(nc, 1<<20)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		nc.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		nc.Close()
		return nil, fmt.Errorf("upgrade recusado: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	sum := sha1.Sum([]byte(k + wsGUID))
	if resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		nc.Close()
		return nil, errors.New("Sec-WebSocket-Accept invalido")
	}
	nc.SetDeadline(time.Time{})
	return &wsConn{nc: nc, br: br}, nil
}

func (w *wsConn) Close() { w.nc.Close() }

// writeFrame envia um frame mascarado (cliente).
func (w *wsConn) writeFrame(op byte, payload []byte) error {
	h := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		h = append(h, 0x80|byte(n))
	case n < 1<<16:
		h = append(h, 0x80|126, byte(n>>8), byte(n))
	default:
		h = append(h, 0x80|127)
		h = binary.BigEndian.AppendUint64(h, uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	h = append(h, mask...)
	body := make([]byte, len(payload))
	for i, b := range payload {
		body[i] = b ^ mask[i%4]
	}
	w.nc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := w.nc.Write(append(h, body...))
	return err
}

func (w *wsConn) WriteText(b []byte) error { return w.writeFrame(1, b) }

// closeError carrega o codigo de fechamento recebido do servidor.
type closeError struct {
	Code   int
	Reason string
}

func (e *closeError) Error() string {
	return fmt.Sprintf("conexao fechada pelo servidor: codigo %d %q", e.Code, e.Reason)
}

// ReadMessage devolve a proxima mensagem de texto/binaria completa. Responde ping,
// ignora pong, e devolve *closeError no frame de fechamento.
func (w *wsConn) ReadMessage() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.br, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		masked, n := h[1]&0x80 != 0, uint64(h[1]&0x7f)
		switch n {
		case 126:
			var e [2]byte
			if _, err := io.ReadFull(w.br, e[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(e[:]))
		case 127:
			var e [8]byte
			if _, err := io.ReadFull(w.br, e[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(e[:])
		}
		if n > 256<<20 {
			return nil, fmt.Errorf("frame de %d bytes recusado", n)
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.br, mask[:]); err != nil {
				return nil, err
			}
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.br, p); err != nil {
			return nil, err
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i%4]
			}
		}
		switch op {
		case 8:
			ce := &closeError{Code: 1005}
			if len(p) >= 2 {
				ce.Code = int(binary.BigEndian.Uint16(p))
				ce.Reason = string(p[2:])
			}
			return nil, ce
		case 9:
			w.writeFrame(10, p)
		case 10:
		default: // 0 continuacao, 1 texto, 2 binario
			msg = append(msg, p...)
			if fin {
				return msg, nil
			}
		}
	}
}

func (w *wsConn) SetReadDeadline(t time.Time) { w.nc.SetReadDeadline(t) }
