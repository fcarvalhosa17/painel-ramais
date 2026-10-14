//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// procStats devolve VmRSS em bytes e o tempo de CPU (utime+stime) do processo.
// Assume CLK_TCK = 100, valor padrao do kernel Linux em x86_64.
func procStats(pid int) (rss uint64, cpu time.Duration, err error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				kb, _ := strconv.ParseUint(f[1], 10, 64)
				rss = kb * 1024
			}
		}
	}
	st, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, err
	}
	s := string(st)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+2:]) // depois de "(comm) "
	if len(f) < 13 {
		return rss, 0, fmt.Errorf("/proc/%d/stat inesperado", pid)
	}
	ut, _ := strconv.ParseInt(f[11], 10, 64)
	stt, _ := strconv.ParseInt(f[12], 10, 64)
	return rss, time.Duration(ut+stt) * 10 * time.Millisecond, nil
}
