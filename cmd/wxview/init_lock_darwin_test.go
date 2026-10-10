//go:build darwin

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInitLockExcludesScanAndCapture(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	unlock, err := lockInit()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	for _, args := range [][]string{{}, {"--key-source", "lldb"}} {
		if err := runInit(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Fatal("concurrent init accepted")
		}
	}
	unlock()
	again, err := lockInit()
	if err != nil {
		t.Fatal("lock not released", err)
	}
	again()
}

func TestInitHelpAndInvalidFlagsDoNotCreateState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	if err := runInit(context.Background(), []string{"--help"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--key-source", "other"}, {"--db-root", "/a"}} {
		if err := runInit(context.Background(), args, &out); err == nil {
			t.Fatal("invalid flags accepted")
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".wxview")); !os.IsNotExist(err) {
		t.Fatal("help/invalid flags created state")
	}
}
