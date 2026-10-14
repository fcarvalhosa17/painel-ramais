//go:build !windows

package config

import (
	"fmt"
	"os"
	"syscall"
)

// checkSecretPerm exige modo 0600 e dono igual ao usuario do servico.
func checkSecretPerm(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm() != 0o600 {
		return fmt.Errorf("permissao %04o, precisa ser 0600", fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("dono uid %d, precisa ser o usuario do servico (uid %d)", st.Uid, os.Geteuid())
	}
	return nil
}
