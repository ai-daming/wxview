package decrypt

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"wxview/internal/sqlitedb"
)

const (
	PageSize     = 4096
	KeySize      = 32
	SaltSize     = 16
	IVSize       = 16
	HMACSize     = 64
	ReserveSize  = IVSize + HMACSize
	sqliteHeader = "SQLite format 3\x00"
)

var (
	ErrIncorrectKey    = errors.New("incorrect sqlcipher key")
	ErrAlreadyPlainDB  = errors.New("database is already plaintext sqlite")
	ErrInvalidDatabase = errors.New("invalid database")
)

// ValidateRawKey verifies a WeChat 4.x SQLCipher raw page key against page 1.
func ValidateRawKey(page1 []byte, key []byte) bool {
	if len(page1) < PageSize || len(key) != KeySize {
		return false
	}
	if bytes.Equal(page1[:len(sqliteHeader)], []byte(sqliteHeader)) {
		return false
	}
	salt := page1[:SaltSize]
	macKey := deriveRawMACKey(key, salt)
	return validatePageHMAC(page1, 1, macKey)
}

func ValidateRawHexKey(page1 []byte, hexKey string) bool {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return false
	}
	return ValidateRawKey(page1, key)
}

func ReadPage1(path string) ([]byte, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	page1 := make([]byte, PageSize)
	if _, err := io.ReadFull(f, page1); err != nil {
		return nil, nil, err
	}
	if bytes.Equal(page1[:len(sqliteHeader)], []byte(sqliteHeader)) {
		return nil, nil, ErrAlreadyPlainDB
	}
	salt := append([]byte(nil), page1[:SaltSize]...)
	return page1, salt, nil
}

func DecryptFile(ctx context.Context, src string, dst string, hexKey string) error {
	snapshot, err := openSourceSnapshot(ctx, src)
	if err != nil {
		return err
	}
	defer snapshot.close()
	if err = snapshot.decrypt(ctx, dst, hexKey); err != nil {
		if stableErr := snapshot.stable(); errors.Is(stableErr, ErrSourceChanged) {
			return stableErr
		}
		return err
	}
	return snapshot.stable()
}

func (snapshot *sourceSnapshot) decrypt(ctx context.Context, dst, hexKey string) error {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != KeySize {
		return fmt.Errorf("decode key: expected %d-byte hex key", KeySize)
	}
	page1 := make([]byte, PageSize)
	if err = snapshot.readPage(1, page1); err != nil {
		return err
	}
	if bytes.Equal(page1[:len(sqliteHeader)], []byte(sqliteHeader)) {
		return ErrAlreadyPlainDB
	}
	if !ValidateRawKey(page1, key) {
		return ErrIncorrectKey
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	macKey := deriveRawMACKey(key, page1[:SaltSize])
	buffer := make([]byte, PageSize)
	for page := int64(1); page <= snapshot.count; page++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = snapshot.readPage(page, buffer); err != nil {
			return err
		}
		plain := buffer
		if !isAllZero(buffer) {
			plain, err = decryptPage(buffer, int(page), key, macKey)
			if err != nil {
				return err
			}
		}
		if _, err = out.Write(plain); err != nil {
			return err
		}
	}
	return out.Close()
}

// DecryptToCache preserves the public refresh entrypoint. All callers use the
// same stable main/WAL capture and atomic publication mechanism.
func DecryptToCache(ctx context.Context, src string, dst string, hexKey string) error {
	_, err := DecryptToCacheWithSignature(ctx, src, dst, hexKey)
	return err
}

// CacheRevision identifies the immutable cache file published by a capture.
type CacheRevision struct {
	Size    int64 `json:"size"`
	MTimeNS int64 `json:"mtime_ns"`
}

// DecryptToCacheWithSignature returns the source version actually captured.
func DecryptToCacheWithSignature(ctx context.Context, src, dst, hexKey string) (SourceSignature, error) {
	source, _, err := DecryptToCacheSnapshot(ctx, src, dst, hexKey)
	return source, err
}

// DecryptToCacheSnapshot binds the source to the temporary cache revision before
// rename. A later stat of dst could belong to a concurrent publisher.
func DecryptToCacheSnapshot(ctx context.Context, src, dst, hexKey string) (SourceSignature, CacheRevision, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return SourceSignature{}, CacheRevision{}, err
	}
	var revision CacheRevision
	source, err := retrySnapshotCapture(ctx, func() (SourceSignature, error) {
		snapshot, err := openSourceSnapshot(ctx, src)
		if err != nil {
			return SourceSignature{}, err
		}
		defer snapshot.close()
		return snapshot.publishRevision(ctx, dst, hexKey, &revision)
	})
	if err != nil {
		return SourceSignature{}, CacheRevision{}, err
	}
	return source, revision, nil
}

