package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// maxKeyFileBytes: a chave tem dezenas de bytes; o teto evita ler um arquivo enorme trocado no lugar.
const maxKeyFileBytes = 4096

// readIssabelKeyFile abre a chave sem seguir symlink (O_NOFOLLOW) e sem bloquear em FIFO
// (O_NONBLOCK), confere tipo, modo e dono no MESMO descritor (fstat) e le dele: nao ha janela
// entre a checagem e a leitura para trocar o arquivo. Ausente devolve o erro do open
// (fs.ErrNotExist); regra violada devolve ErrKeyInvalid.
func readIssabelKeyFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK) { // symlink no ultimo componente
			return nil, fmt.Errorf("%w: %s", ErrKeyInvalid, keyModeProblem(fs.ModeSymlink, 0, 0))
		}
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	uid := os.Geteuid()
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		uid = int(st.Uid)
	}
	if p := keyModeProblem(fi.Mode(), uid, os.Geteuid()); p != "" {
		return nil, fmt.Errorf("%w: %s", ErrKeyInvalid, p)
	}
	return io.ReadAll(io.LimitReader(f, maxKeyFileBytes))
}
