package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Formato (versao 1), em texto, uma unidade por quadro:
//
//	#PAINEL-RAMAIS-CAPTURA 1
//	#meta {json}
//	<dir> <data RFC 3339 UTC com nanossegundos> <n>\n
//	<n bytes do quadro, exatamente como no socket, linhas CRLF>\n
//	...
//	#fim {json}
//
// dir "<" = recebido do Asterisk; ">" = enviado pelo daemon. Linhas que comecam com "#"
// fora de um quadro sao comentarios (meta e fim levam JSON). n e o tamanho depois da limpeza.
const (
	Magic     = "#PAINEL-RAMAIS-CAPTURA 1"
	DirRecv   = '<'
	DirSent   = '>'
	maxFrame  = 4 << 20
	timeStamp = time.RFC3339Nano
)

// Frame e um quadro da captura.
type Frame struct {
	Dir  byte
	At   time.Time
	Data []byte
}

// Writer grava a captura. Seguro para uso concorrente.
type Writer struct {
	mu     sync.Mutex
	w      *bufio.Writer
	san    *Sanitizer
	frames int
}

// NewWriter escreve o cabecalho e a linha meta.
func NewWriter(w io.Writer, san *Sanitizer, meta map[string]any) (*Writer, error) {
	cw := &Writer{w: bufio.NewWriter(w), san: san}
	mj, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(cw.w, "%s\n#meta %s\n", Magic, mj)
	return cw, cw.w.Flush()
}

// WriteFrame limpa e grava um quadro.
func (c *Writer) WriteFrame(dir byte, at time.Time, raw []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data := c.san.Frame(raw) // o Sanitizer nao e concorrente: sob o mesmo lock
	fmt.Fprintf(c.w, "%c %s %d\n", dir, at.UTC().Format(timeStamp), len(data))
	c.w.Write(data)
	c.w.WriteByte('\n')
	c.frames++
	return c.w.Flush()
}

// Frames diz quantos quadros foram gravados.
func (c *Writer) Frames() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frames
}

// Close escreve a linha final.
func (c *Writer) Close(extra map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if extra == nil {
		extra = map[string]any{}
	}
	extra["quadros"] = c.frames
	j, _ := json.Marshal(extra)
	fmt.Fprintf(c.w, "#fim %s\n", j)
	return c.w.Flush()
}

// Read le uma captura inteira: meta, quadros e a linha final (nil se faltar).
func Read(r io.Reader) (meta map[string]any, frames []Frame, end map[string]any, err error) {
	br := bufio.NewReaderSize(r, 1<<16)
	line, err := br.ReadString('\n')
	if err != nil || strings.TrimRight(line, "\r\n") != Magic {
		return nil, nil, nil, errors.New("captura: cabecalho ausente ou versao desconhecida")
	}
	for {
		line, err = br.ReadString('\n')
		if err == io.EOF && line == "" {
			return meta, frames, end, nil
		}
		if err != nil && err != io.EOF {
			return nil, nil, nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "#meta "):
			meta = parseJSON(line[len("#meta "):])
		case strings.HasPrefix(line, "#fim "):
			end = parseJSON(line[len("#fim "):])
		case strings.HasPrefix(line, "#"), line == "":
		default:
			f, ferr := readFrame(line, br)
			if ferr != nil {
				return nil, nil, nil, ferr
			}
			frames = append(frames, f)
		}
	}
}

func parseJSON(s string) map[string]any {
	var m map[string]any
	json.Unmarshal([]byte(s), &m)
	return m
}

func readFrame(head string, br *bufio.Reader) (Frame, error) {
	parts := strings.Fields(head)
	if len(parts) != 3 || (parts[0] != "<" && parts[0] != ">") {
		return Frame{}, fmt.Errorf("captura: cabecalho de quadro invalido %q", head)
	}
	at, err := time.Parse(timeStamp, parts[1])
	n, err2 := strconv.Atoi(parts[2])
	if err != nil || err2 != nil || n < 0 || n > maxFrame {
		return Frame{}, fmt.Errorf("captura: cabecalho de quadro invalido %q", head)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(br, data); err != nil {
		return Frame{}, fmt.Errorf("captura: quadro truncado: %w", err)
	}
	if b, err := br.ReadByte(); err != nil || b != '\n' {
		return Frame{}, errors.New("captura: quadro sem terminador")
	}
	return Frame{Dir: parts[0][0], At: at, Data: data}, nil
}

// splitter corta um fluxo AMI em quadros: o banner (uma linha) e os blocos terminados em
// linha em branco.
type splitter struct{ buf []byte }

func (s *splitter) feed(p []byte) (out [][]byte) {
	s.buf = append(s.buf, p...)
	for {
		var end int
		if bytes.HasPrefix(s.buf, []byte("Asterisk Call Manager")) {
			end = bytes.Index(s.buf, []byte("\r\n"))
			if end >= 0 {
				end += 2
			}
		} else if i := bytes.Index(s.buf, []byte("\r\n\r\n")); i >= 0 {
			end = i + 4
		} else {
			end = -1
		}
		if end < 0 {
			if len(s.buf) > maxFrame {
				out = append(out, s.buf) // protocolo fora do esperado: nao acumula sem limite
				s.buf = nil
			}
			return out
		}
		out = append(out, append([]byte(nil), s.buf[:end]...))
		s.buf = s.buf[end:]
	}
}

// Tap embrulha a conexao e grava tudo que passa por ela (leitura e escrita).
func Tap(c net.Conn, w *Writer, onErr func(error)) net.Conn {
	return &tapConn{Conn: c, w: w, onErr: onErr}
}

type tapConn struct {
	net.Conn
	w        *Writer
	onErr    func(error)
	rmu, wmu sync.Mutex
	rsplit   splitter
	wsplit   splitter
}

func (t *tapConn) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)
	if n > 0 {
		t.rmu.Lock()
		t.record(DirRecv, &t.rsplit, p[:n])
		t.rmu.Unlock()
	}
	return n, err
}

func (t *tapConn) Write(p []byte) (int, error) {
	n, err := t.Conn.Write(p)
	if n > 0 {
		t.wmu.Lock()
		t.record(DirSent, &t.wsplit, p[:n])
		t.wmu.Unlock()
	}
	return n, err
}

func (t *tapConn) record(dir byte, sp *splitter, p []byte) {
	now := time.Now()
	for _, fr := range sp.feed(p) {
		if err := t.w.WriteFrame(dir, now, fr); err != nil && t.onErr != nil {
			t.onErr(err)
		}
	}
}
