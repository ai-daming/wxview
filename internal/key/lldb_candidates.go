package key

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"wxview/internal/decrypt"
)

type capturedKey struct {
	Salt string `json:"salt"`
	Key  string `json:"enc_key"`
}

type captureFile struct {
	Version int                    `json:"version"`
	Keys    map[string]capturedKey `json:"keys"`
}

var captureShard = regexp.MustCompile(`^(message|biz_message|media)_[0-9]+\.db$`)

// Discovery stays bound to one account even if the user logs into another one.
func discoverCaptureTargets(root string) ([]TargetDB, map[string]bool, error) {
	required := map[string]bool{"contact/contact.db": true}
	rels := []string{"contact/contact.db"}
	entries, err := os.ReadDir(filepath.Join(root, "message"))
	if err != nil {
		return nil, nil, fmt.Errorf("read message directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !captureShard.MatchString(entry.Name()) {
			continue
		}
		rel := "message/" + entry.Name()
		rels = append(rels, rel)
		if strings.HasPrefix(entry.Name(), "message_") {
			required[rel] = true
		}
	}
	if len(required) == 1 {
		return nil, nil, fmt.Errorf("no numeric message shards in selected account")
	}
	for _, rel := range []string{
		"message/message_fts.db", "message/message_resource.db", "message/message_revoke.db",
		"session/session.db", "head_image/head_image.db", "favorite/favorite.db", "sns/sns.db",
	} {
		_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	targets := make([]TargetDB, 0, len(rels))
	for _, rel := range rels {
		path := filepath.Join(root, filepath.FromSlash(rel))
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read target %s: %w", rel, err)
		}
		if resolved != path {
			return nil, nil, fmt.Errorf("symlink database target rejected: %s", rel)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("not a regular database: %s", rel)
		}
		targets = append(targets, TargetDB{Account: filepath.Base(filepath.Dir(root)), DataDir: root, DBRelPath: rel, DBPath: path})
	}
	return targets, required, nil
}

// Reject duplicate JSON members before decoding: encoding/json otherwise silently
// accepts the last duplicate key, which is ambiguous for a secret-bearing file.
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 8 {
		return fmt.Errorf("candidate JSON too deeply nested")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := t.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate candidate JSON member")
			}
			seen[name] = true
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
	case json.Delim('['):
		for d.More() {
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
	}
	return err
}

func readCaptureFile(path string) (captureFile, error) {
	var file captureFile
	f, err := os.Open(path)
	if err != nil {
		return file, fmt.Errorf("no capture result available")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
	if err != nil || len(data) > 4*1024*1024 {
		return file, fmt.Errorf("capture result unreadable or too large")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if uniqueJSON(d, 0) != nil {
		return file, fmt.Errorf("invalid or duplicate capture JSON")
	}
	if _, err := d.Token(); err != io.EOF {
		return file, fmt.Errorf("trailing capture JSON")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&file) != nil || file.Version != 1 || file.Keys == nil {
		return file, fmt.Errorf("unsupported capture format")
	}
	for rel, c := range file.Keys {
		if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel || strings.Contains(rel, "\\") || strings.HasPrefix(rel, "../") {
			return captureFile{}, fmt.Errorf("invalid candidate path")
		}
		key, e1 := hex.DecodeString(c.Key)
		salt, e2 := hex.DecodeString(c.Salt)
		if e1 != nil || e2 != nil || len(key) != 32 || len(salt) != 16 {
			return captureFile{}, fmt.Errorf("invalid candidate key or salt encoding")
		}
	}
	return file, nil
}

// No write occurs until all required current targets are covered. The old Store
// is copied so errors do not even mutate the caller's in-memory state.
func mergeCapturedKeys(root string, old Store, file captureFile) (Store, EnsureKeysResult, error) {
	result := EnsureKeysResult{}
	targets, required, err := discoverCaptureTargets(root)
	if err != nil {
		return Store{}, result, err
	}
	account := targets[0].Account
	if old.Version != 1 {
		return Store{}, result, fmt.Errorf("unsupported existing key store version")
	}
	entries := map[string]Entry{}
	for _, e := range old.Keys {
		if e.Account != account || !samePath(e.DataDir, root) {
			return Store{}, result, fmt.Errorf("existing key store belongs to a different account or data directory")
		}
		entries[e.DBRelPath] = e
	}
	allowed := map[string]bool{}
	for _, t := range targets {
		allowed[t.DBRelPath] = true
	}
	for rel := range file.Keys {
		if !allowed[rel] {
			return Store{}, result, fmt.Errorf("candidate target not in current supported account databases")
		}
	}
	captured := 0
	var missing []string
	for _, target := range targets {
		page, salt, readErr := decrypt.ReadPage1(target.DBPath)
		saltHex := hex.EncodeToString(salt)
		candidate, hasCandidate := file.Keys[target.DBRelPath]
		if readErr == nil && hasCandidate && strings.EqualFold(candidate.Salt, saltHex) && decrypt.ValidateRawHexKey(page, candidate.Key) {
			e := Entry{Account: account, DataDir: root, DBRelPath: target.DBRelPath, Key: strings.ToLower(candidate.Key), Salt: saltHex}
			one := Store{Version: 1}
			one.Upsert(e)
			e = one.Keys[0]
			entries[target.DBRelPath] = e
			result.Keys = append(result.Keys, EnsureResult{Target: target, Entry: e})
			captured++
			continue
		}
		if e, ok := entries[target.DBRelPath]; ok && readErr == nil && strings.EqualFold(e.Salt, saltHex) && decrypt.ValidateRawHexKey(page, e.Key) {
			result.Keys = append(result.Keys, EnsureResult{Target: target, Entry: e, Reused: true})
			if hasCandidate {
				result.Warnings = append(result.Warnings, EnsureWarning{target.DBRelPath, "candidate rejected; retained verified existing key"})
			}
			continue
		}
		delete(entries, target.DBRelPath)
		if required[target.DBRelPath] {
			missing = append(missing, target.DBRelPath)
		} else {
			result.Warnings = append(result.Warnings, EnsureWarning{target.DBRelPath, "auxiliary key unavailable or invalid; skipped"})
		}
	}
	if len(missing) > 0 {
		return Store{}, result, fmt.Errorf("required keys missing or invalid: %s; existing key store unchanged", strings.Join(missing, ", "))
	}
	if captured == 0 {
		return Store{}, result, fmt.Errorf("no newly captured valid keys; existing key store unchanged")
	}
	merged := Store{Version: 1}
	for _, e := range entries {
		merged.Keys = append(merged.Keys, e)
	}
	return merged, result, nil
}
