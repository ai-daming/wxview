package media

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wxview/internal/sqlitedb"
)

type resourceMediaFile struct {
	Key        string
	Name       string
	Size       int64
	ModifyTime int64
	Path       string
}

type resourceMediaRow struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	ModifyTime   int64  `json:"modify_time"`
	Dir1         string `json:"dir1"`
	Dir2         string `json:"dir2"`
	RelativePath string `json:"relative_path"`
}

func queryResourceMedia(ctx context.Context, dbPaths []string, dataDir string, mediaType string, keys []string) []resourceMediaFile {
	keys = cleanMediaKeys(keys)
	if len(keys) == 0 {
		return nil
	}
	var out []resourceMediaFile
	for _, dbPath := range dbPaths {
		sqlitedb.ObserveRead(ctx, dbPath)
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		out = append(out, queryHardlinkMedia(ctx, dbPath, dataDir, mediaType, keys)...)
		out = append(out, queryHlinkMedia(ctx, dbPath, dataDir, keys)...)
	}
	return dedupeResourceFiles(out)
}

func queryHardlinkMedia(ctx context.Context, dbPath string, dataDir string, mediaType string, keys []string) []resourceMediaFile {
	tablePrefix := ""
	switch mediaType {
	case "image":
		tablePrefix = "image"
	case "video":
		tablePrefix = "video"
	case "file":
		tablePrefix = "file"
	default:
		return nil
	}
	accountBase := filepath.Dir(dataDir)
	condition, args := resourceKeyCondition(keys, "f.md5", "f.file_name")
	var out []resourceMediaFile
	for _, table := range []string{tablePrefix + "_hardlink_info_v3", tablePrefix + "_hardlink_info_v4"} {
		query := fmt.Sprintf(`
SELECT
  COALESCE(f.md5, ''),
  COALESCE(f.file_name, ''),
  COALESCE(f.file_size, 0),
  COALESCE(f.modify_time, 0),
  COALESCE(d1.username, ''),
  COALESCE(d2.username, ''),
  ''
FROM %s f
LEFT JOIN dir2id d1 ON d1.rowid = f.dir1
LEFT JOIN dir2id d2 ON d2.rowid = f.dir2
WHERE %s;
`, sqlitedb.QuoteIdent(table), condition)
		rows := queryResourceRows(ctx, dbPath, query, args...)
		for _, row := range rows {
			path := hardlinkPath(accountBase, mediaType, row)
			if path == "" {
				continue
			}
			out = append(out, resourceMediaFile{
				Key:        row.Key,
				Name:       row.Name,
				Size:       row.Size,
				ModifyTime: row.ModifyTime,
				Path:       path,
			})
		}
	}
	return out
}

func queryHlinkMedia(ctx context.Context, dbPath string, dataDir string, keys []string) []resourceMediaFile {
	accountBase := filepath.Dir(dataDir)
	condition, args := resourceKeyCondition(keys, "r.mediaMd5", "d.fileName")
	query := fmt.Sprintf(`
SELECT
  COALESCE(r.mediaMd5, ''),
  COALESCE(d.fileName, ''),
  COALESCE(r.mediaSize, 0),
  COALESCE(r.modifyTime, 0),
  '',
  '',
  COALESCE(d.relativePath, '')
FROM HlinkMediaRecord r
JOIN HlinkMediaDetail d ON r.inodeNumber = d.inodeNumber
WHERE %s;
`, condition)
	rows := queryResourceRows(ctx, dbPath, query, args...)
	out := make([]resourceMediaFile, 0, len(rows))
	for _, row := range rows {
		if row.RelativePath == "" || row.Name == "" {
			continue
		}
		out = append(out, resourceMediaFile{
			Key:        row.Key,
			Name:       row.Name,
			Size:       row.Size,
			ModifyTime: row.ModifyTime,
			Path:       filepath.Join(accountBase, "Message", "MessageTemp", filepath.FromSlash(row.RelativePath), row.Name),
		})
	}
	return out
}

func queryResourceRows(ctx context.Context, dbPath string, query string, args ...any) []resourceMediaRow {
	db, err := sqlitedb.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []resourceMediaRow{}
	for rows.Next() {
		var row resourceMediaRow
		if err := rows.Scan(&row.Key, &row.Name, &row.Size, &row.ModifyTime, &row.Dir1, &row.Dir2, &row.RelativePath); err != nil {
			return nil
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil && err != sql.ErrNoRows {
		return nil
	}
	return out
}

func hardlinkPath(accountBase string, mediaType string, row resourceMediaRow) string {
	if row.Name == "" {
		return ""
	}
	switch mediaType {
	case "image":
		if row.Dir1 == "" || row.Dir2 == "" {
			return ""
		}
		return filepath.Join(accountBase, "msg", "attach", row.Dir1, row.Dir2, "Img", row.Name)
	case "video":
		if row.Dir1 == "" {
			return ""
		}
		return filepath.Join(accountBase, "msg", "video", row.Dir1, row.Name)
	case "file":
		if row.Dir1 == "" {
			return ""
		}
		return filepath.Join(accountBase, "msg", "file", row.Dir1, row.Name)
	default:
		return ""
	}
}

func resourceKeyCondition(keys []string, keyColumn string, nameColumn string) (string, []any) {
	parts := make([]string, 0, len(keys)*2)
	args := make([]any, 0, len(keys)*2)
	for _, key := range keys {
		parts = append(parts, keyColumn+" = ?")
		args = append(args, key)
		parts = append(parts, nameColumn+" LIKE ? || '%'")
		args = append(args, key)
	}
	return strings.Join(parts, " OR "), args
}

func cleanMediaKeys(keys []string) []string {
	var out []string
	for _, key := range keys {
		key = strings.ToLower(strings.TrimSpace(key))
		if len(key) < 6 {
			continue
		}
		valid := true
		for _, r := range key {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			valid = false
			break
		}
		if valid && !contains(out, key) {
			out = append(out, key)
		}
	}
	return out
}

func dedupeResourceFiles(files []resourceMediaFile) []resourceMediaFile {
	seen := map[string]bool{}
	out := make([]resourceMediaFile, 0, len(files))
	for _, file := range files {
		if file.Path == "" || seen[file.Path] {
			continue
		}
		seen[file.Path] = true
		out = append(out, file)
	}
	return out
}
