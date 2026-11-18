package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfirmFromApprovesExplicitYes(t *testing.T) {
	for _, input := range []string{"y\n", "Y\n", "yes\n", "  YES  \n", "y"} {
		var out bytes.Buffer
		if err := confirmFrom(strings.NewReader(input), &out, "Start test thing"); err != nil {
			t.Fatalf("input %q should approve: %v", input, err)
		}
		if !strings.Contains(out.String(), "[y/N]") {
			t.Fatalf("prompt missing [y/N]: %q", out.String())
		}
	}
}

func TestConfirmFromDeniesEverythingElse(t *testing.T) {
	for _, input := range []string{"\n", "n\n", "no\n", "maybe\n", "  \n", ""} {
		var out bytes.Buffer
		err := confirmFrom(strings.NewReader(input), &out, "Start test thing")
		if err == nil {
			t.Fatalf("input %q should deny", input)
		}
		if !strings.Contains(err.Error(), "confirmation denied") {
			t.Fatalf("unexpected error for %q: %v", input, err)
		}
	}
}

func stubConfirm(err error) (restore func()) {
	orig := confirmStartAction
	confirmStartAction = func(string) error { return err }
	return func() { confirmStartAction = orig }
}

func TestDaemonStartDeniedByGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	restore := stubConfirm(fmt.Errorf("stubbed denial"))
	defer restore()
	var out bytes.Buffer
	err := run([]string{"daemon", "start"}, &out, &out)
	if err == nil {
		t.Fatal("expected daemon start to be denied by the confirmation gate")
	}
	if !strings.Contains(err.Error(), "stubbed denial") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "daemon socket") {
		t.Fatalf("expected socket line before the gate: %q", out.String())
	}
}

func TestAPIStartDeniedByGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	restore := stubConfirm(fmt.Errorf("stubbed denial"))
	defer restore()
	var out bytes.Buffer
	err := run([]string{"api", "start"}, &out, &out)
	if err == nil {
		t.Fatal("expected api start to be denied by the confirmation gate")
	}
	if !strings.Contains(err.Error(), "stubbed denial") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".wxview", "api.json")); statErr == nil {
		t.Fatal("api state must not be written when the gate denies")
	}
}

func TestDaemonForegroundWithoutTokenRejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(daemonForegroundEnv, "1")
	t.Setenv(daemonSpawnTokenEnv, "")
	var out bytes.Buffer
	err := run([]string{"daemon"}, &out, &out)
	if err == nil {
		t.Fatal("expected foreground daemon without a spawn token to be rejected")
	}
	if !strings.Contains(err.Error(), "spawn token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSpawnTokenRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	token, err := issueSpawnToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("expected a non-empty token")
	}
	path := filepath.Join(home, ".wxview", "daemon-spawn-token")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != token {
		t.Fatalf("token file content mismatch: %q vs %q", data, token)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("token file perm = %o, want 600", perm)
		}
	}
	if err := consumeSpawnToken(token); err != nil {
		t.Fatalf("consume should accept the issued token: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file should be consumed: %v", err)
	}
}

func TestSpawnTokenRejectsForgersAndReplays(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	token, err := issueSpawnToken()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".wxview", "daemon-spawn-token")
	if err := consumeSpawnToken("forged"); err == nil {
		t.Fatal("expected a mismatched token to be rejected")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file should be consumed even on mismatch: %v", err)
	}
	if err := consumeSpawnToken(token); err == nil {
		t.Fatal("replayed token must be rejected after the file was burned")
	}
}

func TestStartCommandsDocumentConfirmationGate(t *testing.T) {
	var daemonOut bytes.Buffer
	if err := run([]string{"daemon", "--help"}, &daemonOut, &daemonOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(daemonOut.String(), "requires interactive terminal confirmation") {
		t.Fatalf("daemon help should document the confirmation gate:\n%s", daemonOut.String())
	}
	var apiOut bytes.Buffer
	if err := run([]string{"api", "start", "--help"}, &apiOut, &apiOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(apiOut.String(), "requires interactive terminal confirmation") {
		t.Fatalf("api start help should document the confirmation gate:\n%s", apiOut.String())
	}
}
