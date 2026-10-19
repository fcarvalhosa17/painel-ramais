package state

import (
	"sort"
	"strconv"
	"strings"

	"painel-ramais/internal/model"
)

// Listas compartilhadas e somente leitura: nunca mudam depois de criadas (o hub as le de outra goroutine).
var (
	noNames     = []string{}
	noGroupOnly = []string{model.NoGroup}
)

type rangedGroup struct {
	name   string
	ranges []model.Range // ordenadas por Start; dentro de um grupo nao se sobrepoem
}

// groupIdx resolve os grupos de um ramal (ADR-006, CONTRATOS 10 "Grupos no daemon"). E imutavel depois
// de montada: o State troca o ponteiro inteiro a cada GroupsLoaded.
type groupIdx struct {
	ranged []rangedGroup       // so grupos com faixa, na ordem do catalogo (rank)
	rank   map[string]int      // posicao do grupo no catalogo ordenado por strings.ToLower(nome)
	manual map[string][]string // numero -> grupos com vinculo manual; nunca mutado
}

// newGroupIdx monta a resolucao a partir do catalogo completo e dos vinculos manuais.
func newGroupIdx(catalog []model.Group, manual map[string][]string) *groupIdx {
	gs := append([]model.Group(nil), catalog...)
	sort.SliceStable(gs, func(i, j int) bool { return strings.ToLower(gs[i].Name) < strings.ToLower(gs[j].Name) })
	gi := &groupIdx{rank: make(map[string]int, len(gs)), manual: manual}
	for i, g := range gs {
		gi.rank[g.Name] = i
		if len(g.Ranges) == 0 {
			continue
		}
		rs := append([]model.Range(nil), g.Ranges...)
		sort.Slice(rs, func(a, b int) bool { return rs[a].Start < rs[b].Start })
		gi.ranged = append(gi.ranged, rangedGroup{name: g.Name, ranges: rs})
	}
	return gi
}

// resolve devolve (groups, rangeGroups, manualGroups) do ramal. Nenhuma das tres e nil; groups nunca e
// vazia ("Sem grupo" quando nao ha vinculo). Ramal nao numerico, com zero a esquerda ou com mais de 18
// digitos nunca cai numa faixa (so no vinculo manual, que e pelo texto do numero).
func (g *groupIdx) resolve(number string) (all, rng, man []string) {
	rng, man = noNames, noNames
	if n, ok := numeric(number); ok {
		var hit []string
		for _, rg := range g.ranged {
			// busca binaria por grupo: dentro do grupo as faixas nao se sobrepoem
			i := sort.Search(len(rg.ranges), func(i int) bool { return rg.ranges[i].Start > n }) - 1
			if i >= 0 && n <= rg.ranges[i].End {
				hit = append(hit, rg.name)
			}
		}
		if hit != nil {
			rng = hit // ja na ordem do catalogo
		}
	}
	if m := g.manual[number]; len(m) > 0 {
		man = append([]string(nil), m...)
		g.sortNames(man)
	}
	switch {
	case len(rng) == 0 && len(man) == 0:
		return noGroupOnly, rng, man
	case len(man) == 0:
		return rng, rng, man
	case len(rng) == 0:
		return man, rng, man
	}
	all = append(append(make([]string, 0, len(rng)+len(man)), rng...), man...)
	g.sortNames(all)
	out := all[:1]
	for _, n := range all[1:] { // tira repetido (grupo na faixa e no manual)
		if n != out[len(out)-1] {
			out = append(out, n)
		}
	}
	return out, rng, man
}

// sortNames ordena como o catalogo: pela posicao dele; nome fora do catalogo (nao deveria existir) vai
// para o fim, por nome.
func (g *groupIdx) sortNames(names []string) {
	sort.Slice(names, func(i, j int) bool {
		ri, iok := g.rank[names[i]]
		rj, jok := g.rank[names[j]]
		switch {
		case iok && jok && ri != rj:
			return ri < rj
		case iok != jok:
			return iok
		}
		return names[i] < names[j]
	})
}

// numeric converte o numero do ramal para a comparacao com as faixas: so digitos, sem zero a esquerda
// e ate 18 digitos (cabe em int64).
func numeric(number string) (int64, bool) {
	if len(number) == 0 || len(number) > 18 || (number[0] == '0' && len(number) > 1) {
		return 0, false
	}
	for i := 0; i < len(number); i++ {
		if number[i] < '0' || number[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(number, 10, 64)
	return n, err == nil
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
