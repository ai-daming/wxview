package app

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	Name                     = "wxview"
	ConfigDir                = ".wxview"
	SocketName               = "wxview.sock"
	LogFileName              = "wxview.log"
	DaemonSpawnTokenFileName = "daemon-spawn-token"
)

func HomeDir() (string, error) {
	if os.Geteuid() == 0 {
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && sudoUser != "root" {
			u, err := user.Lookup(sudoUser)
			if err == nil && u.HomeDir != "" {
				return u.HomeDir, nil
			}
		}
	}
	return os.UserHomeDir()
}

func BaseDir() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ConfigDir), nil
}

func EnsureBaseDir() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	if err := ChownForSudo(base); err != nil {
		return "", err
	}
	return base, nil
}

func SocketPath() (string, error) {
	base, err := EnsureBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, SocketName), nil
}

func LogPath() (string, error) {
	base, err := EnsureBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, LogFileName), nil
}

func DaemonSpawnTokenPath() (string, error) {
	base, err := EnsureBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, DaemonSpawnTokenFileName), nil
}

func CacheDBPath(account string, relPath string) (string, error) {
	base, err := EnsureBaseDir()
	if err != nil {
		return "", err
	}
	cacheRoot := filepath.Join(base, "cache")
	accountDir := filepath.Join(cacheRoot, SafeAccountDir(account))
	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		return "", err
	}
	if err := ChownForSudo(cacheRoot); err != nil {
		return "", err
	}
	if err := ChownForSudo(accountDir); err != nil {
		return "", err
	}
	return filepath.Join(accountDir, relPath), nil
}

func SafeAccountDir(account string) string {
	if account == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range account {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func ChownForSudo(path string) error {
	uid, gid, ok := sudoOwner()
	if !ok {
		return nil
	}
	if err := os.Chown(path, uid, gid); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ChownTreeForSudo(path string) error {
	uid, gid, ok := sudoOwner()
	if !ok {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := os.Chown(p, uid, gid); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

func sudoOwner() (int, int, bool) {
	return sudoOwnerFromEnv(os.Geteuid(), os.Getenv("SUDO_UID"), os.Getenv("SUDO_GID"))
}

func sudoOwnerFromEnv(euid int, uidText string, gidText string) (int, int, bool) {
	if euid != 0 || uidText == "" || gidText == "" {
		return 0, 0, false
	}
	uid, err := strconv.Atoi(uidText)
	if err != nil || uid <= 0 {
		return 0, 0, false
	}
	gid, err := strconv.Atoi(gidText)
	if err != nil || gid <= 0 {
		return 0, 0, false
	}
	return uid, gid, true
}
