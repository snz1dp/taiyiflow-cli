//go:build !windows

package workspaceinstance

import (
	"os"

	"golang.org/x/sys/unix"
)

func acquireFileLock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func releaseFileLock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}