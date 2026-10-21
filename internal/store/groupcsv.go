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

const noGroupName = "Sem grupo" // valor interno (banco, JSON); o texto para a pessoa e noGroupLabel

// noGroupLabel e como as mensagens chamam o noGroupName.
const noGroupLabel = "Sem visão"

// isNoGroupName: "Sem grupo" (nome interno e CSV antigo) ou "Sem visão" / "Sem visao", sem diferenciar maiusculas.
func isNoGroupName(name string) bool {
	return strings.EqualFold(name, noGroupName) || strings.EqualFold(name, noGroupLabel) || strings.EqualFold(name, "Sem visao")
}

// nameKey e a chave de identidade de um nome de grupo: a mesma do COLLATE NOCASE do SQLite
// (so as letras ASCII dobram), para o arquivo e o banco concordarem sobre "e o mesmo grupo".
func nameKey(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// ParseGroupsCSV le o CSV de grupos (CONTRATOS.md secao 9). Qualquer erro ou sobreposicao de faixas
// do mesmo grupo dentro do arquivo rejeita tudo e lista as linhas em conflito. Linha repetida e
// "Sem grupo,N" voltam marcadas como Skip (contam como lidas e saem como ignoradas).
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
	var cols []int // colunas [visao, ramal] do cabecalho; nil = sem cabecalho (duas colunas, nessa ordem)
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
			if c, ok := csvHeader(rec); ok {
				cols = c
				continue
			}
		}
		if cols != nil && len(rec) > max(cols[0], cols[1]) {
			if extra := extraCell(rec, cols); extra {
				errs = append(errs, fmt.Sprintf("linha %d: coluna a mais com conteúdo; use só visão e ramal (ou faixa)", line))
				continue
			}
			rec = []string{rec[cols[0]], rec[cols[1]]}
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
	markRepeated(rows)
	perGroup := map[string]int{}
	for _, r := range rows {
		if !r.Manual && !r.Skip {
			k := nameKey(r.Name)
			if perGroup[k]++; perGroup[k] > MaxGroupRanges {
				return nil, fmt.Errorf("a visão %q tem mais de %d faixas no arquivo; o máximo é %d por visão. Nada foi gravado", r.Name, MaxGroupRanges, MaxGroupRanges)
			}
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

// csvHeader reconhece o cabecalho: uma coluna "ramal" e uma coluna da visao, que se chama "visao"
// (ou "visão") no arquivo novo e "grupo" no antigo; com as duas, vale "visao". Devolve [visao, ramal].
func csvHeader(rec []string) ([]int, bool) {
	visao, grupo, ramal := -1, -1, -1
	for i, c := range rec {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "visao", "visão":
			visao = i
		case "grupo":
			grupo = i
		case "ramal":
			ramal = i
		}
	}
	if visao < 0 {
		visao = grupo
	}
	if visao < 0 || ramal < 0 {
		return nil, false
	}
	return []int{visao, ramal}, true
}

// extraCell diz se alguma coluna fora das mapeadas (visao, ramal) tem conteudo; vazia (";" final do Excel) passa.
func extraCell(rec []string, cols []int) bool {
	for i, c := range rec {
		if i != cols[0] && i != cols[1] && strings.TrimSpace(c) != "" {
			return true
		}
	}
	return false
}

// markRepeated marca como Skip a linha repetida: mesmo grupo (sem diferenciar maiusculas) e mesmo
// valor (o mesmo ramal ou a mesma faixa) de uma linha anterior. Fica ignorada, sem erro.
func markRepeated(rows []GroupRow) {
	seen := make(map[string]struct{}, len(rows))
	for i := range rows {
		r := &rows[i]
		if r.Skip {
			continue
		}
		key := nameKey(r.Name) + "\x00"
		if r.Manual {
			key += "m" + r.Number
		} else {
			key += fmt.Sprintf("r%d-%d", r.Start, r.End)
		}
		if _, dup := seen[key]; dup {
			r.Skip = true
			continue
		}
		seen[key] = struct{}{}
	}
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
		return GroupRow{}, fmt.Errorf("linha %d: esperado visão e ramal (ou faixa), veio %d campo(s)", line, len(rec))
	}
	name := strings.TrimSpace(rec[0])
	noGroup := isNoGroupName(name) // "Sem grupo,2150" ou "Sem visão,2150": aceito e ignorado (arquivos antigos); faixa e erro
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
		return GroupRow{}, fmt.Errorf("linha %d: %q aceita so um ramal por linha, nao faixa", line, noGroupLabel)
	}
	// Ramal unico vira vinculo manual (soma a faixa, ADR-006); faixa N-M continua sendo faixa do grupo.
	// "Sem grupo,N" nao tem vinculo: a linha conta como lida e sai como ignorada.
	return GroupRow{Line: line, Name: name, Start: start, End: end, Manual: !found, Number: val, Skip: noGroup}, nil
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

// overlaps acha faixas que se cruzam DENTRO DO MESMO GRUPO no arquivo (sem diferenciar maiusculas
// no nome). Faixas de grupos diferentes podem se sobrepor (ADR-006); faixa identica ja foi marcada
// como repetida e nao entra; vinculo manual nao e faixa.
func overlaps(rows []GroupRow) []string {
	byGroup := map[string][]GroupRow{}
	var order []string
	for _, r := range rows {
		if r.Manual || r.Skip {
			continue
		}
		k := nameKey(r.Name)
		if _, ok := byGroup[k]; !ok {
			order = append(order, k)
		}
		byGroup[k] = append(byGroup[k], r)
	}
	var out []string
	for _, k := range order {
		sorted := byGroup[k]
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
		maxEnd := GroupRow{End: -1}
		for _, r := range sorted {
			if r.Start <= maxEnd.End {
				a, b := maxEnd.Line, r.Line
				if a > b {
					a, b = b, a
				}
				out = append(out, fmt.Sprintf("linhas %d e %d: faixas %d-%d e %d-%d da visão %q se sobrepoem", a, b, maxEnd.Start, maxEnd.End, r.Start, r.End, sorted[0].Name))
			}
			if r.End > maxEnd.End {
				maxEnd = r
			}
		}
	}
	return out
}
