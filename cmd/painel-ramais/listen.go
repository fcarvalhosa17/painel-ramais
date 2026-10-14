package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

// sdListenFdsStart e o primeiro descritor entregue pelo systemd (SD_LISTEN_FDS_START).
const sdListenFdsStart = 3

// activation isola o que socket activation precisa do sistema (trocavel nos testes).
type activation struct {
	getenv       func(string) string
	unsetenv     func(string) error
	pid          int
	newFile      func(fd uintptr, name string) *os.File
	fileListener func(*os.File) (net.Listener, error)
}

func systemActivation() activation {
	return activation{getenv: os.Getenv, unsetenv: os.Unsetenv, pid: os.Getpid(), newFile: os.NewFile, fileListener: net.FileListener}
}

// openListener usa o socket do systemd (LISTEN_PID/LISTEN_FDS, protocolo sd_listen_fds)
// se houver; fora do systemd abre addr. activated diz qual dos dois aconteceu.
// O socket herdado tem de ser TCP em loopback: unit mal configurada nao expoe a porta.
func openListener(addr string, a activation) (ln net.Listener, activated bool, err error) {
	n, err := listenFDs(a)
	if err != nil {
		return nil, false, err
	}
	if n == 0 {
		ln, err = net.Listen("tcp", addr)
		return ln, false, err
	}
	f := a.newFile(sdListenFdsStart, "LISTEN_FD_3")
	if f == nil {
		return nil, true, fmt.Errorf("socket activation: descritor %d invalido", sdListenFdsStart)
	}
	defer f.Close() // o listener guarda a propria copia
	ln, err = a.fileListener(f)
	if err != nil {
		return nil, true, fmt.Errorf("socket activation: %w", err)
	}
	if err := requireLoopbackTCP(ln); err != nil {
		ln.Close()
		return nil, true, err
	}
	return ln, true, nil
}

// listenFDs devolve quantos descritores o systemd entregou a ESTE processo (0 = nenhum).
func listenFDs(a activation) (int, error) {
	pidS, fdsS := a.getenv("LISTEN_PID"), a.getenv("LISTEN_FDS")
	if pidS == "" && fdsS == "" {
		return 0, nil
	}
	defer func() { // como sd_listen_fds(unset_environment=1): filhos nao herdam
		for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
			a.unsetenv(k)
		}
	}()
	pid, err := strconv.Atoi(pidS)
	if err != nil || pid != a.pid {
		return 0, nil // variaveis herdadas de outro processo: ignorar
	}
	n, err := strconv.Atoi(fdsS)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("socket activation: LISTEN_FDS invalido %q", fdsS)
	}
	if n > 1 {
		return 0, fmt.Errorf("socket activation: %d sockets entregues, o painel espera exatamente 1 (painel-ramais.socket)", n)
	}
	return n, nil
}

func requireLoopbackTCP(ln net.Listener) error {
	ta, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !ta.IP.IsLoopback() {
		return fmt.Errorf("socket activation: %v nao e TCP em loopback; revise ListenStream em painel-ramais.socket", ln.Addr())
	}
	return nil
}
