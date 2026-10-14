package ami

import (
	"errors"
	"testing"
)

func TestCheckVersion(t *testing.T) {
	cases := []struct {
		v    string
		fail bool
	}{
		{"18.20.0", false}, {"13.0.0", false}, {"certified/13.21-cert5", false}, {"20.5.2", false},
		{"11.25.3", true}, {"1.8.32.3", true}, {"12.8.2", true}, {"GIT-master", true},
	}
	for _, c := range cases {
		err := CheckVersion(c.v)
		if (err != nil) != c.fail {
			t.Errorf("%q: err=%v, fail esperado=%v", c.v, err, c.fail)
		}
	}
	if err := CheckVersion("11.25.3"); !errors.Is(err, ErrVersionTooOld) {
		t.Errorf("11.x deve ser ErrVersionTooOld, veio %v", err)
	}
}

func TestResolveTechnology(t *testing.T) {
	counts := func(pj, sp int) func(string) (int, error) {
		return func(tech string) (int, error) {
			if tech == "pjsip" {
				return pj, nil
			}
			return sp, nil
		}
	}
	cases := []struct {
		name       string
		configured string
		pj, sp     bool
		count      func(string) (int, error)
		want       string
		warn, fail bool
	}{
		{"auto só pjsip", "auto", true, false, nil, "pjsip", false, false},
		{"auto só sip", "auto", false, true, nil, "sip", false, false},
		{"auto ambos, sip tem ramais", "auto", true, true, counts(0, 120), "sip", true, false},
		{"auto ambos, pjsip tem ramais", "auto", true, true, counts(300, 2), "pjsip", true, false},
		{"auto ambos, empate vai para pjsip", "auto", true, true, counts(0, 0), "pjsip", true, false},
		{"auto nenhum", "auto", false, false, nil, "", false, true},
		{"vazio equivale a auto", "", true, false, nil, "pjsip", false, false},
		{"sip forçado e carregado", "sip", true, true, nil, "sip", false, false},
		{"pjsip forçado sem módulo", "pjsip", false, true, nil, "", false, true},
		{"valor inválido", "iax", true, true, nil, "", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := ResolveTechnology(c.configured, c.pj, c.sp, c.count)
			if (err != nil) != c.fail {
				t.Fatalf("err=%v", err)
			}
			if d.Technology != c.want || (d.Warning != "") != c.warn {
				t.Fatalf("got %+v", d)
			}
		})
	}
	if _, err := ResolveTechnology("auto", false, false, nil); !errors.Is(err, ErrNoTechnology) {
		t.Fatalf("nenhum módulo deve dar ErrNoTechnology, veio %v", err)
	}
}

func TestEnsureSameTechnology(t *testing.T) {
	cases := []struct {
		prev, cur string
		fail      bool
	}{
		{"", "pjsip", false}, {"pjsip", "pjsip", false}, {"sip", "sip", false},
		{"pjsip", "sip", true}, {"sip", "pjsip", true},
	}
	for _, c := range cases {
		err := EnsureSameTechnology(c.prev, c.cur)
		if (err != nil) != c.fail || (c.fail && !errors.Is(err, ErrTechnologyChanged)) {
			t.Errorf("%q->%q: err=%v", c.prev, c.cur, err)
		}
	}
}
