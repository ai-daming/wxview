//go:build windows

package main

import (
	"os"
	"syscall"
)

func daemonSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func daemonSysProcAttr() *syscall.SysProcAttr {
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}

func terminateProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
