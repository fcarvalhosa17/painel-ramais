// Package ami implementa o cliente do Asterisk Manager Interface: framing,
// login, allowlist de actions (somente leitura) e leitura que nunca bloqueia.
// Não importa nenhum outro pacote interno (CONTRATOS.md seção 12).
package ami

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"time"
)

// maxLine limita uma linha AMI. Linha maior derruba a conexão (protocolo corrompido).
const maxLine = 64 << 10

// ErrLineTooLong indica linha AMI acima de maxLine.
var ErrLineTooLong = errors.New("ami: linha acima do limite")

// Message é um bloco AMI (resposta ou evento). As chaves ficam em minúsculas:
// o Asterisk trata o nome do campo sem diferenciar maiúsculas.
type Message struct {
	Fields map[string]string
	// At é a hora em que o leitor completou o bloco (base do lag_ms).
	At time.Time
}

// NewMessage monta uma mensagem a partir de pares chave, valor (testes e fixtures).
func NewMessage(kv ...string) Message {
	m := Message{Fields: make(map[string]string, len(kv)/2), At: time.Now()}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Fields[strings.ToLower(kv[i])] = kv[i+1]
	}
	return m
}

// Get devolve o valor do campo ("" se ausente). A chave não diferencia maiúsculas.
func (m Message) Get(key string) string { return m.Fields[strings.ToLower(key)] }

// Event devolve o valor de "Event".
func (m Message) Event() string { return m.Fields["event"] }

// ActionID devolve o valor de "ActionID".
func (m Message) ActionID() string { return m.Fields["actionid"] }

// IsResponse informa se o bloco é uma resposta de action.
func (m Message) IsResponse() bool { _, ok := m.Fields["response"]; return ok }

// Parser lê blocos AMI de um stream. Blocos chegam fatiados em qualquer
// ponto (inclusive no meio de uma linha); o bufio acumula até a linha fechar.
type Parser struct{ r *bufio.Reader }

// NewParser cria um parser sobre r.
func NewParser(r io.Reader) *Parser { return &Parser{r: bufio.NewReaderSize(r, maxLine)} }

func (p *Parser) readLine() (string, error) {
	b, err := p.r.ReadSlice('\n')
	switch {
	case err == nil:
		return string(bytes.TrimRight(b, "\r\n")), nil
	case errors.Is(err, bufio.ErrBufferFull):
		return "", ErrLineTooLong
	case errors.Is(err, io.EOF) && len(b) > 0:
		return "", io.ErrUnexpectedEOF
	default:
		return "", err
	}
}

// ReadBanner lê a primeira linha da conexão ("Asterisk Call Manager/X.Y.Z").
func (p *Parser) ReadBanner() (string, error) { return p.readLine() }

// Next devolve o próximo bloco completo. Linhas em branco entre blocos são
// ignoradas; linha sem ':' dentro do bloco também (ex.: "--END COMMAND--").
func (p *Parser) Next() (Message, error) {
	var fields map[string]string
	for {
		line, err := p.readLine()
		if err != nil {
			if fields != nil && errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return Message{}, err
		}
		if line == "" {
			if fields == nil {
				continue
			}
			return Message{Fields: fields, At: time.Now()}, nil
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if fields == nil {
			fields = make(map[string]string, 12)
		}
		fields[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
}

// ParseAll lê todos os blocos de r (fixtures de teste). Aceita o último bloco
// sem a linha em branco final.
func ParseAll(r io.Reader) ([]Message, error) {
	p := NewParser(io.MultiReader(r, strings.NewReader("\n\n")))
	var out []Message
	for {
		m, err := p.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
}
