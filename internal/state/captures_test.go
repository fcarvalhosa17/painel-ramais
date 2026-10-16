package state

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/ami/calls"
	"painel-ramais/internal/capture"
	"painel-ramais/internal/model"
)

// Reproduz as capturas sinteticas do simulador evento a evento e confere, depois de CADA evento,
// que nenhuma chamada esta "em conversa" sem outro lado e que a direcao de uma chamada nao
// vire "internal" depois de ter sido inbound/outbound (ao perder a outra perna).
func TestCapturesNeverTalkWithoutPeerNorFlipDirection(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/capturas/*.txt")
	re := regexp.MustCompile(`(?:PJSIP|SIP)/(\d+)-[0-9a-f]+`)
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		_, frames, _, err := capture.Read(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		prefix := "PJSIP"
		if strings.Contains(f, "-sip.") {
			prefix = "SIP"
		}
		ep := func(ch string) (string, bool) { return calls.EndpointFromChannel(ch, prefix) }
		h := newHarness(t)
		at := h.step()
		h.feed(model.SyncStarted{At: at, Technology: model.TechPJSIP, Reason: model.SyncConnect})
		seen := map[string]bool{}
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				h.feed(model.EndpointSeen{At: at, Name: m[1], Technology: model.TechPJSIP}, model.RegistrationChanged{At: at, Name: m[1], State: model.RegRegistered})
			}
		}
		h.feed(model.SyncFinished{At: at})
		h.s.Flush()
		n := 0
		lastDir := map[string]model.Direction{}
		for _, fr := range frames {
			if fr.Dir != '<' {
				continue
			}
			p := ami.NewParser(io.MultiReader(bytes.NewReader(fr.Data), strings.NewReader("\r\n\r\n")))
			for {
				m, err := p.Next()
				if err != nil {
					break
				}
				m.At = fr.At
				for _, ev := range calls.Translate(m, ep) {
					n++
					h.feed(ev)
					for num, x := range h.s.cur.exts {
						for _, c := range x.model(h.s.cur.tech).Calls {
							if c.State == model.CallTalking && c.Peer.Kind == model.PeerUnknown {
								t.Errorf("%s ev#%d %s uid=%s: ramal %s chamada %s em conversa sem outro lado", filepath.Base(f), n, m.Event(), m.Get("Uniqueid"), num, c.ID)
							}
							if d0 := lastDir[c.ID]; d0 != "" && d0 != model.DirInternal && c.Direction == model.DirInternal {
								t.Errorf("%s ev#%d %s: ramal %s chamada %s virou internal (era %s)", filepath.Base(f), n, m.Event(), num, c.ID, d0)
							}
							lastDir[c.ID] = c.Direction
						}
					}
				}
			}
		}
	}
}
