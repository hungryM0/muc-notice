package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"muc-notice/internal/config"
	"muc-notice/internal/notice"
	"muc-notice/internal/store"
)

func TestDedicatedSOCKS5TransportAndMessageLimits(t *testing.T) {
	client, err := NewClient("123:test", "socks5://198.18.169.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	transport := client.http.Transport.(*http.Transport)
	req, _ := http.NewRequest(http.MethodPost, "https://api.telegram.org", nil)
	proxy, err := transport.Proxy(req)
	if err != nil || proxy.String() != "socks5://198.18.169.1:1080" {
		t.Fatalf("proxy: %v %v", proxy, err)
	}
	if transport == http.DefaultTransport {
		t.Fatal("global transport modified")
	}
	if err := client.Send(context.Background(), -42, notice.Notice{Title: strings.Repeat("a", 4096), Link: "https://example.edu"}); err == nil {
		t.Fatal("oversized message accepted")
	}
	if err := client.Send(context.Background(), -42, notice.Notice{Title: "Missing link"}); err == nil {
		t.Fatal("missing link accepted")
	}
}

type refreshResult struct{ err error }

func (r *refreshResult) GetComplete(context.Context) error { return r.err }

func TestBaselineRetryAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "notices.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	now := time.Now()
	old := notice.Notice{ID: "old", Type: 5, Title: "Old", Link: "https://example.edu/old", PublishedAt: now, UpdatedAt: now}
	if err := db.Sync(ctx, []notice.Notice{old}, now.Add(-time.Hour), now); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var body struct {
			ChatID    int64  `json:"chat_id"`
			Text      string `json:"text"`
			ParseMode string `json:"parse_mode"`
			Preview   struct {
				Disabled bool `json:"is_disabled"`
			} `json:"link_preview_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ChatID != -42 || body.Text != "New\nhttps://example.edu/new" || body.ParseMode != "" || !body.Preview.Disabled {
			t.Errorf("unexpected message: %+v", body)
		}
		if attempts == 1 {
			w.WriteHeader(429)
			w.Write([]byte(`{"ok":false,"error_code":429,"parameters":{"retry_after":600}}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()
	sender := &Client{http: server.Client(), endpoint: server.URL}
	cfg := config.TelegramConfig{ChatID: -42, NoticeTypes: []int{5, 6, 32}, IntervalSeconds: 300}
	refresh := &refreshResult{}
	worker := NewWorker(cfg, refresh, db, sender)
	if err := worker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatal("historical notice was sent")
	}
	fresh := notice.Notice{ID: "new", Type: 6, Title: "New", Link: "https://example.edu/new", PublishedAt: now.Add(time.Minute), UpdatedAt: now}
	other := notice.Notice{ID: "research", Type: 8, Title: "Research", Link: "https://example.edu/research", PublishedAt: now, UpdatedAt: now}
	if err := db.Sync(ctx, []notice.Notice{fresh, other}, now.Add(-time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if err := worker.Check(ctx); err == nil {
		t.Fatal("429 accepted")
	}
	if err := worker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatal("retry delay ignored")
	}
	worker.retryAt = time.Time{}
	refresh.err = errors.New("portal unavailable")
	if err := worker.Check(ctx); err == nil {
		t.Fatal("portal error lost")
	}
	if attempts != 2 {
		t.Fatal("pending message not retried during portal failure")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	refresh.err = nil
	worker = NewWorker(cfg, refresh, db, sender)
	if err := worker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatal("delivered message repeated after restart")
	}
}

func TestIncompleteRefreshDoesNotEstablishBaseline(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "notices.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	refresh := &refreshResult{err: errors.New("partial")}
	worker := NewWorker(config.TelegramConfig{ChatID: -42, NoticeTypes: []int{5}}, refresh, db, nil)
	if err := worker.Check(ctx); err == nil {
		t.Fatal("partial refresh accepted")
	}
	now := time.Now()
	if err := db.Sync(ctx, []notice.Notice{{ID: "old", Type: 5, Title: "Old", Link: "https://example.edu", PublishedAt: now, UpdatedAt: now}}, now.Add(-time.Hour), now); err != nil {
		t.Fatal(err)
	}
	refresh.err = nil
	if err := worker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := db.PendingTelegram(ctx, -42, []int{5})
	if err != nil || len(items) != 0 {
		t.Fatalf("historical baseline failed: %v %v", items, err)
	}
}
