package key

import (
	"bytes"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Artificial ciphertext is enough to exercise the production page-1 HMAC gate.
// No user data or real WeChat keys are used by these tests.
func captureFixture(t *testing.T, root, rel string, seed byte) capturedKey {
	t.Helper()
	key := bytes.Repeat([]byte{seed}, 32)
	page := bytes.Repeat([]byte{seed + 1}, 4096)
	salt := append([]byte(nil), page[:16]...)
	macsalt := append([]byte(nil), salt...)
	for i := range macsalt {
		macsalt[i] ^= 0x3a
	}
	mk, err := pbkdf2.Key(sha512.New, string(key), macsalt, 2, 32)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha512.New, mk)
	mac.Write(page[16:4032])
	mac.Write([]byte{1, 0, 0, 0})
	copy(page[4032:], mac.Sum(nil))
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, page, 0600); err != nil {
		t.Fatal(err)
	}
	return capturedKey{Salt: hex.EncodeToString(salt), Key: hex.EncodeToString(key)}
}

func captureRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "account", "db_storage")
}

func TestCaptureMergeCoverageAndAtomicSave(t *testing.T) {
	root := captureRoot(t)
	contact := captureFixture(t, root, "contact/contact.db", 1)
	message := captureFixture(t, root, "message/message_0.db", 2)
	captureFixture(t, root, "session/session.db", 3)
	old := Store{Version: 1}
	old.Upsert(Entry{Account: "account", DataDir: root, DBRelPath: "message/message_0.db", Key: message.Key, Salt: message.Salt})
	old.Upsert(Entry{Account: "account", DataDir: root, DBRelPath: "contact/contact.db", Key: strings.Repeat("ff", 32), Salt: strings.Repeat("ff", 16)})
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := SaveStore(path, old); err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(old)
	before, _ := os.ReadFile(path)
	file := captureFile{Version: 1, Keys: map[string]capturedKey{"contact/contact.db": contact}}
	merged, result, err := mergeCapturedKeys(root, old, file)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Keys) != 2 || len(result.Keys) != 2 || len(result.Warnings) != 1 {
		t.Fatalf("unexpected counts: keys=%d results=%d warnings=%d", len(merged.Keys), len(result.Keys), len(result.Warnings))
	}
	if !result.Keys[1].Reused || result.Keys[0].Reused {
		t.Fatal("new/reused classification incorrect")
	}
	unchanged, _ := json.Marshal(old)
	if !bytes.Equal(original, unchanged) {
		t.Fatal("merge mutated original")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("merge wrote before save")
	}
	if err := SaveStore(path, merged); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := loaded.Find(root, "contact/contact.db", contact.Salt); !ok || e.Key != contact.Key {
		t.Fatal("saved key cannot be reused")
	}
	// A newly created required shard must be re-discovered, even if capture began earlier.
	captureFixture(t, root, "message/message_1.db", 4)
	before, _ = os.ReadFile(path)
	if _, _, err = mergeCapturedKeys(root, loaded, file); err == nil || !strings.Contains(err.Error(), "message/message_1.db") {
		t.Fatal("new required shard not enforced")
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed merge overwrote store")
	}
}

