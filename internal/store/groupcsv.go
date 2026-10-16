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
	"unicode/utf8"
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
	if n := utf8.RuneCountInString(name); n < 1 || n > 64 {
		return GroupRow{}, fmt.Errorf("linha %d: nome do grupo deve ter de 1 a 64 caracteres", line)
	}
	if strings.ContainsFunc(name, badNameRune) {
		return GroupRow{}, fmt.Errorf("linha %d: nome do grupo com caractere de controle ou de formatacao invisivel", line)
	}
	if strings.EqualFold(name, noGroupName) {
		return GroupRow{}, fmt.Errorf("linha %d: %q e nome reservado", line, noGroupName)
	}
	lo, hi, found := strings.Cut(strings.TrimSpace(rec[1]), "-")
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
	return GroupRow{Line: line, Name: name, Start: start, End: end}, nil
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

func overlaps(rows []GroupRow) []string {
	sorted := append([]GroupRow(nil), rows...)
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
	return out
}
