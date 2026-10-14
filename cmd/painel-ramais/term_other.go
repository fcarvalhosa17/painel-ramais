//go:build !linux

package main

import "errors"

// readPasswordTTY: sem leitura sem eco fora do Linux (alvo do produto); use --password-stdin.
func readPasswordTTY(string) (string, error) {
	return "", errors.New("leitura de senha no terminal só existe no Linux; use --password-stdin")
}
