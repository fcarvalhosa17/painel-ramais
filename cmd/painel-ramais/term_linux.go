//go:build linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// readPasswordTTY le uma linha do terminal sem eco (ioctl direto: x/term nao
// esta na lista fechada de dependencias).
func readPasswordTTY(prompt string) (string, error) {
	fd := os.Stdin.Fd()
	var old syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&old))); e != 0 {
		return "", errors.New("stdin não é um terminal; use --password-stdin")
	}
	quiet := old
	quiet.Lflag &^= syscall.ECHO
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&quiet))); e != 0 {
		return "", fmt.Errorf("desligar eco do terminal: %v", e)
	}
	defer syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&old)))
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprintln(os.Stderr)
	if err != nil && line == "" {
		return "", errors.New("senha não informada")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
