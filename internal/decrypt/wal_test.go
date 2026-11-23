package decrypt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"wxview/internal/decrypt/decrypttest"
)

type testWALFrame struct {
	page, commit uint32
	data         []byte
}

func TestSnapshotChangesAndCancellationPreserveCache(t *testing.T) {
	for _, mode := range []string{"main removed", "wal removed", "wal appended", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := decrypttest.Load(t)
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "source.db"), filepath.Join(dir, "cache.db")
			f.Write(t, src, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			snapshot, err := openSourceSnapshot(ctx, src)
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.close()
			want := ErrSourceChanged
			switch mode {
			case "main removed":
				err = os.Remove(src)
			case "wal removed":
				err = os.Remove(src + "-wal")
			case "wal appended":
				err = os.WriteFile(src+"-wal", f.WAL2, 0600)
			case "canceled":
				cancel()
				want = context.Canceled
			}
			if err != nil {
				t.Fatal(err)
			}
			old := []byte("previous cache")
			if err = os.WriteFile(dst, old, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = snapshot.publish(ctx, dst, f.Key); !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			got, err := os.ReadFile(dst)
			if err != nil || !bytes.Equal(got, old) {
				t.Fatal("failed capture replaced cache")
			}
			left, err := filepath.Glob(dst + ".*.tmp")
			if err != nil || len(left) != 0 {
				t.Fatal("failed capture left temporary data")
			}
		})
	}
}

