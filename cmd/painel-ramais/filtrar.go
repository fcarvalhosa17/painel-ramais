package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"painel-ramais/internal/ami"
	"painel-ramais/internal/capture"
)

// filtrar le texto do stdin e escreve no stdout com as MESMAS regras de limpeza de `capturar`
// (internal/capture): segredos viram *** sempre; com --anonimizar, numeros, nomes, hosts e IPs
// viram valores ficticios, iguais para o mesmo original durante toda a execucao. E o filtro que o
// `instalar.py homologar` aplica a todo o pacote (nao existe segunda rotina de mascara).
func (c *cli) filtrar(args []string) int {
	fs := flag.NewFlagSet("filtrar", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	fs.Usage = func() { fmt.Fprint(c.stderr, filtrarHelp) }
	var hosts hostList
	fs.Var(&hosts, "host", "nome de maquina (hostname/FQDN) a trocar por host-NNN.invalid em qualquer ponto do texto; repetivel; so com --anonimizar")
	anon := fs.Bool("anonimizar", false, "troca numeros externos, nomes e IPs por valores ficticios (LGPD)")
	digits := fs.Int("digitos-externos", 7, "tamanho minimo de um numero externo ao anonimizar")
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil || len(pos) != 0 || *digits < 3 {
		return c.usage()
	}
	san := capture.NewSanitizer(capture.SanitizeOptions{Anonymize: *anon, ExternalDigits: *digits})
	for _, h := range hosts {
		san.AddHost(h)
	}
	in := bufio.NewReaderSize(c.stdin, 1<<20)
	out := bufio.NewWriterSize(c.stdout, 1<<20)
	for {
		line, rerr := in.ReadBytes('\n') // linha inteira: o contexto de uma chave nunca atravessa linhas
		if len(line) > 0 {
			if _, err := out.Write(san.Text(line)); err != nil {
				c.errf("filtrar: %v", err)
				return exitEnv
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			c.errf("filtrar: %v", rerr)
			return exitEnv
		}
	}
	if err := out.Flush(); err != nil {
		c.errf("filtrar: %v", err)
		return exitEnv
	}
	return exitOK
}

// actions imprime a lista fixa de actions AMI do daemon (a mesma allowlist do cliente AMI), uma por linha.
func (c *cli) actions(args []string) int {
	if len(args) != 0 {
		return c.usage()
	}
	for _, a := range ami.AllowedActions() {
		fmt.Fprintln(c.stdout, a)
	}
	return exitOK
}

// hostList e o flag --host repetivel.
type hostList []string

func (h *hostList) String() string     { return strings.Join(*h, ",") }
func (h *hostList) Set(v string) error { *h = append(*h, v); return nil }

const filtrarHelp = `uso: painel-ramais filtrar [--anonimizar] [--digitos-externos 7] [--host NOME]...
Le texto do stdin e escreve no stdout linha a linha com as regras do capturar (internal/capture):
  - sempre: valor de chave secreta (secret, password, passphrase, pass, pwd, senha, token, *key, auth, cookie, *session,
    hmac, signature, credential, *hash, md5, challenge) vira ***: com aspas (duplas ou simples) ate a aspa que fecha, sem
    aspas ate o FIM DA LINHA (o resto da linha tambem some); tambem dentro de valor entre aspas, de URL com query
    (?session=X) e de repr de Python; cabecalho Cookie/Set-Cookie/Authorization vira *** na linha toda; senha de
    usuario:senha@host (ancorada no ultimo "@") e response/nonce de Digest;
  - com --anonimizar: numeros externos, nomes, usuarios, hosts, IPv4 e IPv6 viram ficticios (o mesmo original, o mesmo ficticio);
  - --host NOME: o hostname/FQDN da maquina, trocado em qualquer ponto do texto (repetivel).
NAO cobre: segredo que ocupa varias linhas (chave PEM), segredo em argv/ps fora de par chave=valor, texto livre sem chave
(nome de pessoa, numero curto). Revise o resultado antes de compartilhar.
`
