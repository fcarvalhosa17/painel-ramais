package api

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"painel-ramais/internal/model"
)

const stampLayout = "2006-01-02T15:04:05.000Z"

// Codigos de fechamento do WebSocket (CONTRATOS.md secao 5.4).
const (
	closeReload   = 4000
	closeSession  = 4001
	closeSlow     = 4002
	closeProtocol = 4003
)

func quote(s string) []byte {
	b, _ := json.Marshal(s)
	return b
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("serializar resposta: " + err.Error()) // so tipos proprios, nunca entrada do usuario
	}
	return b
}

func techJSON(t model.Technology) []byte {
	if t == "" {
		return []byte("null")
	}
	return quote(string(t))
}

// deltaFrame monta a mensagem delta uma vez; os mesmos bytes vao a todos os clientes.
func deltaFrame(b model.Batch, raws [][]byte, now time.Time) []byte {
	var buf bytes.Buffer
	buf.Grow(len(raws)*320 + 200)
	buf.WriteString(`{"type":"delta","epoch":`)
	buf.Write(quote(b.Epoch))
	buf.WriteString(`,"from_version":` + strconv.FormatUint(b.FromVersion, 10))
	buf.WriteString(`,"to_version":` + strconv.FormatUint(b.ToVersion, 10))
	buf.WriteString(`,"ami_connected":` + strconv.FormatBool(b.AMIConnected))
	buf.WriteString(`,"server_time":`)
	buf.Write(quote(now.UTC().Format(stampLayout)))
	buf.WriteString(`,"extensions":[`)
	for i, r := range raws {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(r)
	}
	buf.WriteString(`],"removed":[`)
	for i, n := range b.Removed {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(quote(n))
	}
	buf.WriteByte(']')
	if b.Groups != nil { // so quando o catalogo mudou; o epoch nao muda
		buf.WriteString(`,"groups":`)
		buf.Write(catalogJSON(b.Groups))
	}
	// rc.8: as quatro chaves de chamadas e filas so saem quando a lista tem item (ausente = nada mudou).
	if len(b.Calls) > 0 {
		buf.WriteString(`,"calls":`)
		buf.Write(mustJSON(b.Calls))
	}
	if len(b.CallsRemoved) > 0 {
		buf.WriteString(`,"calls_removed":`)
		buf.Write(mustJSON(b.CallsRemoved))
	}
	if len(b.Queues) > 0 {
		buf.WriteString(`,"queues":`)
		buf.Write(mustJSON(b.Queues))
	}
	if len(b.QueuesRemoved) > 0 {
		buf.WriteString(`,"queues_removed":`)
		buf.Write(mustJSON(b.QueuesRemoved))
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

type catalogGroup struct {
	Name   string      `json:"name"`
	Ranges []rangeJSON `json:"ranges"`
}

// catalogJSON serializa o catalogo de grupos ({"name","ranges":[{"start","end"}]}); nil vira [].
func catalogJSON(groups []model.Group) []byte {
	out := make([]catalogGroup, len(groups))
	for i, g := range groups {
		rs := make([]rangeJSON, len(g.Ranges))
		for j, r := range g.Ranges {
			rs[j] = rangeJSON{r.Start, r.End}
		}
		out[i] = catalogGroup{g.Name, rs}
	}
	return mustJSON(out)
}

func reloadFrame(reason, epoch string) []byte {
	return mustJSON(struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
		Epoch  string `json:"epoch"`
	}{"reload", reason, epoch})
}

// sessionFrame e o aviso enviado antes do fechamento 4001 (reason: expired ou revoked).
func sessionFrame(reason string) []byte {
	return mustJSON(struct {
		Type    string `json:"type"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}{"session_expired", reason, "Sessao encerrada; faca login novamente"})
}
