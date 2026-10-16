package store

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// 3.000 linhas novas, cada uma sobrepondo as 3.000 faixas gravadas (9 milhoes de pares):
// a checagem conta e guarda so as 20 primeiras mensagens, sem esgotar a memoria.
func TestCheckExistingDoesNotAccumulateMessages(t *testing.T) {
	s := open(t)
	var have []GroupRow
	for i := 0; i < 3000; i++ {
		have = append(have, GroupRow{Line: i + 1, Name: fmt.Sprintf("G%d", i), Start: int64(i * 10), End: int64(i*10 + 5)})
	}
	if err := s.ImportGroups(ctx, have, true, now); err != nil {
		t.Fatal(err)
	}
	var rows []GroupRow
	for i := 0; i < 3000; i++ {
		rows = append(rows, GroupRow{Line: i + 1, Name: fmt.Sprintf("N%d", i), Start: 0, End: 1_000_000})
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := s.ImportGroups(ctx, rows, false, now)
	runtime.ReadMemStats(&after)

	if !errors.Is(err, ErrOverlap) {
		t.Fatalf("err = %v, queria ErrOverlap", err)
	}
	msg := err.Error()
	if got := strings.Count(msg, "\n"); got != 21 { // 20 mensagens + a linha "... e mais N"
		t.Errorf("%d quebras de linha na mensagem, queria 21", got)
	}
	if !strings.Contains(msg, "... e mais 8999980") { // 9.000.000 pares - 20 mostrados
		t.Errorf("contagem do resto ausente: %.200s", msg[len(msg)-80:])
	}
	t.Logf("alocacao: %d KiB", (after.TotalAlloc-before.TotalAlloc)>>10)
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 10<<20 {
		t.Errorf("alocou %d MiB, o teto e 10 MiB", alloc>>20)
	}
}

// Nome de grupo com controle ou marca bidirecional e recusado (disfarca texto na tela).
func TestParseCSVRefusesControlAndBidiInName(t *testing.T) {
	bad := map[string]string{
		"RLO U+202E":  "Supo\u202erte",
		"LRE U+202A":  "\u202aSuporte",
		"PDF U+202C":  "Suporte\u202c",
		"LRI U+2066":  "Su\u2066porte",
		"PDI U+2069":  "Suporte\u2069",
		"NUL":         "Su\x00porte",
		"SOH":         "Su\x01porte",
		"DEL":         "Su\x7fporte",
		"NEL (C1)":    "Su\u0085porte",
		"tab interno": "Su\tporte",
		"ZWSP U+200B": "Su\u200bporte",
		"LRM U+200E":  "Su\u200eporte",
		"RLM U+200F":  "Su\u200fporte",
		"ALM U+061C":  "Su\u061cporte",
		"BOM U+FEFF":  "Su\ufeffporte",
		"LS U+2028":   "Su\u2028porte",
		"PS U+2029":   "Su\u2029porte",
	}
	for name, grp := range bad {
		_, err := ParseGroupsCSV(strings.NewReader(grp + ",2000\n"))
		if err == nil || !strings.Contains(err.Error(), "caractere de controle ou de formatacao invisivel") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, ok := range []string{"Suporte", "Vendas Norte", "A\u00e7\u00e3o", "\u65e5\u672c\u8a9e", "Time-1 (SP)"} {
		if _, err := ParseGroupsCSV(strings.NewReader(ok + ",2000\n")); err != nil {
			t.Errorf("%q deveria valer: %v", ok, err)
		}
	}
}
