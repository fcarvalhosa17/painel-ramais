package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// notifyDaemon manda SIGHUP ao daemon depois de uma alteracao no banco. O banco
// ja foi gravado: qualquer falha aqui so avisa e a CLI sai com 0.
func notifyDaemon(stderr io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", "-p", "MainPID", "--value", "painel-ramais").Output()
	pid, perr := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || perr != nil || pid <= 0 {
		fmt.Fprintln(stderr, "painel-ramais: aviso: daemon parado (ou systemctl indisponível); a alteração vale no próximo start")
		return
	}
	p, err := os.FindProcess(pid)
	if err == nil {
		err = p.Signal(syscall.SIGHUP)
	}
	if err != nil {
		fmt.Fprintln(stderr, "painel-ramais: aviso: não consegui avisar o daemon; rode: systemctl reload painel-ramais")
	}
}