func TestSnapshotSourceReplacementCannotPublishOldDescriptor(t *testing.T) {
	f := decrypttest.Load(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "source.db")
	dst := filepath.Join(dir, "cache.db")
	f.Write(t, src, 1)
	snapshot, err := openSourceSnapshot(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.close()
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	// Same bytes, size and mtime; identity must still detect path replacement.
	replacement := filepath.Join(dir, "replacement.db")
	if err = os.WriteFile(replacement, f.Main, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(replacement, src); err != nil {
		t.Fatal(err)
	}
	old := []byte("previous valid cache")
	if err = os.WriteFile(dst, old, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = snapshot.publish(context.Background(), dst, f.Key); !errors.Is(err, ErrSourceChanged) {
		t.Fatal("changed source identity was accepted")
	}
	after, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(after, old) {
		t.Fatal("unstable capture replaced existing cache")
	}
}

func TestCaptureRetriesAreBoundedAndDoNotRetryCorruption(t *testing.T) {
	calls := 0
	_, err := retrySnapshotCapture(context.Background(), func() (SourceSignature, error) { calls++; return SourceSignature{}, ErrSourceChanged })
	if !errors.Is(err, ErrSourceChanged) || calls != 3 {
		t.Fatal("changing source retry was not bounded to three attempts")
	}
	calls = 0
	_, err = retrySnapshotCapture(context.Background(), func() (SourceSignature, error) { calls++; return SourceSignature{}, ErrInvalidWAL })
	if !errors.Is(err, ErrInvalidWAL) || calls != 1 {
		t.Fatal("corrupt WAL was retried as a changing source")
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls = 0
	_, err = retrySnapshotCapture(ctx, func() (SourceSignature, error) { calls++; cancel(); return SourceSignature{}, ErrSourceChanged })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancellation did not stop retries")
	}
}

func TestSnapshotIgnoresOldGenerationAndIncompleteTail(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	page1 := encryptTestPage(t, 1, key, bytes.Repeat([]byte{0x11}, 4000))
	page2 := encryptTestPage(t, 2, key, bytes.Repeat([]byte{'A'}, 4016))
	updated := encryptTestPage(t, 2, key, bytes.Repeat([]byte{'B'}, 4016))
	wal := makeTestWAL(binary.LittleEndian, testWALFrame{2, 2, updated}, testWALFrame{2, 2, page2})
	wal[32+4120+8] ^= 1 // A stale frame belongs to the preceding WAL generation.
	for _, data := range [][]byte{wal, append(append([]byte(nil), wal[:32+4120]...), []byte("partial next frame")...)} {
		dir := t.TempDir()
		src := filepath.Join(dir, "source.db")
		dst := filepath.Join(dir, "cache.db")
		if err := os.WriteFile(src, append(append([]byte(nil), page1...), page2...), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(src+"-wal", data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := DecryptFile(context.Background(), src, dst, hex.EncodeToString(key)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dst)
		if err != nil || got[4096] != 'B' {
			t.Fatal("old generation or incomplete frame replaced committed data")
		}
	}
}

// Independent WAL writer for artificial encrypted pages. Uses SQLite's format,
// never the production parser/checksum, to keep corruption tests meaningful.
func makeTestWAL(order binary.ByteOrder, frames ...testWALFrame) []byte {
	header := make([]byte, 32)
	magic := uint32(0x377f0682)
	if order == binary.BigEndian {
		magic++
	}
	binary.BigEndian.PutUint32(header, magic)
	binary.BigEndian.PutUint32(header[4:], 3007000)
	binary.BigEndian.PutUint32(header[8:], 4096)
	copy(header[16:], []byte("12345678"))
	var first, second uint32
	checksum := func(data []byte) {
		for i := 0; i < len(data); i += 8 {
			first += order.Uint32(data[i:]) + second
			second += order.Uint32(data[i+4:]) + first
		}
	}
	checksum(header[:24])
	binary.BigEndian.PutUint32(header[24:], first)
	binary.BigEndian.PutUint32(header[28:], second)
	result := append([]byte(nil), header...)
	for _, frame := range frames {
		h := make([]byte, 24)
		binary.BigEndian.PutUint32(h, frame.page)
		binary.BigEndian.PutUint32(h[4:], frame.commit)
		copy(h[8:], header[16:24])
		checksum(h[:8])
		checksum(frame.data)
		binary.BigEndian.PutUint32(h[16:], first)
		binary.BigEndian.PutUint32(h[20:], second)
		result = append(result, h...)
		result = append(result, frame.data...)
	}
	return result
}

func TestDecryptFileReadsLastCommittedWALTransaction(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	page1 := encryptTestPage(t, 1, key, bytes.Repeat([]byte{0x11}, 4000))
	page2 := encryptTestPage(t, 2, key, bytes.Repeat([]byte{'A'}, 4016))
	updated := encryptTestPage(t, 2, key, bytes.Repeat([]byte{'B'}, 4016))
	uncommitted := encryptTestPage(t, 2, key, bytes.Repeat([]byte{'C'}, 4016))
	page3 := encryptTestPage(t, 3, key, bytes.Repeat([]byte{'D'}, 4016))
	for _, tc := range []struct {
		name  string
		wal   []byte
		pages int
		want  byte
	}{
		{"no wal", nil, 2, 'A'},
		{"no commit", makeTestWAL(binary.LittleEndian, testWALFrame{2, 0, updated}), 2, 'A'},
		{"committed and unfinished", makeTestWAL(binary.LittleEndian, testWALFrame{2, 2, updated}, testWALFrame{2, 0, uncommitted}), 2, 'B'},
		{"big endian checksums", makeTestWAL(binary.BigEndian, testWALFrame{2, 2, updated}), 2, 'B'},
		{"growth", makeTestWAL(binary.LittleEndian, testWALFrame{3, 3, page3}), 3, 'A'},
		{"truncation", makeTestWAL(binary.LittleEndian, testWALFrame{1, 1, page1}), 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "source.db")
			dst := filepath.Join(dir, "cache.db")
			main := append(append([]byte(nil), page1...), page2...)
			if err := os.WriteFile(src, main, 0600); err != nil {
				t.Fatal(err)
			}
			if tc.wal != nil {
				if err := os.WriteFile(src+"-wal", tc.wal, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := DecryptFile(context.Background(), src, dst, hex.EncodeToString(key)); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dst)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.pages*4096 {
				t.Fatal("snapshot has the wrong committed database size")
			}
			if tc.pages >= 2 && !bytes.Equal(got[4096:4096+4016], bytes.Repeat([]byte{tc.want}, 4016)) {
				t.Fatal("snapshot omitted committed WAL data or included an unfinished transaction")
			}
			after, err := os.ReadFile(src)
			if err != nil || !bytes.Equal(after, main) {
				t.Fatal("source main database was changed")
			}
			if tc.wal != nil {
				after, err = os.ReadFile(src + "-wal")
				if err != nil || !bytes.Equal(after, tc.wal) {
					t.Fatal("source WAL was changed")
				}
			}
		})
	}
}

func TestDecryptFileRejectsInvalidWALAndJournal(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	first := encryptTestPage(t, 1, key, bytes.Repeat([]byte{0x11}, 4000))
	second := encryptTestPage(t, 2, key, bytes.Repeat([]byte{'A'}, 4016))
	valid := makeTestWAL(binary.LittleEndian, testWALFrame{2, 2, second})
	badHeader := append([]byte(nil), valid...)
	badHeader[24] ^= 1
	badFrame := append([]byte(nil), valid...)
	badFrame[100] ^= 1
	for _, tc := range []struct {
		name    string
		wal     []byte
		journal bool
	}{
		{"header checksum", badHeader, false}, {"frame checksum", badFrame, false}, {"incomplete header", []byte("short"), false},
		{"missing growth page", makeTestWAL(binary.LittleEndian, testWALFrame{3, 3, second}), false},
		{"truncated pages cannot resurrect", makeTestWAL(binary.LittleEndian, testWALFrame{1, 1, first}, testWALFrame{3, 3, second}), false},
		{"rollback journal", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "source.db")
			dst := filepath.Join(dir, "cache.db")
			main := first
			if tc.name == "truncated pages cannot resurrect" {
				main = append(append([]byte(nil), first...), second...)
			}
			if err := os.WriteFile(src, main, 0600); err != nil {
				t.Fatal(err)
			}
			if tc.wal != nil {
				if err := os.WriteFile(src+"-wal", tc.wal, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.journal {
				if err := os.WriteFile(src+"-journal", []byte("active"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := DecryptFile(context.Background(), src, dst, hex.EncodeToString(key)); err == nil {
				t.Fatal("invalid or unsafe source pair was accepted")
			}
		})
	}
}
