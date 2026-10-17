package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"muc-notice/internal/config"
	"muc-notice/internal/feed"
	"muc-notice/internal/service"
	"muc-notice/internal/store"
)

func TestPortalToSQLiteToAtom(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	releaseTime := time.Now().In(location).Format("2006-01-02 15:04:05")
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("start_date") == "" || r.Form.Get("end_date") == "" {
			t.Fatal("date range is missing")
		}
		fmt.Fprintf(w, `{"state":true,"datas":{"tables":[{"notice_id":"integration","notice_type":5,"notice_title":"跨层测试通知","notice_link":"/notice/integration","notice_release_time":%q}]}}`, releaseTime)
	}))
	defer portal.Close()

	storage, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "notices.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	cfg := config.Default()
	cfg.URL = portal.URL
	cfg.Feed.BaseURL = "https://feeds.example.edu"
	cfg.NoticeTypes = []int{5}
	cfg.Cookies = map[string]string{"JSESSIONID": "stored"}
	noticeService, err := service.NewNoticeService(filepath.Join(t.TempDir(), "config.toml"), cfg, storage, portal.Client())
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(noticeService, feed.Metadata{
		BaseURL:     cfg.Feed.BaseURL,
		Title:       cfg.Feed.Title,
		Description: cfg.Feed.Description,
	}, cfg.Server.CacheTTLSeconds)

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, feed.AtomPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "跨层测试通知") || !strings.Contains(body, "https://feeds.example.edu/notice/integration") {
		t.Fatalf("Atom body missing persisted notification:\n%s", body)
	}
}
