package key

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wxview/internal/app"
)

const keyStoreRelPath = "keys.json"

type Entry struct {
	Account     string    `json:"account"`
	DataDir     string    `json:"data_dir"`
	DBRelPath   string    `json:"db_rel_path"`
	Key         string    `json:"key"`
	Salt        string    `json:"salt"`
	Fingerprint string    `json:"fingerprint"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Store struct {
	Version int     `json:"version"`
	Keys    []Entry `json:"keys"`
}

type storeFile struct {
	Version int                   `json:"version"`
	Account string                `json:"account"`
	DataDir string                `json:"data_dir"`
	Keys    map[string]dbKeyStore `json:"keys"`
}

type dbKeyStore struct {
	Key         string    `json:"key"`
	Salt        string    `json:"salt"`
	Fingerprint string    `json:"fingerprint"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func LoadStore(path string) (Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Store{Version: 1}, nil
		}
		return Store{}, err
	}
	if len(data) == 0 {
		return Store{Version: 1}, nil
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return Store{}, err
	}
	if file.Version == 0 {
		file.Version = 1
	}
	return flattenStoreFile(file), nil
}

func SaveStore(path string, store Store) error {
	store.Version = 1
	sort.SliceStable(store.Keys, func(i, j int) bool {
		if store.Keys[i].Account != store.Keys[j].Account {
			return store.Keys[i].Account < store.Keys[j].Account
		}
		if store.Keys[i].DataDir != store.Keys[j].DataDir {
			return store.Keys[i].DataDir < store.Keys[j].DataDir
		}
		return store.Keys[i].DBRelPath < store.Keys[j].DBRelPath
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(groupStoreFile(store), "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func KeyStorePath(account string) (string, error) {
	return app.CacheDBPath(account, keyStoreRelPath)
}

func CompactStoreFile(account string) error {
	storePath, err := KeyStorePath(account)
	if err != nil {
		return err
	}
	store, err := LoadStore(storePath)
	if err != nil {
		return err
	}
	if err := SaveStore(storePath, store); err != nil {
		return err
	}
	return app.ChownForSudo(storePath)
}

func flattenStoreFile(file storeFile) Store {
	store := Store{Version: file.Version}
	for relPath, key := range file.Keys {
		store.Keys = append(store.Keys, Entry{
			Account:     file.Account,
			DataDir:     file.DataDir,
			DBRelPath:   relPath,
			Key:         strings.ToLower(key.Key),
			Salt:        strings.ToLower(key.Salt),
			Fingerprint: key.Fingerprint,
			UpdatedAt:   key.UpdatedAt,
		})
	}
	return store
}

func groupStoreFile(store Store) storeFile {
	file := storeFile{
		Version: 1,
		Keys:    map[string]dbKeyStore{},
	}
	if len(store.Keys) > 0 {
		file.Account = store.Keys[0].Account
		file.DataDir = store.Keys[0].DataDir
	}
	for _, entry := range store.Keys {
		file.Keys[entry.DBRelPath] = dbKeyStore{
			Key:         strings.ToLower(entry.Key),
			Salt:        strings.ToLower(entry.Salt),
			Fingerprint: entry.Fingerprint,
			UpdatedAt:   entry.UpdatedAt,
		}
	}
	return file
}

func (s Store) Find(dataDir string, dbRelPath string, salt string) (Entry, bool) {
	for _, entry := range s.Keys {
		if samePath(entry.DataDir, dataDir) && entry.DBRelPath == dbRelPath && strings.EqualFold(entry.Salt, salt) {
			return entry, true
		}
	}
	return Entry{}, false
}

func (s *Store) Upsert(entry Entry) {
	entry.Fingerprint = Fingerprint(entry.Key, entry.Salt, entry.DataDir, entry.DBRelPath)
	entry.UpdatedAt = time.Now().UTC()
	for i := range s.Keys {
		if samePath(s.Keys[i].DataDir, entry.DataDir) && s.Keys[i].DBRelPath == entry.DBRelPath && strings.EqualFold(s.Keys[i].Salt, entry.Salt) {
			s.Keys[i] = entry
			return
		}
	}
	s.Keys = append(s.Keys, entry)
}

func Fingerprint(keyHex string, saltHex string, dataDir string, relPath string) string {
	sum := sha256.Sum256([]byte(keyHex + "|" + saltHex + "|" + dataDir + "|" + relPath))
	return hex.EncodeToString(sum[:8])
}

func samePath(a string, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA == nil {
		a = aa
	}
	if errB == nil {
		b = bb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
