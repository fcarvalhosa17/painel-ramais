package amitest

import (
	"fmt"
	"reflect"
	"strings"
)

// Render descreve um evento em uma linha estável para comparação em teste:
// "Tipo{Campo=valor ...}", sem At, sem campos zerados, ponteiros desreferenciados.
func Render(ev any) string {
	v := reflect.ValueOf(ev)
	t := v.Type()
	var parts []string
	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		if t.Field(i).Name == "At" {
			continue
		}
		if f.Kind() == reflect.Pointer {
			if f.IsNil() {
				continue
			}
			f = f.Elem()
		}
		if f.IsZero() {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", t.Field(i).Name, f.Interface()))
	}
	return t.Name() + "{" + strings.Join(parts, " ") + "}"
}
