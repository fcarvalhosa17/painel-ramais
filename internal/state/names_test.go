package state

import (
	"strings"
	"testing"
	"unicode/utf8"

	"painel-ramais/internal/model"
)

// cp monta texto a partir de pontos de codigo (o fonte nao carrega controle nem bidi crus).
func cp(cps ...rune) string { return string(cps) }

// Nome vindo de fora (display name do telefone, callerid do Asterisk, cache antigo) passa
// por CleanName em todas as entradas: sem controle/bidi e no maximo 64 caracteres.
func TestNamesFromOutsideAreNormalized(t *testing.T) {
	h := newHarness(t)
	evil := "Joao" + cp(0x202E) + "evil" + cp(0x1b) + "[2J" + strings.Repeat("x", 100)
	h.feed(model.NamesLoaded{At: h.now, Names: map[string]string{"2002": "Maria" + cp(0x2066, 0x200B) + " Souza"}})

	at := h.step()
	h.feed(model.SyncStarted{At: at, Technology: model.TechPJSIP, Reason: model.SyncConnect},
		model.EndpointSeen{At: at, Name: "2001", CallerIDName: evil},
		model.EndpointSeen{At: at, Name: "2002"},
		model.EndpointSeen{At: at, Name: "2003"},
		model.SyncFinished{At: at})
	got := nameOf(h, "2001")
	if utf8.RuneCountInString(got) > model.MaxNameRunes || strings.ContainsAny(got, cp(0x202E, 0x1b)) || !strings.HasPrefix(got, "Joaoevil[2J") {
		t.Errorf("EndpointSeen: %q", got)
	}
	if nameOf(h, "2002") != "Maria Souza" {
		t.Errorf("cache antigo: %q", nameOf(h, "2002"))
	}

	// Nome aprendido pelo display name de quem disca.
	h.s.Flush()
	at = h.step()
	h.feed(
		chn(at, "u1", "u1", "PJSIP/2003-0001", "2003", "2003", "Ana"+cp(0x202E, 0x0)+"Paula", "2001"),
		chn(at, "u2", "u1", "PJSIP/2001-0002", "2001", "2001", "", "2001"),
		model.DialStarted{At: at, SrcUniqueid: "u1", DstUniqueid: "u2"},
	)
	if nameOf(h, "2003") != "AnaPaula" {
		t.Errorf("display name do telefone: %q", nameOf(h, "2003"))
	}
	// Nome do outro lado da chamada (peer) tambem sai limpo.
	for _, c := range h.ext("2001").Calls {
		if c.Peer.Name != nil && strings.ContainsAny(*c.Peer.Name, cp(0x202E, 0)) {
			t.Errorf("peer.name: %q", *c.Peer.Name)
		}
	}
}

// Cada sincronizacao completa informa os ramais existentes (base do expurgo do cache).
func TestOnSyncReportsExistingExtensions(t *testing.T) {
	var got [][]string
	h := newHarness(t)
	h.s.onSync = func(n []string) { got = append(got, n) }
	h.online()
	if len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("OnSync: %v", got)
	}
}
