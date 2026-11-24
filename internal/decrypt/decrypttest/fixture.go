// Package decrypttest supplies independently generated SQLite/WAL fixtures.
// It never reads WeChat data and is imported only by tests.
package decrypttest

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha512"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed fixture.b64
var packed string

type Fixture struct {
	Main, WAL1, WAL2 []byte
	Key, Salt        string
}

func Load(t *testing.T) Fixture {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(packed))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(zr)
	_ = zr.Close()
	if err != nil {
		t.Fatal(err)
	}
	var plain struct{ Main, WAL1, WAL2 []byte }
	if err = json.Unmarshal(data, &plain); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	salt := bytes.Repeat([]byte{0x7a}, 16)
	encryptPage := func(page []byte, number uint32) []byte {
		out := make([]byte, 4096)
		start := 0
		if number == 1 {
			start = 16
			copy(out, salt)
		}
		iv := bytes.Repeat([]byte{byte(number)}, 16)
		copy(out[4016:], iv)
		block, err := aes.NewCipher(key)
		if err != nil {
			t.Fatal(err)
		}
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[start:4016], page[start:4016])
		macSalt := bytes.Repeat([]byte{0x7a ^ 0x3a}, 16)
		macKey, err := pbkdf2.Key(sha512.New, string(key), macSalt, 2, 32)
		if err != nil {
			t.Fatal(err)
		}
		mac := hmac.New(sha512.New, macKey)
		mac.Write(out[start:4032])
		var n [4]byte
		binary.LittleEndian.PutUint32(n[:], number)
		mac.Write(n[:])
		copy(out[4032:], mac.Sum(nil))
		return out
	}
	main := make([]byte, 0, len(plain.Main))
	for offset := 0; offset < len(plain.Main); offset += 4096 {
		main = append(main, encryptPage(plain.Main[offset:offset+4096], uint32(offset/4096+1))...)
	}
	encryptWAL := func(wal []byte) []byte {
		out := append([]byte(nil), wal...)
		var a, b uint32
		var order binary.ByteOrder = binary.LittleEndian
		if binary.BigEndian.Uint32(wal) == 0x377f0683 {
			order = binary.BigEndian
		}
		sum := func(data []byte) {
			for i := 0; i < len(data); i += 8 {
				a += order.Uint32(data[i:]) + b
				b += order.Uint32(data[i+4:]) + a
			}
		}
		sum(out[:24])
		binary.BigEndian.PutUint32(out[24:], a)
		binary.BigEndian.PutUint32(out[28:], b)
		for offset := 32; offset+4120 <= len(out); offset += 4120 {
			copy(out[offset+24:offset+4120], encryptPage(wal[offset+24:offset+4120], binary.BigEndian.Uint32(wal[offset:])))
			sum(out[offset : offset+8])
			sum(out[offset+24 : offset+4120])
			binary.BigEndian.PutUint32(out[offset+16:], a)
			binary.BigEndian.PutUint32(out[offset+20:], b)
		}
		return out
	}
	return Fixture{Main: main, WAL1: encryptWAL(plain.WAL1), WAL2: encryptWAL(plain.WAL2), Key: hex.EncodeToString(key), Salt: hex.EncodeToString(salt)}
}

func (f Fixture) Write(t *testing.T, path string, stage int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, f.Main, 0600); err != nil {
		t.Fatal(err)
	}
	if stage > 0 {
		wal := f.WAL1
		if stage == 2 {
			wal = f.WAL2
		}
		if err := os.WriteFile(path+"-wal", wal, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
