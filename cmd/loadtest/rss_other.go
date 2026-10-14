//go:build !windows && !linux

package main

import (
	"errors"
	"time"
)

func procStats(pid int) (uint64, time.Duration, error) {
	return 0, 0, errors.New("medicao de RSS/CPU so em Windows e Linux")
}
