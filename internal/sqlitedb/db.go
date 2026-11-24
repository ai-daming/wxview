package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const BusyTimeoutMS = 30000

type readObserverKey struct{}

// WithReadObserver observes database paths immediately before read-only opens.
// It is request-scoped and never changes connection or query semantics.
func WithReadObserver(ctx context.Context, observe func(string)) context.Context {
	return context.WithValue(ctx, readObserverKey{}, observe)
}

// ObserveRead also covers a dependency that cannot be opened (for example an
// optional media cache known from the source directory but missing locally).
func ObserveRead(ctx context.Context, path string) {
	if observe, ok := ctx.Value(readObserverKey{}).(func(string)); ok {
		observe(path)
	}
}

func OpenReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("sqlite path is empty")
	}
	ObserveRead(ctx, path)
	db, err := sql.Open("sqlite", FileURI(path, url.Values{
		"mode":      {"ro"},
		"immutable": {"1"},
	}))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func OpenReadOnlyLive(ctx context.Context, path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("sqlite path is empty")
	}
	ObserveRead(ctx, path)
	db, err := sql.Open("sqlite", FileURI(path, url.Values{"mode": {"ro"}}))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d;", BusyTimeoutMS)); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func OpenReadWrite(ctx context.Context, path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", FileURI(path, url.Values{"mode": {"rwc"}}))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d;", BusyTimeoutMS)); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA synchronous=NORMAL;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func FileURI(path string, values url.Values) string {
	path = filepath.ToSlash(path)
	if vol := filepath.VolumeName(path); vol != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path}
	if len(values) > 0 {
		u.RawQuery = values.Encode()
	}
	return u.String()
}

func ExecScript(ctx context.Context, db *sql.DB, script string) error {
	_, err := db.ExecContext(ctx, script)
	return err
}

func TableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=? LIMIT 1;", table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]string, n)
	for i := range out {
		out[i] = "?"
	}
	return strings.Join(out, ",")
}
