package store

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const noGroupName = "Sem grupo"

// ParseGroupsCSV le o CSV de grupos (CONTRATOS.md secao 9). Qualquer erro ou
// sobreposicao dentro do arquivo rejeita tudo e lista as linhas em conflito.
func ParseGroupsCSV(r io.Reader) ([]GroupRow, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 8<<20))
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	rd := csv.NewReader(bytes.NewReader(raw))
	rd.Comma = detectDelimiter(raw)
	rd.Comment = '#'
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true

	var rows []GroupRow
	var errs []string
	first := true
	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line, _ := rd.FieldPos(0)
		if err != nil {
			errs = append(errs, fmt.Sprintf("linha %d: %v", line, err))
			continue
		}
		if first {
			first = false
			if len(rec) == 2 && strings.EqualFold(strings.TrimSpace(rec[0]), "grupo") && strings.EqualFold(strings.TrimSpace(rec[1]), "ramal") {
				continue
			}
		}
		row, err := parseGroupRecord(line, rec)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		rows = append(rows, row)
	}
	// M2: teto antes de qualquer gravacao (o trigger de sobreposicao e O(N) por faixa).
	if len(rows) > MaxImportRows {
		return nil, fmt.Errorf("o arquivo tem %d linhas de dados; o maximo por importacao e %d. Nada foi gravado: divida o arquivo", len(rows), MaxImportRows)
	}
	perGroup := map[string]int{}
	for _, r := range rows {
		if !r.Manual {
			perGroup[r.Name]++
		}
	}
	for name, n := range perGroup {
		if n > MaxGroupRanges {
			return nil, fmt.Errorf("o grupo %q tem %d faixas no arquivo; o maximo e %d por grupo. Nada foi gravado", name, n, MaxGroupRanges)
		}
	}
	errs = append(errs, overlaps(rows)...)
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(limit(errs, 20), "\n"))
	}
	if len(rows) == 0 {
		return nil, errors.New("arquivo sem nenhuma linha de dados")
	}
	return rows, nil
}

// detectDelimiter olha a primeira linha de dados: ';' se ela tem ';', senao ','.
func detectDelimiter(raw []byte) rune {
	for _, ln := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.Contains(t, ";") {
			return ';'
		}
		break
	}
	return ','
}

func parseGroupRecord(line int, rec []string) (GroupRow, error) {
	if len(rec) != 2 {
		return GroupRow{}, fmt.Errorf("linha %d: esperado grupo e ramal (ou faixa), veio %d campo(s)", line, len(rec))
	}
	name := strings.TrimSpace(rec[0])
	noGroup := strings.EqualFold(name, noGroupName) // "Sem grupo,2150": "Sem grupo" manual, so ramal unico
	if noGroup {
		name = noGroupName
	} else if err := ValidGroupName(name); err != nil {
		return GroupRow{}, fmt.Errorf("linha %d: %w", line, err)
	}
	val := strings.TrimSpace(rec[1])
	lo, hi, found := strings.Cut(val, "-")
	start, err := parseNum(lo)
	end := start
	if err == nil && found {
		end, err = parseNum(hi)
	}
	if err != nil {
		return GroupRow{}, fmt.Errorf("linha %d: ramal ou faixa invalido %q (use 2150 ou 2000-2999)", line, rec[1])
	}
	if end < start {
		return GroupRow{}, fmt.Errorf("linha %d: faixa %d-%d com inicio maior que o fim", line, start, end)
	}
	if found && noGroup {
		return GroupRow{}, fmt.Errorf("linha %d: %q aceita so um ramal por linha, nao faixa", line, noGroupName)
	}
	// Ramal unico vira vinculo manual (vence a faixa); faixa N-M continua sendo faixa do grupo.
	return GroupRow{Line: line, Name: name, Start: start, End: end, Manual: !found, Number: val}, nil
}

// badNameRune: a mesma regra do <usuario> do sub issabel: (auth.ValidIssabelSub): controle (Cc,
// inclui C1), formatacao (Cf: bidi, largura zero, ALM, BOM) e separador de linha/paragrafo (Zl, Zp).
func badNameRune(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

func parseNum(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 18 {
		return 0, errors.New("numero invalido")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("numero invalido")
		}
	}
	return strconv.ParseInt(s, 10, 64)
}

// overlaps acha faixas que se cruzam dentro do arquivo (vinculo manual nao e faixa) e ramal
// atribuido a dois grupos diferentes.
func overlaps(rows []GroupRow) []string {
	var sorted []GroupRow
	for _, r := range rows {
		if !r.Manual {
			sorted = append(sorted, r)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	var out []string
	maxEnd := GroupRow{End: -1}
	for _, r := range sorted {
		if r.Start <= maxEnd.End {
			a, b := maxEnd.Line, r.Line
			if a > b {
				a, b = b, a
			}
			out = append(out, fmt.Sprintf("linhas %d e %d: faixas se sobrepoem (%d-%d e %d-%d)", a, b, maxEnd.Start, maxEnd.End, r.Start, r.End))
		}
		if r.End > maxEnd.End {
			maxEnd = r
		}
	}
	first := map[string]GroupRow{}
	for _, r := range rows {
		if !r.Manual {
			continue
		}
		if p, ok := first[r.Number]; !ok {
			first[r.Number] = r
		} else if !strings.EqualFold(p.Name, r.Name) {
			out = append(out, fmt.Sprintf("linhas %d e %d: ramal %s em dois grupos (%q e %q)", p.Line, r.Line, r.Number, p.Name, r.Name))
		}
	}
	return out
}
