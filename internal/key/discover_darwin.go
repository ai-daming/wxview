//go:build darwin

package key

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"wxview/internal/app"
)

const contactRelPath = "contact/contact.db"
const messageRelDir = "message"
const sessionRelPath = "session/session.db"
const favoriteRelPath = "favorite/favorite.db"
const snsRelPath = "sns/sns.db"
const headImageRelPath = "head_image/head_image.db"

var messageAuxRelPaths = []string{
	"message/message_fts.db",
	"message/message_resource.db",
	"message/message_revoke.db",
}

type TargetDB struct {
	Account   string
	DataDir   string
	DBRelPath string
	DBPath    string
}

func DiscoverContactDB() (TargetDB, error) { return discoverContactDB(false) }

// DiscoverWatcherAccount requires positive evidence from WeChat open files.
// Background probe failures must never silently select a different mtime account.
func DiscoverWatcherAccount() (TargetDB, error) { return discoverContactDB(true) }

func discoverContactDB(requireActive bool) (TargetDB, error) {
	home, err := app.HomeDir()
	if err != nil {
		return TargetDB{}, err
	}
	root := filepath.Join(home, "Library", "Containers", "com.tencent.xinWeChat", "Data", "Documents", "xwechat_files")
	entries, err := os.ReadDir(root)
	if err != nil {
		return TargetDB{}, fmt.Errorf("detect WeChat data root %s: %w", root, err)
	}

	var candidates []TargetDB
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		account := entry.Name()
		dataDir := filepath.Join(root, account, "db_storage")
		dbPath := filepath.Join(dataDir, filepath.FromSlash(contactRelPath))
		info, err := os.Stat(dbPath)
		if err != nil || info.IsDir() {
			continue
		}
		candidates = append(candidates, TargetDB{
			Account:   account,
			DataDir:   dataDir,
			DBRelPath: contactRelPath,
			DBPath:    dbPath,
		})
	}
	if len(candidates) == 0 {
		return TargetDB{}, fmt.Errorf("no WeChat contact database found under %s", root)
	}
	scores := wechatOpenDataDirScores(root)
	if requireActive {
		active := candidates[:0]
		for _, candidate := range candidates {
			if scores[filepath.Clean(candidate.DataDir)] > 0 {
				active = append(active, candidate)
			}
		}
		if len(active) == 0 {
			return TargetDB{}, fmt.Errorf("active WeChat account is unavailable; retrying after the probe interval")
		}
		candidates = active
	}
	return chooseContactCandidate(candidates, scores), nil
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

func wechatOpenDataDirScores(root string) map[string]int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pids, err := wechatPIDs(ctx)
	if err != nil || len(pids) == 0 {
		return nil
	}
	scores := map[string]int{}
	for _, pid := range pids {
		cmd := exec.CommandContext(ctx, "lsof", "-Fn", "-p", strconv.Itoa(pid))
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "n") {
				continue
			}
			if dataDir, ok := accountDataDirFromOpenPath(root, line[1:]); ok {
				scores[filepath.Clean(dataDir)]++
			}
		}
	}
	return scores
}

func accountDataDirFromOpenPath(root string, path string) (string, bool) {
	path = strings.TrimSuffix(path, " (deleted)")
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 || parts[1] != "db_storage" {
		return "", false
	}
	return filepath.Join(root, parts[0], "db_storage"), true
}