func TestCaptureRejectsInvalidAndKeepsVerifiedOld(t *testing.T) {
	root := captureRoot(t)
	contact := captureFixture(t, root, "contact/contact.db", 1)
	message := captureFixture(t, root, "message/message_0.db", 2)
	old := Store{Version: 1}
	for rel, c := range map[string]capturedKey{"contact/contact.db": contact, "message/message_0.db": message} {
		old.Upsert(Entry{Account: "account", DataDir: root, DBRelPath: rel, Key: c.Key, Salt: c.Salt})
	}
	invalid := contact
	invalid.Key = strings.Repeat("ff", 32)
	tests := []struct {
		name      string
		file      captureFile
		store     Store
		wantError bool
	}{
		{"zero", captureFile{1, map[string]capturedKey{}}, old, true},
		{"bad hmac only", captureFile{1, map[string]capturedKey{"contact/contact.db": invalid}}, old, true},
		{"bad hmac with valid capture", captureFile{1, map[string]capturedKey{"contact/contact.db": invalid, "message/message_0.db": message}}, old, false},
		{"unknown target", captureFile{1, map[string]capturedKey{"other/other.db": contact}}, old, true},
		{"missing required", captureFile{1, map[string]capturedKey{"contact/contact.db": contact}}, Store{Version: 1}, true},
		{"wrong account", captureFile{1, map[string]capturedKey{"contact/contact.db": contact}}, Store{Version: 1, Keys: []Entry{{Account: "other", DataDir: root}}}, true},
		{"wrong root", captureFile{1, map[string]capturedKey{"contact/contact.db": contact}}, Store{Version: 1, Keys: []Entry{{Account: "account", DataDir: root + "other"}}}, true},
		{"future version", captureFile{1, map[string]capturedKey{"contact/contact.db": contact}}, Store{Version: 2}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := json.Marshal(tc.store)
			merged, result, err := mergeCapturedKeys(root, tc.store, tc.file)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v", err)
			}
			after, _ := json.Marshal(tc.store)
			if !bytes.Equal(before, after) {
				t.Fatal("input mutated")
			}
			if !tc.wantError {
				e, ok := merged.Find(root, "contact/contact.db", contact.Salt)
				if !ok || e.Key != contact.Key || len(result.Warnings) != 1 {
					t.Fatal("verified old key was lost")
				}
			}
		})
	}
	// Salt changed while the helper was running: both old and candidate are stale.
	captureFixture(t, root, "contact/contact.db", 9)
	if _, _, err := mergeCapturedKeys(root, old, captureFile{1, map[string]capturedKey{"contact/contact.db": contact, "message/message_0.db": message}}); err == nil {
		t.Fatal("changed salt accepted")
	}
}

func TestReadCaptureFileStrictAndPrivateErrors(t *testing.T) {
	secret := strings.Repeat("ab", 32)
	salt := strings.Repeat("cd", 16)
	valid := `{"version":1,"keys":{"contact/contact.db":{"salt":"` + salt + `","enc_key":"` + secret + `"}}}`
	cases := []string{
		valid + ` {}`, strings.Replace(valid, `"version":1`, `"version":1,"version":2`, 1),
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"salt":`, `"unknown":"`+secret+`","salt":`, 1),
		strings.Replace(valid, "contact/contact.db", "../contact.db", 1),
		strings.Replace(valid, "contact/contact.db", "/contact.db", 1),
		strings.Replace(valid, secret, "bad", 1),
		`{"version":1,"keys":null}`,
		`{"version":1,"keys":{"a":{},"a":{}}}`,
		strings.Repeat(" ", 4*1024*1024+1),
	}
	path := filepath.Join(t.TempDir(), "candidates.json")
	for _, s := range cases {
		if err := os.WriteFile(path, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := readCaptureFile(path)
		if err == nil {
			t.Fatal("invalid capture accepted")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatal("secret leaked")
		}
	}
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := readCaptureFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(file.Keys["contact/contact.db"], capturedKey{salt, secret}) {
		t.Fatal("valid capture not parsed")
	}
}

func TestCaptureDiscoveryRejectsSymlink(t *testing.T) {
	root := captureRoot(t)
	captureFixture(t, root, "contact/contact.db", 1)
	captureFixture(t, root, "message/message_0.db", 2)
	source := filepath.Join(root, "message/message_0.db")
	if err := os.Symlink(source, filepath.Join(root, "message/message_1.db")); err != nil {
		t.Skip("symlink not available")
	}
	if _, _, err := discoverCaptureTargets(root); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestLoadStorePreservesUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"keys":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Version != 2 {
		t.Fatal("unknown version silently relabeled")
	}
}

func TestCaptureSaveFailureKeepsExistingFile(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("needs ordinary filesystem permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	original := []byte(`{"version":1,"keys":{}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if err := SaveStore(path, Store{Version: 1}); err == nil {
		t.Fatal("write into read-only directory succeeded")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("failed save changed existing store")
	}
}
