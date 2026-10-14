package state

import (
	"sort"
	"strings"
)

// inferDialEdges reconstroi, no fim da sincronizacao, as relacoes de Dial das
// chamadas que o CoreShowChannels trouxe (os DialBegin que as originaram ja passaram).
// Sem isso uma chamada tocando restaurada ficaria sem "com quem fala".
//
// Em cada Linkedid, o canal que executa Dial (dialData) e o canal destino (dialed) sao
// pareados: primeiro pelo nome (argumento do Dial e prefixo do canal); o que sobrar e
// ligado quando so resta um lado possivel.
// ponytail: heuristica por Linkedid; com varios Dial sem casar por nome e varios destinos
// soltos, o par fica sem relacao (peer unknown) em vez de chutar.
func (v *view) inferDialEdges() {
	type group struct{ srcs, dsts []*channel }
	groups := map[string]*group{}
	for _, c := range v.chans {
		if !c.fromSnap || c.dialSrc != "" {
			continue
		}
		g := groups[c.linkedID]
		if g == nil {
			g = &group{}
			groups[c.linkedID] = g
		}
		switch {
		case c.dialed:
			g.dsts = append(g.dsts, c)
		case c.dialData != "":
			g.srcs = append(g.srcs, c)
		}
	}
	for _, g := range groups {
		sort.Slice(g.srcs, func(i, j int) bool { return g.srcs[i].uid < g.srcs[j].uid })
		sort.Slice(g.dsts, func(i, j int) bool { return g.dsts[i].uid < g.dsts[j].uid })
		v.pairDials(g.srcs, g.dsts)
	}
}

func (v *view) pairDials(srcs, dsts []*channel) {
	var loose []*channel
	for _, d := range dsts {
		if s := matchDialer(srcs, d); s != nil {
			v.dialStart(s.uid, d.uid)
		} else {
			loose = append(loose, d)
		}
	}
	if len(srcs) == 1 { // um unico Dial no grupo: os destinos que sobraram sao dele (Dial A&B)
		for _, d := range loose {
			v.dialStart(srcs[0].uid, d.uid)
		}
	}
}

// matchDialer acha o canal cujo Dial cita o destino d (ex.: "PJSIP/2001,30" e "PJSIP/2001-0000002a").
func matchDialer(srcs []*channel, d *channel) *channel {
	for _, s := range srcs {
		for _, t := range dialTargets(s.dialData) {
			if strings.HasPrefix(d.name, t+"-") {
				return s
			}
		}
	}
	return nil
}

// dialTargets extrai os destinos de "PJSIP/2001&PJSIP/2002,30,tr" e tira a opcao
// de Local ("Local/x@ctx/n" -> "Local/x@ctx").
func dialTargets(data string) []string {
	first, _, _ := strings.Cut(data, ",")
	var out []string
	for _, t := range strings.Split(first, "&") {
		t = strings.TrimSpace(t)
		if strings.HasPrefix(t, "Local/") {
			if at := strings.Index(t, "@"); at >= 0 {
				if i := strings.LastIndex(t, "/"); i > at {
					t = t[:i]
				}
			}
		}
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
