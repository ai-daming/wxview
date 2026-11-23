package key

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsScanSessionReusesMaterialAcrossDatabases(t *testing.T) {
	first, page := memoryPage(t)
	root := t.TempDir()
	second := captureFixture(t, root, "message/message_0.db", 19)
	secondPage, err := os.ReadFile(filepath.Join(root, "message/message_0.db"))
	if err != nil {
		t.Fatal(err)
	}
	regions, read, data := windowsMemoryFixture(t, "x'"+first.Key+first.Salt+"'", bytes.Repeat([]byte{0xa5}, 32))
	copy(data[0x1000:], "x'"+second.Key+second.Salt+"'")
	session := newWindowsScanSession(context.Background())
	collect := func(ctx context.Context, budget *memoryReadBudget) (windowsKeyMaterial, error) {
		return collectWindowsKeyMaterial(ctx, regions, read, budget)
	}
	got, err := session.key(context.Background(), 123, first.Salt, page, collect)
	if err != nil || got != first.Key {
		t.Fatal("first database key failed")
	}
	// A process may disappear after capture. Cached material must still be
	// authenticated against each next database, rather than re-reading it.
	noProcess := func(context.Context, *memoryReadBudget) (windowsKeyMaterial, error) {
		return windowsKeyMaterial{}, errors.New("process has exited")
	}
	got, err = session.key(context.Background(), 123, second.Salt, secondPage, noProcess)
	if err != nil || got != second.Key {
		t.Fatal("second database did not reuse authenticated material")
	}
	got, err = session.key(context.Background(), 456, first.Salt, page, noProcess)
	if got != "" || err == nil {
		t.Fatal("material from another PID was reused")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := session.key(ctx, 123, first.Salt, page, collect); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("cached result bypassed cancellation")
	}
	bad := append([]byte(nil), page...)
	bad[500] ^= 1
	if got, err := session.key(context.Background(), 123, first.Salt, bad, noProcess); got != "" || !errors.Is(err, errKeyVerification) {
		t.Fatal("cached material bypassed per-database HMAC")
	}
}

func TestWindowsScanSessionSharesBudgetAcrossProcesses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := newWindowsScanSession(ctx)
	session.budget.remaining = 3
	collect := func(ctx context.Context, budget *memoryReadBudget) (windowsKeyMaterial, error) {
		_, err := budget.read(ctx, func(uint64, int) ([]byte, error) { return []byte("ok"), nil }, 0x10000, 2)
		return windowsKeyMaterial{}, err
	}
	_, _ = session.key(ctx, 1, "", nil, collect)
	if _, err := session.key(ctx, 2, "", nil, collect); !errors.Is(err, errMemoryBudget) {
		t.Fatal("new PID received a fresh scan budget")
	}
	cancel()
	if _, err := session.key(ctx, 3, "", nil, collect); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled session continued")
	}
}
