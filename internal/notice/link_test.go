package notice

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPortalInternalNoticeLink(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"state":true,"datas":{"tables":[{"notice_id":42,"notice_type":5,"notice_title":"Internal","notice_source":0,"notice_release_time":"2026-10-07 10:00"}]}}`)
	}))
	defer server.Close()
	client := NewClient(server.Client(), server.URL+"/getNoticeByPage", nil, 20, time.UTC)
	items, err := client.FetchType(context.Background(), 5, time.Now(), time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	item := Normalize(items[0], server.URL)
	if !strings.Contains(item.Link, "/#/print?notice_id=42&show_type=1&type=5") {
		t.Fatalf("detail route lost: %s", item.Link)
	}
}