func retrySnapshotCapture(ctx context.Context, attempt func() (SourceSignature, error)) (SourceSignature, error) {
	var last error
	for tries := 0; tries < 3; tries++ {
		if err := ctx.Err(); err != nil {
			return SourceSignature{}, err
		}
		signature, err := attempt()
		if err == nil {
			return signature, nil
		}
		if !errors.Is(err, ErrSourceChanged) {
			return SourceSignature{}, err
		}
		last = err
	}
	return SourceSignature{}, fmt.Errorf("capture remained unstable after 3 attempts: %w", last)
}

func (snapshot *sourceSnapshot) publish(ctx context.Context, dst, hexKey string) (SourceSignature, error) {
	return snapshot.publishRevision(ctx, dst, hexKey, nil)
}

func (snapshot *sourceSnapshot) publishRevision(ctx context.Context, dst, hexKey string, revision *CacheRevision) (SourceSignature, error) {
	var err error
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.tmp")
	if err != nil {
		return SourceSignature{}, err
	}
	name := tmp.Name()
	if err = tmp.Close(); err != nil {
		_ = os.Remove(name)
		return SourceSignature{}, err
	}
	defer os.Remove(name)
	if err = snapshot.decrypt(ctx, name, hexKey); err != nil {
		if stableErr := snapshot.stable(); errors.Is(stableErr, ErrSourceChanged) {
			err = stableErr
		}
		return SourceSignature{}, err
	}
	if err = ValidateSQLite(ctx, name); err != nil {
		return SourceSignature{}, fmt.Errorf("validate decrypted sqlite: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return SourceSignature{}, err
	}
	if err = snapshot.stable(); err != nil {
		return SourceSignature{}, err
	}
	if err = os.Chmod(name, 0600); err != nil {
		return SourceSignature{}, err
	}
	info, err := os.Stat(name)
	if err != nil {
		return SourceSignature{}, err
	}
	if err = os.Rename(name, dst); err != nil {
		return SourceSignature{}, err
	}
	if revision != nil {
		*revision = CacheRevision{Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
	}
	return snapshot.state.signature, nil
}

func ValidateSQLite(ctx context.Context, dbPath string) error {
	db, err := sqlitedb.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	var schemaVersion int
	return db.QueryRowContext(ctx, "PRAGMA schema_version;").Scan(&schemaVersion)
}

func SaltHex(page1 []byte) string {
	if len(page1) < SaltSize {
		return ""
	}
	return hex.EncodeToString(page1[:SaltSize])
}

func deriveRawMACKey(encKey []byte, salt []byte) []byte {
	macSalt := make([]byte, len(salt))
	for i := range salt {
		macSalt[i] = salt[i] ^ 0x3a
	}
	return pbkdf2HMACSHA512(encKey, macSalt, 2, KeySize)
}

func validatePageHMAC(page []byte, pageNum int, macKey []byte) bool {
	if len(page) < PageSize {
		return false
	}
	dataEnd := PageSize - ReserveSize + IVSize
	stored := page[dataEnd : dataEnd+HMACSize]
	mac := hmac.New(sha512.New, macKey)
	offset := 0
	if pageNum == 1 {
		offset = SaltSize
	}
	mac.Write(page[offset:dataEnd])
	var pg [4]byte
	binary.LittleEndian.PutUint32(pg[:], uint32(pageNum))
	mac.Write(pg[:])
	return hmac.Equal(mac.Sum(nil), stored)
}

func decryptPage(page []byte, pageNum int, encKey []byte, macKey []byte) ([]byte, error) {
	if !validatePageHMAC(page, pageNum, macKey) {
		return nil, fmt.Errorf("page %d hmac verification failed", pageNum)
	}
	offset := 0
	if pageNum == 1 {
		offset = SaltSize
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	encrypted := append([]byte(nil), page[offset:PageSize-ReserveSize]...)
	iv := page[PageSize-ReserveSize : PageSize-ReserveSize+IVSize]
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(encrypted, encrypted)

	if pageNum == 1 {
		out := make([]byte, 0, PageSize)
		out = append(out, []byte(sqliteHeader)...)
		out = append(out, encrypted...)
		out = append(out, make([]byte, ReserveSize)...)
		return out, nil
	}

	out := make([]byte, 0, PageSize)
	out = append(out, encrypted...)
	out = append(out, make([]byte, ReserveSize)...)
	return out, nil
}

func isAllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func pbkdf2HMACSHA512(password []byte, salt []byte, iter int, keyLen int) []byte {
	if iter <= 0 || keyLen <= 0 {
		return nil
	}
	hLen := sha512.Size
	numBlocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, numBlocks*hLen)
	var blockIndex [4]byte

	for block := 1; block <= numBlocks; block++ {
		binary.BigEndian.PutUint32(blockIndex[:], uint32(block))
		mac := hmac.New(sha512.New, password)
		mac.Write(salt)
		mac.Write(blockIndex[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)

		for i := 1; i < iter; i++ {
			mac = hmac.New(sha512.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
