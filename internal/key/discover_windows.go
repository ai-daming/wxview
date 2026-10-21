//go:build windows

package key

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

const contactRelPath = "contact/contact.db"
const messageRelDir = "message"
const sessionRelPath = "session/session.db"
const favoriteRelPath = "favorite/favorite.db"
const snsRelPath = "sns/sns.db"
const headImageRelPath = "head_image/head_image.db"

const (
	envWindowsDBStorage = "WXVIEW_WECHAT_DB_STORAGE"
	envWindowsDataRoot  = "WXVIEW_WECHAT_DATA_ROOT"
)

var messageAuxRelPaths = []string{
	"message/message_fts.db",
	"message/message_resource.db",
	"message/message_revoke.db",
}

var procMultiByteToWideChar = kernel32DLL.NewProc("MultiByteToWideChar")

type TargetDB struct {
	Account   string
	DataDir   string
	DBRelPath string
	DBPath    string
}

func DiscoverContactDB() (TargetDB, error) {
	candidates := discoverWindowsContactCandidates()
	if len(candidates) == 0 {
		return TargetDB{}, fmt.Errorf("no Windows WeChat contact database found; set %s to a db_storage directory or ensure %%APPDATA%%\\Tencent\\xwechat\\config points to xwechat_files", envWindowsDBStorage)
	}
	return chooseContactCandidate(candidates, nil), nil
}

func DiscoverMessageDBs() ([]TargetDB, error) {
	contactTarget, err := DiscoverContactDB()
	if err != nil {
		return nil, err
	}
	return discoverMessageDBsByPrefix(contactTarget, "message_")
}

func DiscoverBizMessageDBs() ([]TargetDB, error) {
	contactTarget, err := DiscoverContactDB()
	if err != nil {
		return nil, err
	}
	return discoverMessageDBsByPrefix(contactTarget, "biz_message_")
}

func DiscoverMediaDBs() ([]TargetDB, error) {
	contactTarget, err := DiscoverContactDB()
	if err != nil {
		return nil, err
	}
	return discoverMessageDBsByPrefix(contactTarget, "media_")
}

func discoverMessageDBsByPrefix(contactTarget TargetDB, prefix string) ([]TargetDB, error) {
	messageDir := filepath.Join(contactTarget.DataDir, messageRelDir)
	entries, err := os.ReadDir(messageDir)
	if err != nil {
		return nil, fmt.Errorf("detect WeChat message database directory %s: %w", messageDir, err)
	}

	var candidates []TargetDB
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		_, ok := numberedShardIndex(entry.Name(), prefix)
		if !ok {
			continue
		}
		relPath := filepath.ToSlash(filepath.Join(messageRelDir, entry.Name()))
		dbPath := filepath.Join(contactTarget.DataDir, filepath.FromSlash(relPath))
		info, err := os.Stat(dbPath)
		if err != nil || info.IsDir() {
			continue
		}
		candidates = append(candidates, TargetDB{
			Account:   contactTarget.Account,
			DataDir:   contactTarget.DataDir,
			DBRelPath: relPath,
			DBPath:    dbPath,
		})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no WeChat %sdatabase shard found under %s", prefix, messageDir)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, _ := numberedShardIndex(filepath.Base(candidates[i].DBPath), prefix)
		right, _ := numberedShardIndex(filepath.Base(candidates[j].DBPath), prefix)
		return left < right
	})
	return candidates, nil
}

func DiscoverMessageRelatedDBs() ([]TargetDB, error) {
	target, err := DiscoverContactDB()
	if err != nil {
		return nil, err
	}
	return AccountMessageTargets(target)
}

func DiscoverMessageAuxDBs() ([]TargetDB, error) {
	contactTarget, err := DiscoverContactDB()
	if err != nil {
		return nil, err
	}
	targets := make([]TargetDB, 0, len(messageAuxRelPaths))
	for _, relPath := range messageAuxRelPaths {
		dbPath := filepath.Join(contactTarget.DataDir, filepath.FromSlash(relPath))
		info, err := os.Stat(dbPath)
		if err != nil || info.IsDir() {
			continue
		}
		targets = append(targets, TargetDB{
			Account:   contactTarget.Account,
			DataDir:   contactTarget.DataDir,
			DBRelPath: relPath,
			DBPath:    dbPath,
		})
	}
	return targets, nil
}

func DiscoverRequiredDBs() ([]TargetDB, error) {
	contactTarget, err := DiscoverContactDB()
	if err != nil {
		return nil, err
	}
	targets := []TargetDB{contactTarget}
	messageTargets, err := DiscoverMessageDBs()
	if err != nil {
		return nil, err
	}
	targets = append(targets, messageTargets...)
	return targets, nil
}

func DiscoverSupportedDBs() ([]TargetDB, error) {
	targets, err := DiscoverRequiredDBs()
	if err != nil {
		return nil, err
	}
	if bizTargets, err := DiscoverBizMessageDBs(); err == nil {
		targets = append(targets, bizTargets...)
	}
	auxTargets, err := DiscoverMessageAuxDBs()
	if err != nil {
		return nil, err
	}
	targets = append(targets, auxTargets...)
	if sessionTarget, ok := DiscoverSessionDB(); ok {
		targets = append(targets, sessionTarget)
	}
	if favoriteTarget, ok := DiscoverFavoriteDB(); ok {
		targets = append(targets, favoriteTarget)
	}
	if snsTarget, ok := DiscoverSNSDB(); ok {
		targets = append(targets, snsTarget)
	}
	if headImageTarget, ok := DiscoverHeadImageDB(); ok {
		targets = append(targets, headImageTarget)
	}
	return targets, nil
}

