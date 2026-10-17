// Package store persists notification history in SQLite.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"muc-notice/internal/notice"

	_ "modernc.org/sqlite"
)

// SQLite stores notification history in a local database.
type SQLite struct {
	db *sql.DB
}

// Open creates or opens the SQLite database at path.
func Open(ctx context.Context, path string) (*SQLite, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o750); err != nil {
		return nil, fmt.Errorf("create SQLite directory: %w", err)
	}

	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	dsn := sqliteDSN(absPath, query)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect SQLite database: %w", err)
	}
	if err := os.Chmod(absPath, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("set SQLite permissions: %w", err)
	}

	storage := &SQLite{db: db}
	if err := storage.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return storage, nil
}

func sqliteDSN(path string, query url.Values) string {
	sqlitePath := filepath.ToSlash(path)
	if !strings.HasPrefix(sqlitePath, "/") {
		sqlitePath = "/" + sqlitePath
	}
	return (&url.URL{Scheme: "file", Path: sqlitePath, RawQuery: query.Encode()}).String()
}

func (s *SQLite) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS notices (
    id TEXT PRIMARY KEY,
    type INTEGER NOT NULL,
    type_name TEXT NOT NULL,
    title TEXT NOT NULL,
    link TEXT NOT NULL,
    published_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS notices_published_at_idx ON notices(published_at DESC);
CREATE TABLE IF NOT EXISTS telegram_baselines (
    chat_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    PRIMARY KEY (chat_id, type)
);
CREATE TABLE IF NOT EXISTS telegram_messages (
    chat_id INTEGER NOT NULL,
    notice_id TEXT NOT NULL,
    type INTEGER NOT NULL,
    title TEXT NOT NULL,
    link TEXT NOT NULL,
    published_at TEXT NOT NULL,
    delivered_at TEXT,
    PRIMARY KEY (chat_id, notice_id)
);
CREATE INDEX IF NOT EXISTS telegram_pending_idx ON telegram_messages(chat_id, published_at) WHERE delivered_at IS NULL;
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize SQLite schema: %w", err)
	}
	return nil
}

// Sync upserts fetched records and prunes entries older than cutoff atomically.
func (s *SQLite) Sync(ctx context.Context, notices []notice.Notice, cutoff, observedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin notification sync: %w", err)
	}
	defer tx.Rollback()

	const upsert = `
INSERT INTO notices (
    id, type, type_name, title, link, published_at, updated_at, first_seen_at, last_seen_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    type = excluded.type,
    type_name = excluded.type_name,
    title = excluded.title,
    link = excluded.link,
    published_at = excluded.published_at,
    updated_at = excluded.updated_at,
    last_seen_at = excluded.last_seen_at
`
	seen := formatTime(observedAt)
	for _, item := range notices {
		if _, err := tx.ExecContext(ctx, upsert,
			item.ID,
			item.Type,
			item.TypeName,
			item.Title,
			item.Link,
			formatTime(item.PublishedAt),
			formatTime(item.UpdatedAt),
			seen,
			seen,
		); err != nil {
			return fmt.Errorf("upsert notification %q: %w", item.ID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM notices WHERE published_at < ?`, formatTime(cutoff)); err != nil {
		return fmt.Errorf("prune notification history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notification sync: %w", err)
	}
	return nil
}

// List returns recent notifications of the selected types ordered newest first.
func (s *SQLite) List(ctx context.Context, cutoff time.Time, noticeTypes []int, limit int) ([]notice.Notice, error) {
	query := `
SELECT id, type, type_name, title, link, published_at, updated_at
FROM notices
WHERE published_at >= ?`
	args := []any{formatTime(cutoff)}
	if len(noticeTypes) > 0 {
		placeholders := make([]string, 0, len(noticeTypes))
		for _, noticeType := range noticeTypes {
			placeholders = append(placeholders, "?")
			args = append(args, noticeType)
		}
		query += " AND type IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += `
ORDER BY published_at DESC, id ASC
LIMIT ?
`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query notification history: %w", err)
	}
	defer rows.Close()

	items := make([]notice.Notice, 0)
	for rows.Next() {
		var item notice.Notice
		var publishedAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.Type, &item.TypeName, &item.Title, &item.Link, &publishedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan notification history: %w", err)
		}
		item.PublishedAt, err = parseTime(publishedAt)
		if err != nil {
			return nil, fmt.Errorf("parse stored publication time: %w", err)
		}
		item.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse stored update time: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification history: %w", err)
	}
	return items, nil
}

// Close releases the database connection.
func (s *SQLite) Close() error {
	return s.db.Close()
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
