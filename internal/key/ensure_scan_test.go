package key

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wxview/internal/contacts"
)

// Encrypt the independently generated real SQLite fixture, which reserves
// 80 bytes per page and contains one invented contact, never user data.
func runtimeQueryFixture(t *testing.T, root string) capturedKey {
	t.Helper()
	encoded, err := os.ReadFile("testdata/windows_contact.b64")
	if err != nil {
		t.Fatal(err)
	}
	zipped, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(zipped))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil {
		t.Fatal(err)
	}
	key, salt := bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 16)
	macSalt := bytes.Repeat([]byte{8 ^ 0x3a}, 16)
	macKey, err := pbkdf2.Key(sha512.New, string(key), macSalt, 2, 32)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	for offset := 0; offset < len(plain); offset += 4096 {
		number := offset/4096 + 1
		page := make([]byte, 4096)
		start := 0
		if number == 1 {
			start = 16
			copy(page, salt)
		}
		iv := bytes.Repeat([]byte{byte(number)}, 16)
		copy(page[4016:], iv)
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(page[start:4016], plain[offset+start:offset+4016])
		mac := hmac.New(sha512.New, macKey)
		mac.Write(page[start:4032])
		var pageNumber [4]byte
		binary.LittleEndian.PutUint32(pageNumber[:], uint32(number))
		mac.Write(pageNumber[:])
		copy(page[4032:], mac.Sum(nil))
		encrypted = append(encrypted, page...)
	}
	path := filepath.Join(root, "contact", "contact.db")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encrypted, 0600); err != nil {
		t.Fatal(err)
	}
	return capturedKey{Salt: hex.EncodeToString(salt), Key: hex.EncodeToString(key)}
}

func TestEnsureRuntimeKeySavesAndReusesValidatedMaterial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "synthetic", "db_storage")
	candidate := runtimeQueryFixture(t, root)
	target := TargetDB{Account: "synthetic", DataDir: root, DBRelPath: "contact/contact.db", DBPath: filepath.Join(root, "contact", "contact.db")}
	regions, read, _ := windowsMemoryFixture(t, "x'"+candidate.Key+candidate.Salt+"'", bytes.Repeat([]byte{0xa5}, 32))
	session := newWindowsScanSession(context.Background())
	scan := func(ctx context.Context, salt string, page []byte, _ string) (string, error) {
		return session.key(ctx, 1, salt, page, func(ctx context.Context, budget *memoryReadBudget) (windowsKeyMaterial, error) {
			return collectWindowsKeyMaterial(ctx, regions, read, budget)
		})
	}
	result, err := ensureDBKey(context.Background(), target, scan)
	if err != nil || result.Reused || result.Entry.Key != candidate.Key {
		t.Fatal("runtime candidate did not reach the account store")
	}
	result, err = ensureDBKey(context.Background(), target, func(context.Context, string, []byte, string) (string, error) {
		return "", errors.New("process unavailable")
	})
	if err != nil || !result.Reused {
		t.Fatal("valid stored key was not reused without scanning")
	}
	loaded, err := LoadDBKey(context.Background(), target)
	if err != nil || loaded.Entry.Key != candidate.Key {
		t.Fatal("query key loader could not consume the saved key")
	}
	_, cache, err := EnsureDBCache(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	items, err := contacts.NewService(cache).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Username != "synthetic_friend" || items[0].Remark != "Fixture contact" {
		t.Fatal("runtime key did not survive key store, cache decryption and production contact query")
	}
}

func TestEnsureScanFailureOrCancellationPreservesStore(t *testing.T) {
	for _, mode := range []string{"invalid", "failure", "canceled after scan", "already canceled"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			root := filepath.Join(home, "synthetic", "db_storage")
			candidate := captureFixture(t, root, "contact/contact.db", 7)
			other := captureFixture(t, root, "message/message_0.db", 19)
			store := Store{Version: 1}
			store.Upsert(Entry{Account: "synthetic", DataDir: root, DBRelPath: "message/message_0.db", Key: other.Key, Salt: other.Salt})
			storePath, err := KeyStorePath("synthetic")
			if err != nil {
				t.Fatal(err)
			}
			if err := SaveStore(storePath, store); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(storePath)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "already canceled" {
				cancel()
			}
			target := TargetDB{Account: "synthetic", DataDir: root, DBRelPath: "contact/contact.db", DBPath: filepath.Join(root, "contact", "contact.db")}
			_, err = ensureDBKey(ctx, target, func(context.Context, string, []byte, string) (string, error) {
				switch mode {
				case "invalid":
					return other.Key, nil
				case "failure":
					return "", errMemoryRead
				case "canceled after scan":
					cancel()
				}
				return candidate.Key, nil
			})
			if err == nil {
				t.Fatal("failed or canceled scan was accepted")
			}
			if (mode == "already canceled" || mode == "canceled after scan") && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost its cause")
			}
			after, err := os.ReadFile(storePath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("failed initialization changed existing account keys")
			}
		})
	}
}
