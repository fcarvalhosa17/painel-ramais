// Comando painel-ramais: daemon coletor (serve) e CLI de administracao.
package main

import (
	"fmt"
	"os"

	"painel-ramais/internal/store"
)

// version e trocada no build: -ldflags "-X main.version=1.2.3".
var version = "0.1.1"

const defaultConfig = "/etc/painel-ramais/config.yaml"

// Codigos de saida da CLI (CONTRATOS.md secao 8).
const (
	exitOK      = 0
	exitUsage   = 1 // uso ou validacao
	exitEnv     = 2 // banco, arquivo, permissao
	exitExists  = 3 // user add de usuario que ja existe
	exitVersion = 4 // serve: Asterisk abaixo do minimo; a unit nao deve reiniciar (RestartPreventExitStatus=4)
)

func main() {
	c := &cli{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, euid: os.Geteuid, notify: notifyDaemon, readTTY: readPasswordTTY}
	os.Exit(c.run(os.Args[1:]))
}

// run despacha o subcomando e devolve o codigo de saida.
func (c *cli) run(args []string) int {
	if len(args) == 0 {
		return c.usage()
	}
	cmd, rest := args[0], args[1:]
	if cmd == "version" {
		fmt.Fprintf(c.stdout, "painel-ramais %s schema %d\n", version, store.SchemaVersion)
		return exitOK
	}
	if c.euid() == 0 {
		c.errf("não rode como root; use: sudo -u painel-ramais painel-ramais %s", cmd)
		return exitUsage
	}
	switch cmd {
	case "serve":
		return c.serve(rest)
	case "user":
		return c.user(rest)
	case "group":
		return c.group(rest)
	case "capturar":
		return c.capturar(rest)
	case "filtrar":
		return c.filtrar(rest)
	case "actions":
		return c.actions(rest)
	case "session":
		return c.session(rest)
	case "token":
		return c.token(rest)
	}
	return c.usage()
}

func (c *cli) usage() int {
	fmt.Fprint(c.stderr, `uso:
  painel-ramais serve [--config ARQ]
  painel-ramais user add <usuario> [--password-stdin]
  painel-ramais user del <usuario>
  painel-ramais user passwd <usuario> [--password-stdin]
  painel-ramais group import [--replace] <arquivo.csv | ->   (- le o CSV do stdin)
  painel-ramais group list
  painel-ramais group del <grupo>
  painel-ramais session purge [--all]
  painel-ramais capturar --minutos N --saida ARQ|- [--anonimizar] [--digitos-externos 7]
  painel-ramais filtrar [--anonimizar] [--digitos-externos 7]   (stdin -> stdout; mascara segredos com as regras do capturar)
  painel-ramais actions                                          (lista fixa de actions AMI, uma por linha)
  painel-ramais token --sub <texto> [--ttl 60s]
  painel-ramais version
`)
	return exitUsage
}
