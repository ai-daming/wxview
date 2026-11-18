package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"wxview/internal/app"
)

// confirmStartAction asks the user at a real terminal to approve a background
// start. It is a package-level variable so tests can stub the gate.
var confirmStartAction = defaultConfirmStartAction

// defaultConfirmStartAction rejects non-interactive callers outright and asks
// a y/N question when stdin is a terminal. Only an explicit y/yes approves;
// the default is deny.
func defaultConfirmStartAction(action string) error {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%s requires interactive confirmation; run it from your own terminal", action)
	}
	return confirmFrom(os.Stdin, os.Stdout, action)
}

func confirmFrom(r io.Reader, w io.Writer, action string) error {
	fmt.Fprintf(w, "%s? [y/N]: ", action)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(w)
		return fmt.Errorf("%s confirmation denied", action)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return fmt.Errorf("%s confirmation denied", action)
}

// issueSpawnToken writes a fresh one-time token file that authorizes the
// daemon foreground child spawned by `wxview daemon start` after the user has
// confirmed interactively.
func issueSpawnToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	path, err := app.DaemonSpawnTokenPath()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", err
	}
	if err := app.ChownForSudo(path); err != nil {
		return "", err
	}
	return token, nil
}

// consumeSpawnToken validates and burns the one-time spawn token. The token
// file is removed whether validation passes or not, so a token can only be
// used once.
func consumeSpawnToken(presented string) error {
	path, err := app.DaemonSpawnTokenPath()
	if err != nil {
		return err
	}
	data, readErr := os.ReadFile(path)
	if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
		return fmt.Errorf("remove spawn token file: %w", removeErr)
	}
	if readErr != nil {
		return fmt.Errorf("spawn token file missing or unreadable")
	}
	if presented == "" {
		return fmt.Errorf("spawn token not provided")
	}
	if strings.TrimSpace(string(data)) != presented {
		return fmt.Errorf("spawn token mismatch")
	}
	return nil
}
