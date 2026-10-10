//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"wxview/internal/app"
)

func lockInit() (func(), error) {
	base, err := app.EnsureBaseDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(base, "init.lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open initialization lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another initialization is running or the initialization lock is unavailable")
	}
	if err := app.ChownForSudo(path); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = f.Close() }, nil
}
