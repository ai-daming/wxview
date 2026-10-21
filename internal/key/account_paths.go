package key

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// TargetFor resolves a database within an already selected account.
func TargetFor(account TargetDB, relative string) TargetDB {
	return TargetDB{Account: account.Account, DataDir: account.DataDir, DBRelPath: relative, DBPath: filepath.Join(account.DataDir, filepath.FromSlash(relative))}
}

// AccountShards never performs process or account discovery.
func AccountShards(account TargetDB, prefix string) ([]TargetDB, error) {
	entries, err := os.ReadDir(filepath.Join(account.DataDir, "message"))
	if err != nil {
		return nil, err
	}
	var out []TargetDB
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".db") {
			continue
		}
		number := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".db")
		if number == "" || strings.Trim(number, "0123456789") != "" {
			continue
		}
		if _, err := strconv.ParseUint(number, 10, 63); err != nil {
			continue
		}
		out = append(out, TargetFor(account, "message/"+name))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DBRelPath < out[j].DBRelPath })
	return out, nil
}

func AccountMessageTargets(account TargetDB) ([]TargetDB, error) {
	targets, err := AccountShards(account, "message_")
	if err != nil {
		return nil, err
	}
	for _, prefix := range []string{"biz_message_", "media_"} {
		extra, err := AccountShards(account, prefix)
		if err != nil {
			return nil, err
		}
		targets = append(targets, extra...)
	}
	for _, rel := range []string{"message/message_fts.db", "message/message_resource.db", "message/message_revoke.db"} {
		target := TargetFor(account, rel)
		if info, err := os.Stat(target.DBPath); err == nil && !info.IsDir() {
			targets = append(targets, target)
		}
	}
	return targets, nil
}

func AccountMessageCachePaths(ctx context.Context, account TargetDB, refresh bool) ([]string, error) {
	targets, err := AccountShards(account, "message_")
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("message cache does not exist: initialize or refresh the selected account first")
	}
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		path, err := CachePath(account.Account, target.DBRelPath)
		if err != nil {
			return nil, err
		}
		if refresh {
			if _, _, err := EnsureDBCache(ctx, target); err != nil {
				return nil, err
			}
		}
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("message cache does not exist: initialize or refresh the selected account first: %w", err)
		}
		paths = append(paths, path)
	}
	if refresh {
		if related, err := AccountMessageTargets(account); err == nil {
			required := make(map[string]bool, len(targets))
			for _, target := range targets {
				required[target.DBRelPath] = true
			}
			for _, target := range related {
				if !required[target.DBRelPath] {
					_, _, _ = EnsureDBCache(ctx, target)
				}
			}
		}
	}
	return paths, nil
}
