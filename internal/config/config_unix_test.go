package config

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const goodKey = "ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8=\n"

// Permissao da chave contra o sistema de arquivos real (roda no Rocky/CI, nao no Windows).
func TestIssabelKeyPermOnDisk(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "chave")
	c := Defaults()
	c.Auth.IssabelKeyFile = path
	if err := os.WriteFile(path, []byte(goodKey), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o640, 0o600, 0o440, 0o400} {
		os.Chmod(path, mode)
		if k, err := c.IssabelKey(); err != nil || len(k) != 32 {
			t.Errorf("%04o deveria valer: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o644, 0o660, 0o604, 0o666} {
		os.Chmod(path, mode)
		if _, err := c.IssabelKey(); !errors.Is(err, ErrKeyInvalid) {
			t.Errorf("%04o deveria ser recusado: %v", mode, err)
		}
	}
	// Symlink para um arquivo 0640 perfeito continua recusado (Lstat).
	real := filepath.Join(d, "real")
	os.WriteFile(real, []byte(goodKey), 0o640)
	link := filepath.Join(d, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("sem symlink: %v", err)
	}
	c.Auth.IssabelKeyFile = link
	if _, err := c.IssabelKey(); !errors.Is(err, ErrKeyInvalid) {
		t.Errorf("symlink deveria ser recusado: %v", err)
	}
}

// Um FIFO no lugar da chave nao pode travar a verificacao (open bloqueante): e recusado na hora.
func TestIssabelKeyFIFORefusedWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o640); err != nil {
		t.Skipf("sem mkfifo: %v", err)
	}
	c := Defaults()
	c.Auth.IssabelKeyFile = path
	done := make(chan error, 1)
	go func() { _, err := c.IssabelKey(); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrKeyInvalid) {
			t.Errorf("FIFO deveria ser recusado: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("IssabelKey travou num FIFO")
	}
}

// Arquivo enorme no lugar da chave: le so o teto e recusa o conteudo (base64 invalido/curto), sem estourar memoria.
func TestIssabelKeyHugeFileIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grande")
	big := make([]byte, 5<<20)
	for i := range big {
		big[i] = 'A'
	}
	if err := os.WriteFile(path, big, 0o640); err != nil {
		t.Fatal(err)
	}
	c := Defaults()
	c.Auth.IssabelKeyFile = path
	if k, err := c.IssabelKey(); err != nil && !errors.Is(err, ErrKeyInvalid) || (err == nil && len(k) > maxKeyFileBytes) {
		t.Errorf("len=%d err=%v", len(k), err)
	}
}
