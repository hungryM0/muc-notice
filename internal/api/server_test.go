package api

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"muc-notice/internal/feed"
	"muc-notice/internal/notice"
	"muc-notice/internal/service"
)

func TestFeedRoutesAndETag(t *testing.T) {
	provider := &fakeProvider{items: []notice.Notice{{
		ID:          "urn:muc-notice:1",
		TypeName:    "办公通知",
		Title:       "测试通知",
		Link:        "https://portal.example.edu/1",
		PublishedAt: time.Date(2026, 8, 17, 2, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 8, 17, 2, 0, 0, 0, time.UTC),
	}}}
	server := NewServer(provider, feed.Metadata{BaseURL: "https://feeds.example.edu", Title: "民大通知", Description: "公开订阅"}, 300)

	for path, contentType := range map[string]string{
		feed.AtomPath: "application/atom+xml; charset=utf-8",
		feed.RSSPath:  "application/rss+xml; charset=utf-8",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		if got := response.Header().Get("Content-Type"); got != contentType {
			t.Fatalf("%s Content-Type = %q", path, got)
		}
		if err := xml.Unmarshal(response.Body.Bytes(), new(any)); err != nil {
			t.Fatalf("%s XML: %v", path, err)
		}
		etag := response.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("%s ETag is empty", path)
		}

		cachedRequest := httptest.NewRequest(http.MethodGet, path, nil)
		cachedRequest.Header.Set("If-None-Match", "W/"+etag)
		cachedResponse := httptest.NewRecorder()
		server.ServeHTTP(cachedResponse, cachedRequest)
		if cachedResponse.Code != http.StatusNotModified || cachedResponse.Body.Len() != 0 {
			t.Fatalf("%s cached status=%d body=%q", path, cachedResponse.Code, cachedResponse.Body.String())
		}
	}
}

func TestCategoryFeedRoutesFilterByType(t *testing.T) {
	provider := &fakeProvider{items: []notice.Notice{
		{ID: "office", Type: 5, TypeName: "办公通知", Title: "办公"},
		{ID: "research", Type: 8, TypeName: "科研通知", Title: "科研"},
	}}
	server := NewServer(provider, feed.Metadata{BaseURL: "https://feeds.example.edu", Title: "民大通知"}, 300)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/feeds/office.atom", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "办公") || strings.Contains(body, "科研") {
		t.Fatalf("filtered body = %s", body)
	}
	if !strings.Contains(body, "https://feeds.example.edu/feeds/office.atom") {
		t.Fatalf("self link missing: %s", body)
	}
}

func TestEveryPortalTypeHasEnglishFeedRoutes(t *testing.T) {
	server := NewServer(&fakeProvider{}, feed.Metadata{}, 300)
	for _, category := range feed.Categories() {
		for _, extension := range []string{"atom", "rss"} {
			path := feed.CategoryPath(category, extension)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("%s status = %d", path, response.Code)
			}
		}
	}
}

func TestUnknownCategoryFeedIsNotFound(t *testing.T) {
	server := NewServer(&fakeProvider{}, feed.Metadata{}, 300)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/feeds/campus.atom", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestEmptyFeedReturnsValidXML(t *testing.T) {
	server := NewServer(&fakeProvider{}, feed.Metadata{BaseURL: "https://feeds.example.edu", Title: "民大通知"}, 300)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, feed.AtomPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if err := xml.Unmarshal(response.Body.Bytes(), new(any)); err != nil {
		t.Fatal(err)
	}
}

func TestOldJSONRoutesAreGoneAndHealthRemains(t *testing.T) {
	server := NewServer(&fakeProvider{}, feed.Metadata{}, 300)

	health := httptest.NewRecorder()
	server.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || strings.TrimSpace(health.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("health status=%d body=%q", health.Code, health.Body.String())
	}
	for _, path := range []string{"/api/v1/notices/today", "/api/v1/notices/refresh"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}
}

func TestPortalFailureReturnsBadGatewayWithoutSensitiveDetails(t *testing.T) {
	provider := &fakeProvider{err: errors.New("wrapped: " + service.ErrPortalUnavailable.Error() + " secret-cookie")}
	server := NewServer(provider, feed.Metadata{}, 300)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, feed.RSSPath, nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("untyped error status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret-cookie") {
		t.Fatalf("sensitive detail leaked: %q", response.Body.String())
	}

	provider.err = service.ErrPortalUnavailable
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, feed.RSSPath, nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("portal error status = %d", response.Code)
	}
}

func TestFeedMethodNotAllowed(t *testing.T) {
	server := NewServer(&fakeProvider{}, feed.Metadata{}, 300)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, feed.AtomPath, nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status=%d Allow=%q", response.Code, response.Header().Get("Allow"))
	}
}

type fakeProvider struct {
	items []notice.Notice
	err   error
}

func (p *fakeProvider) Get(context.Context) ([]notice.Notice, error) {
	return append([]notice.Notice(nil), p.items...), p.err
}