func messageShardIndex(name string) (int, bool) {
	return numberedShardIndex(name, "message_")
}

func numberedShardIndex(name string, prefix string) (int, bool) {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".db") {
		return 0, false
	}
	text := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".db")
	index, err := strconv.Atoi(text)
	return index, err == nil
}

func DiscoverFavoriteDB() (TargetDB, bool) {
	return discoverOptionalDB(favoriteRelPath)
}

func DiscoverSessionDB() (TargetDB, bool) {
	return discoverOptionalDB(sessionRelPath)
}

func DiscoverSNSDB() (TargetDB, bool) {
	return discoverOptionalDB(snsRelPath)
}

func DiscoverHeadImageDB() (TargetDB, bool) {
	return discoverOptionalDB(headImageRelPath)
}

func discoverOptionalDB(relPath string) (TargetDB, bool) {
	contactTarget, err := DiscoverContactDB()
	if err != nil {
		return TargetDB{}, false
	}
	dbPath := filepath.Join(contactTarget.DataDir, filepath.FromSlash(relPath))
	info, err := os.Stat(dbPath)
	if err != nil || info.IsDir() {
		return TargetDB{}, false
	}
	return TargetDB{
		Account:   contactTarget.Account,
		DataDir:   contactTarget.DataDir,
		DBRelPath: relPath,
		DBPath:    dbPath,
	}, true
}

func discoverWindowsContactCandidates() []TargetDB {
	seen := map[string]bool{}
	var candidates []TargetDB
	addDataDir := func(dataDir string) {
		dataDir = filepath.Clean(os.ExpandEnv(strings.TrimSpace(dataDir)))
		if dataDir == "" {
			return
		}
		dbPath := filepath.Join(dataDir, filepath.FromSlash(contactRelPath))
		info, err := os.Stat(dbPath)
		if err != nil || info.IsDir() {
			return
		}
		key := strings.ToLower(filepath.Clean(dataDir))
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, TargetDB{
			Account:   filepath.Base(filepath.Dir(dataDir)),
			DataDir:   dataDir,
			DBRelPath: contactRelPath,
			DBPath:    dbPath,
		})
	}

	if exact := os.Getenv(envWindowsDBStorage); exact != "" {
		addDataDir(exact)
	}
	for _, root := range windowsDataRoots() {
		for _, dataDir := range dbStorageDirsUnderRoot(root) {
			addDataDir(dataDir)
		}
	}
	return candidates
}

func windowsDataRoots() []string {
	seen := map[string]bool{}
	var roots []string
	add := func(root string) {
		root = filepath.Clean(os.ExpandEnv(strings.TrimSpace(root)))
		if root == "" {
			return
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return
		}
		key := strings.ToLower(root)
		if seen[key] {
			return
		}
		seen[key] = true
		roots = append(roots, root)
	}

	if root := os.Getenv(envWindowsDataRoot); root != "" {
		add(root)
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		configDir := filepath.Join(appData, "Tencent", "xwechat", "config")
		matches, _ := filepath.Glob(filepath.Join(configDir, "*.ini"))
		for _, iniPath := range matches {
			data, err := os.ReadFile(iniPath)
			if err != nil {
				continue
			}
			if root := decodeWindowsConfigPath(data); root != "" {
				add(root)
			}
		}
	}
	if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
		add(filepath.Join(userProfile, "Documents"))
	}
	return roots
}

func dbStorageDirsUnderRoot(root string) []string {
	var patterns []string
	if strings.EqualFold(filepath.Base(root), "xwechat_files") {
		patterns = append(patterns, filepath.Join(root, "*", "db_storage"))
	} else {
		patterns = append(patterns, filepath.Join(root, "xwechat_files", "*", "db_storage"))
	}
	var out []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && info.IsDir() {
				out = append(out, match)
			}
		}
	}
	return out
}

func decodeWindowsConfigPath(data []byte) string {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return ""
	}
	var text string
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		text = decodeUTF16(data[2:], false)
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		text = decodeUTF16(data[2:], true)
	case utf8.Valid(data):
		text = string(data)
	default:
		text = decodeCurrentACP(data)
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	if text == "" || strings.ContainsAny(text, "\r\n\x00") {
		return ""
	}
	return text
}

func decodeUTF16(data []byte, bigEndian bool) string {
	if len(data)%2 == 1 {
		data = data[:len(data)-1]
	}
	u16 := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		if bigEndian {
			u16 = append(u16, uint16(data[i])<<8|uint16(data[i+1]))
		} else {
			u16 = append(u16, uint16(data[i])|uint16(data[i+1])<<8)
		}
	}
	return string(utf16.Decode(u16))
}

func decodeCurrentACP(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	n, _, _ := procMultiByteToWideChar.Call(0, 0, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 0, 0)
	if n == 0 {
		return string(data)
	}
	u16 := make([]uint16, int(n))
	r1, _, _ := procMultiByteToWideChar.Call(
		0,
		0,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&u16[0])),
		n,
	)
	if r1 == 0 {
		return string(data)
	}
	return syscall.UTF16ToString(u16)
}

func dbModUnix(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

func chooseContactCandidate(candidates []TargetDB, openScores map[string]int) TargetDB {
	candidates = append([]TargetDB(nil), candidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		leftScore := openScores[filepath.Clean(candidates[i].DataDir)]
		rightScore := openScores[filepath.Clean(candidates[j].DataDir)]
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return dbModUnix(candidates[i].DBPath) > dbModUnix(candidates[j].DBPath)
	})
	return candidates[0]
}

func DiscoverWatcherAccount() (TargetDB, error) { return DiscoverContactDB() }
