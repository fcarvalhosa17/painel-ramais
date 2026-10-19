//go:build !debug

package main

import (
	"context"
	"log/slog"
)

// startDebug nao faz nada no binario de release: o endpoint de heap/pprof (debug.go) so
// entra com `-tags debug`, para medir o heap numa VM. Nenhuma variavel de ambiente o liga aqui.
func startDebug(context.Context, *slog.Logger) {}
