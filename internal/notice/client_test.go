package notice

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFetchTypePaginatesAndSendsDateRange(t *testing.T) {
	var mu sync.Mutex
	var pages []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if len(r.PostForm) != 5 {
			t.Errorf("form fields = %v", r.PostForm)
		}
		for _, field := range []string{"currentPage", "pageSize", "type", "start_date", "end_date"} {
			if _, ok := r.PostForm[field]; !ok {
				t.Errorf("missing form field %q", field)
			}
		}
		if got := r.Form.Get("start_date"); got != "2026-07-19" {
			t.Errorf("start_date = %q", got)
		}
		if got := r.Form.Get("end_date"); got != "2026-08-17" {
			t.Errorf("end_date = %q", got)
		}
		if got := r.Form.Get("type"); got != "5" {
			t.Errorf("type = %q", got)
		}
		page, _ := strconv.Atoi(r.Form.Get("currentPage"))
		mu.Lock()
		pages = append(pages, page)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			fmt.Fprint(w, `{"state":true,"datas":{"tables":[{"notice_id":"a","notice_type":5,"notice_title":"A","notice_link":"/a","notice_release_time":"2026-08-17 10:00:00"},{"id":2,"notice_type":5,"notice_title":"B","notice_link":"/b","notice_release_time":"2026-08-16 09:00:00"}]}}`)
			return
		}
		fmt.Fprint(w, `{"state":true,"datas":{"tables":[{"noticeId":"c","notice_type":5,"notice_title":"C","notice_link":"/c","notice_release_time":"2026-08-15 08:00:00"}]}}`)
	}))
	defer server.Close()

	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(server.Client(), server.URL, nil, 2, location)
	start := time.Date(2026, 7, 19, 0, 0, 0, 0, location)
	end := time.Date(2026, 8, 17, 0, 0, 0, 0, location)
	items, err := client.FetchType(t.Context(), 5, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if items[0].PortalID != "a" || items[1].PortalID != "2" || items[2].PortalID != "c" {
		t.Fatalf("portal IDs = %q, %q, %q", items[0].PortalID, items[1].PortalID, items[2].PortalID)
	}
	if items[0].PublishedAt.Location() != location {
		t.Fatalf("location = %v", items[0].PublishedAt.Location())
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(pages) != "[1 2]" {
		t.Fatalf("pages = %v", pages)
	}
}

func TestFetchTypeStopsAtPageLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"state":true,"datas":{"tables":[{"notice_type":5,"notice_title":"A","notice_release_time":"2026-08-17 10:00:00"}]}}`)
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL, nil, 1, time.UTC)
	client.maxPages = 2
	_, err := client.FetchType(t.Context(), 5, time.Now(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "exceeded 2-page limit") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchTypeRecognizesExpiredSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, " ERROR_COMSYS_SESSION_INVALID: expired")
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL, nil, 20, time.UTC)
	_, err := client.FetchType(t.Context(), 5, time.Now(), time.Now())
	if !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchTypeRecognizesLoginRedirectWithoutFollowingIt(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "/user/simpleSSOLogin", http.StatusFound)
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL, nil, 20, time.UTC)
	_, err := client.FetchType(t.Context(), 5, time.Now(), time.Now())
	if !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatalf("err = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestFetchTypeRejectsInvalidPublicationTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"state":true,"datas":{"tables":[{"notice_type":5,"notice_title":"A","notice_release_time":"invalid"}]}}`)
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL, nil, 20, time.UTC)
	_, err := client.FetchType(t.Context(), 5, time.Now(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "publication time") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchTypeAcceptsPublicationTimeWithoutSeconds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"state":true,"datas":{"tables":[{"notice_type":5,"notice_title":"A","notice_release_time":"2026-08-17 10:00"}]}}`)
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL, nil, 20, time.UTC)
	items, err := client.FetchType(t.Context(), 5, time.Now(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PublishedAt.Second() != 0 {
		t.Fatalf("items = %#v", items)
	}
}

func TestFetchTypeRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBodyBytes+1)))
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL, nil, 20, time.UTC)
	_, err := client.FetchType(t.Context(), 5, time.Now(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "exceeds 8 MiB limit") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchTypeRejectsNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream failed", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL, nil, 20, time.UTC)
	_, err := client.FetchType(t.Context(), 5, time.Now(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") {
		t.Fatalf("err = %v", err)
	}
}
