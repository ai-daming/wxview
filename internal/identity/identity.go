package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wxview/internal/app"
	"wxview/internal/contacts"
	"wxview/internal/key"
)

const SchemaVersion = 1

type Info struct {
	SchemaVersion int     `json:"schema_version"`
	Account       Account `json:"account"`
}

type Account struct {
	Account            string `json:"account"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	Alias              string `json:"alias"`
	Remark             string `json:"remark"`
	NickName           string `json:"nick_name"`
	IdentitySource     string `json:"identity_source"`
	CacheDir           string `json:"cache_dir"`
	DataDirFingerprint string `json:"data_dir_fingerprint"`
	DataDir            string `json:"data_dir,omitempty"`
}

func ResolveCurrent(ctx context.Context, includePaths bool) (Info, error) {
	target, err := key.DiscoverContactDB()
	if err != nil {
		return Info{}, err
	}
	cachePath, err := key.ContactCachePath(target.Account)
	if err != nil {
		return Info{}, err
	}
	return Resolve(ctx, target, cachePath, includePaths)
}

func Resolve(ctx context.Context, target key.TargetDB, contactCachePath string, includePaths bool) (Info, error) {
	if strings.TrimSpace(target.Account) == "" {
		return Info{}, fmt.Errorf("account is required")
	}
	username := UsernameFromAccount(target.Account)
	account := Account{
		Account:            target.Account,
		Username:           username,
		DisplayName:        username,
		IdentitySource:     "account",
		CacheDir:           cacheDir(target.Account, contactCachePath),
		DataDirFingerprint: dataDirFingerprint(target.DataDir),
	}
	if includePaths {
		account.DataDir = target.DataDir
	}

	if contactCachePath != "" {
		if _, err := os.Stat(contactCachePath); err == nil {
			contact, ok, err := contacts.NewService(contactCachePath).Lookup(ctx, username)
			if err != nil {
				return Info{}, err
			}
			if ok {
				account.Alias = contact.Alias
				account.Remark = contact.Remark
				account.NickName = contact.NickName
				account.DisplayName = contacts.DisplayName(contact)
				account.IdentitySource = "contact_cache"
			}
		} else if !os.IsNotExist(err) {
			return Info{}, err
		}
	}

	return Info{
		SchemaVersion: SchemaVersion,
		Account:       account,
	}, nil
}

func UsernameFromAccount(account string) string {
	account = strings.TrimSpace(account)
	if !strings.HasPrefix(account, "wxid_") {
		return account
	}
	idx := strings.LastIndex(account, "_")
	if idx <= len("wxid_") {
		return account
	}
	candidate := account[:idx]
	if strings.HasPrefix(candidate, "wxid_") {
		return candidate
	}
	return account
}

func cacheDir(account string, contactCachePath string) string {
	if contactCachePath != "" {
		return filepath.Dir(filepath.Dir(contactCachePath))
	}
	base, err := app.BaseDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "cache", app.SafeAccountDir(account))
}

func dataDirFingerprint(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	cleaned, err := filepath.Abs(dataDir)
	if err != nil {
		cleaned = filepath.Clean(dataDir)
	}
	sum := sha256.Sum256([]byte(cleaned))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}
