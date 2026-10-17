package store

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"muc-notice/internal/notice"
)

func TestSQLiteDSNUsesFileURIPath(t *testing.T) {
	query := url.Values{"_pragma": {"busy_timeout(5000)"}}
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "unix", path: "/tmp/notices.db", want: "file:///tmp/notices.db"},
		{name: "windows drive", path: "C:/work/notices.db", want: "file:///C:/work/notices.db"},
		{name: "escaped path", path: "C:/work dir/notices.db", want: "file:///C:/work%20dir/notices.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dsn := sqliteDSN(tt.path, query)
			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Host != "" {
				t.Fatalf("SQLite URI authority = %q", parsed.Host)
			}
			if !strings.HasPrefix(dsn, tt.want+"?") {
				t.Fatalf("SQLite DSN = %q, want prefix %q", dsn, tt.want+"?")
			}
		})
	}
}

func TestSQLiteSyncPrunesUpsertsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "notices.db")
	storage, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("database mode = %o", got)
		}
	}

	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -29)
	items := []notice.Notice{
		{ID: "old", Type: 5, TypeName: "办公通知", Title: "旧通知", Link: "https://example.edu/old", PublishedAt: cutoff.Add(-time.Second), UpdatedAt: cutoff.Add(-time.Second)},
		{ID: "newer", Type: 5, TypeName: "办公通知", Title: "原标题", Link: "https://example.edu/newer", PublishedAt: now, UpdatedAt: now},
		{ID: "new", Type: 6, TypeName: "教学通知", Title: "新通知", Link: "https://example.edu/new", PublishedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{ID: "other", Type: 9, TypeName: "校园新闻", Title: "其他板块", Link: "https://example.edu/other", PublishedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)},
	}
	if err := storage.Sync(t.Context(), items, cutoff, now); err != nil {
		t.Fatal(err)
	}
	items[1].Title = "更新后的标题"
	if err := storage.Sync(t.Context(), items[1:2], cutoff, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := storage.List(t.Context(), cutoff, []int{5, 6}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d", len(got))
	}
	if got[0].ID != "newer" || got[0].Title != "更新后的标题" || got[1].ID != "new" {
		t.Fatalf("got = %#v", got)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}

	storage, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	got, err = storage.List(t.Context(), cutoff, []int{5}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "newer" {
		t.Fatalf("persisted got = %#v", got)
	}
}
