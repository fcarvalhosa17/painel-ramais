// Package web embute a tela (HTML, CSS e JavaScript de internal/web/static) no binario:
// um RPM, um binario, e tela e daemon nunca ficam em versoes diferentes (ADR-004 item 8).
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var content embed.FS

// Static devolve a raiz da tela: index.html, acesso.html e assets/.
func Static() fs.FS {
	sub, err := fs.Sub(content, "static")
	if err != nil { // so com caminho invalido, ou seja, erro de programacao
		panic(err)
	}
	return sub
}
