package model

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// r monta uma string a partir de pontos de codigo (o fonte nao carrega controles nem BOM crus).
func r(cps ...rune) string { return string(cps) }

func TestCleanName(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"normal", "Maria Souza", "Maria Souza"},
		{"espacos", "  Maria \t\n Souza  ", "Maria Souza"},
		{"ESC", "Ma" + r(0x1b) + "[31mria", "Ma[31mria"},
		{"bidi override e pop", "Jo" + r(0x202E) + "ao" + r(0x202C), "Joao"},
		{"isolates, largura zero, BOM", "a" + r(0x2066) + "b" + r(0x2069) + "c" + r(0x200B) + "d" + r(0xFEFF) + "e", "abcde"},
		{"C1 e DEL", "x" + r(0x85) + "y" + r(0x7f) + "z", "x yz"},
		{"separador de linha", "linha1" + r(0x2028) + "linha2", "linha1 linha2"},
		{"acentos preservados", "Jos" + r(0xe9) + " " + r(0xc1) + "vila", "Jos" + r(0xe9) + " " + r(0xc1) + "vila"},
		{"so controles", r(0, 1, 2), ""},
		{"html fica (escape e da camada de saida)", "<script>alert(1)</script>", "<script>alert(1)</script>"},
	}
	for _, c := range cases {
		if got := CleanName(c.in); got != c.want {
			t.Errorf("%s: CleanName(%q) = %q, queria %q", c.name, c.in, got, c.want)
		}
	}
	long := CleanName(strings.Repeat(r(0xe9), 200))
	if utf8.RuneCountInString(long) != MaxNameRunes || !utf8.ValidString(long) {
		t.Errorf("limite de caracteres: %d", utf8.RuneCountInString(long))
	}
	if got := CleanName(strings.Repeat("ab ", 100)); utf8.RuneCountInString(got) > MaxNameRunes || strings.HasSuffix(got, " ") {
		t.Errorf("limite com espacos: %q", got)
	}
}
