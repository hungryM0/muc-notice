package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"muc-notice/internal/config"
	"muc-notice/internal/notice"
)

func TestGetCachesSuccessfulRefresh(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writePortalPage(w, 5, "one", "测试通知", "2026-08-17 10:00:00")
	}))
	defer server.Close()

	storage := newMemoryStore()
	service := newTestService(t, server, storage, []int{5})
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, service.location)
	service.now = func() time.Time { return now }

	first, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 || requests.Load() != 1 {
		t.Fatalf("first=%d second=%d requests=%d", len(first), len(second), requests.Load())
	}

	now = now.Add(6 * time.Minute)
	if _, err := service.Get(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests after expiration = %d", requests.Load())
	}
}

func TestGetDoesNotLoginForSuccessfulEmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"state":true,"datas":{"tables":[]}}`)
	}))
	defer server.Close()

	storage := newMemoryStore()
	service := newTestService(t, server, storage, []int{5})
	service.cfg.Username = "user"
	service.cfg.Password = "password"
	var loginCalls atomic.Int32
	service.login = func(context.Context, *http.Client, string, string) ([]*http.Cookie, error) {
		loginCalls.Add(1)
		return nil, errors.New("login must not be called")
	}

	items, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || loginCalls.Load() != 0 {
		t.Fatalf("items=%d loginCalls=%d", len(items), loginCalls.Load())
	}
}

func TestGetUsesPersistedHistoryAfterSuccessfulEmptyRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"state":true,"datas":{"tables":[]}}`)
	}))
	defer server.Close()

	storage := newMemoryStore()
	publishedAt := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	storage.items["stored"] = notice.Notice{
		ID:          "stored",
		Type:        5,
		TypeName:    "办公通知",
		Title:       "数据库中的通知",
		PublishedAt: publishedAt,
		UpdatedAt:   publishedAt,
	}
	service := newTestService(t, server, storage, []int{5})

	items, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "stored" {
		t.Fatalf("items = %#v", items)
	}
}

func TestGetExcludesUnconfiguredStoredTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"state":true,"datas":{"tables":[]}}`)
	}))
	defer server.Close()

	storage := newMemoryStore()
	publishedAt := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	storage.items["campus-news"] = notice.Notice{
		ID:          "campus-news",
		Type:        9,
		TypeName:    "校园新闻",
		Title:       "不属于中间通知栏",
		PublishedAt: publishedAt,
		UpdatedAt:   publishedAt,
	}
	service := newTestService(t, server, storage, []int{5, 8, 6, 32})

	items, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %#v", items)
	}
}

func TestGetReturnsOnlySuccessfulTypesOnPartialFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("type") == "6" {
			http.Error(w, "failed", http.StatusBadGateway)
			return
		}
		writePortalPage(w, 5, "office", "办公通知", "2026-08-17 10:00:00")
	}))
	defer server.Close()

	storage := newMemoryStore()
	failedTypeTime := time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC)
	storage.items["teaching"] = notice.Notice{ID: "teaching", Type: 6, TypeName: "教学通知", Title: "旧教学通知", PublishedAt: failedTypeTime, UpdatedAt: failedTypeTime}
	service := newTestService(t, server, storage, []int{5, 6})

	items, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Type != 5 {
		t.Fatalf("items = %#v", items)
	}
}

func TestGetReturnsUnavailableWithoutServingStoredHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed", http.StatusBadGateway)
	}))
	defer server.Close()

	storage := newMemoryStore()
	storedAt := time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC)
	storage.items["stored"] = notice.Notice{ID: "stored", Type: 5, PublishedAt: storedAt, UpdatedAt: storedAt}
	service := newTestService(t, server, storage, []int{5})

	items, err := service.Get(t.Context())
	if !errors.Is(err, ErrPortalUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if items != nil || storage.listCalls != 0 {
		t.Fatalf("items=%#v listCalls=%d", items, storage.listCalls)
	}
}

func TestGetLogsInBeforeFetchingWithoutCookies(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		cookie, err := r.Cookie("JSESSIONID")
		if err != nil || cookie.Value != "fresh" {
			t.Errorf("request did not use refreshed session")
		}
		writePortalPage(w, 5, "fresh", "登录后通知", "2026-08-17 10:00:00")
	}))
	defer server.Close()

	service := newTestService(t, server, newMemoryStore(), []int{5})
	service.cfg.Cookies = map[string]string{}
	service.cfg.Username = "user"
	service.cfg.Password = "password"
	var loginCalls atomic.Int32
	service.login = func(context.Context, *http.Client, string, string) ([]*http.Cookie, error) {
		if requests.Load() != 0 {
			t.Fatal("portal was queried before login")
		}
		loginCalls.Add(1)
		return []*http.Cookie{{Name: "JSESSIONID", Value: "fresh", Path: "/"}}, nil
	}
	service.saveCookies = func(string, map[string]string) error { return nil }

	items, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || loginCalls.Load() != 1 || requests.Load() != 1 {
		t.Fatalf("items=%d login=%d requests=%d", len(items), loginCalls.Load(), requests.Load())
	}
}

func TestGetWithoutCookiesOrCredentialsDoesNotQueryPortal(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	service := newTestService(t, server, newMemoryStore(), []int{5})
	service.cfg.Cookies = map[string]string{}
	_, err := service.Get(t.Context())
	if !errors.Is(err, ErrLoginFailed) {
		t.Fatalf("err = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestGetRelogsOnlyAfterExplicitSessionExpiry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		cookie, err := r.Cookie("JSESSIONID")
		if err != nil || cookie.Value != "fresh" {
			fmt.Fprint(w, "error_comsys_session_invalid")
			return
		}
		writePortalPage(w, 5, "fresh", "登录后通知", "2026-08-17 10:00:00")
	}))
	defer server.Close()

	storage := newMemoryStore()
	service := newTestService(t, server, storage, []int{5, 6})
	service.cfg.Username = "user"
	service.cfg.Password = "password"
	var loginCalls, saveCalls atomic.Int32
	service.login = func(context.Context, *http.Client, string, string) ([]*http.Cookie, error) {
		if requests.Load() != 1 {
			t.Fatalf("requests before login = %d", requests.Load())
		}
		loginCalls.Add(1)
		return []*http.Cookie{{Name: "JSESSIONID", Value: "fresh", Path: "/"}}, nil
	}
	service.saveCookies = func(string, map[string]string) error {
		saveCalls.Add(1)
		return nil
	}

	items, err := service.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || loginCalls.Load() != 1 || saveCalls.Load() != 1 || requests.Load() != 3 {
		t.Fatalf("items=%d login=%d save=%d requests=%d", len(items), loginCalls.Load(), saveCalls.Load(), requests.Load())
	}
}

func TestGetCoalescesConcurrentRefreshes(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		writePortalPage(w, 5, "one", "测试通知", "2026-08-17 10:00:00")
	}))
	defer server.Close()

	service := newTestService(t, server, newMemoryStore(), []int{5})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Get(t.Context())
			errs <- err
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestHistoryCutoffIncludesThirtyShanghaiCalendarDays(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 23, 59, 0, 0, location)
	got := historyCutoff(now, location, 30)
	want := time.Date(2026, 7, 19, 0, 0, 0, 0, location)
	if !got.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", got, want)
	}
}

func newTestService(t *testing.T, server *httptest.Server, storage NoticeStore, noticeTypes []int) *NoticeService {
	t.Helper()
	cfg := config.Default()
	cfg.URL = server.URL
	cfg.Feed.BaseURL = "https://feeds.example.edu"
	cfg.NoticeTypes = noticeTypes
	cfg.Cookies = map[string]string{"JSESSIONID": "stored"}
	service, err := NewNoticeService("config.toml", cfg, storage, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, service.location)
	service.now = func() time.Time { return now }
	return service
}

func writePortalPage(w http.ResponseWriter, noticeType int, id, title, releaseTime string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"state":true,"datas":{"tables":[{"notice_id":%q,"notice_type":%d,"notice_title":%q,"notice_link":%q,"notice_release_time":%q}]}}`, id, noticeType, title, "/notice/"+id, releaseTime)
}

type memoryStore struct {
	mu        sync.Mutex
	items     map[string]notice.Notice
	listCalls int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{items: make(map[string]notice.Notice)}
}

func (s *memoryStore) Sync(_ context.Context, items []notice.Notice, cutoff, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, item := range s.items {
		if item.PublishedAt.Before(cutoff) {
			delete(s.items, id)
		}
	}
	for _, item := range items {
		s.items[item.ID] = item
	}
	return nil
}

func (s *memoryStore) List(_ context.Context, cutoff time.Time, noticeTypes []int, limit int) ([]notice.Notice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	allowedTypes := make(map[int]struct{}, len(noticeTypes))
	for _, noticeType := range noticeTypes {
		allowedTypes[noticeType] = struct{}{}
	}
	items := make([]notice.Notice, 0, len(s.items))
	for _, item := range s.items {
		_, allowed := allowedTypes[item.Type]
		if !item.PublishedAt.Before(cutoff) && allowed {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].PublishedAt.Equal(items[j].PublishedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return append([]notice.Notice(nil), items...), nil
}
