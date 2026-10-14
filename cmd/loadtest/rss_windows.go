//go:build windows

package main

import (
	"errors"
	"syscall"
	"time"
	"unsafe"
)

var (
	psapi                = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemory = psapi.NewProc("GetProcessMemoryInfo")
)

type procMemCounters struct {
	cb                         uint32
	pageFaults                 uint32
	peakWorkingSet, workingSet uintptr
	qPeakPaged, qPaged         uintptr
	qPeakNonPaged, qNonPaged   uintptr
	pagefile, peakPagefile     uintptr
}

// procStats devolve o working set (RSS) em bytes e o tempo de CPU (usuario+sistema) do processo.
func procStats(pid int) (rss uint64, cpu time.Duration, err error) {
	h, err := syscall.OpenProcess(0x0400|0x0010, false, uint32(pid)) // QUERY_INFORMATION | VM_READ
	if err != nil {
		return 0, 0, err
	}
	defer syscall.CloseHandle(h)
	var m procMemCounters
	m.cb = uint32(unsafe.Sizeof(m))
	if r, _, e := procGetProcessMemory.Call(uintptr(h), uintptr(unsafe.Pointer(&m)), uintptr(m.cb)); r == 0 {
		return 0, 0, errors.New("GetProcessMemoryInfo: " + e.Error())
	}
	var c, x, k, u syscall.Filetime
	if err := syscall.GetProcessTimes(h, &c, &x, &k, &u); err != nil {
		return 0, 0, err
	}
	ft := func(f syscall.Filetime) time.Duration {
		return time.Duration(int64(f.HighDateTime)<<32|int64(f.LowDateTime)) * 100 // unidades de 100 ns
	}
	return uint64(m.workingSet), ft(k) + ft(u), nil
}
