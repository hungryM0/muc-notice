package notice

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeUsesPortalIDAndAbsoluteLink(t *testing.T) {
	publishedAt := time.Date(2026, 8, 17, 10, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	item := Normalize(Notice{
		PortalID:    "42",
		Type:        5,
		Title:       "  测试通知  ",
		Link:        "/notice/42#section",
		PublishedAt: publishedAt,
	}, "https://feeds.example.edu/base/")

	if item.TypeName != "办公通知" {
		t.Fatalf("TypeName = %q", item.TypeName)
	}
	if item.Title != "测试通知" {
		t.Fatalf("Title = %q", item.Title)
	}
	if item.Link != "https://feeds.example.edu/notice/42" {
		t.Fatalf("Link = %q", item.Link)
	}
	if !strings.HasPrefix(item.ID, "urn:muc-notice:sha256:") {
		t.Fatalf("ID = %q", item.ID)
	}
	if !item.UpdatedAt.Equal(publishedAt) {
		t.Fatalf("UpdatedAt = %v", item.UpdatedAt)
	}
}

func TestNormalizeFallsBackToLinkID(t *testing.T) {
	item := Normalize(Notice{
		Type:        999,
		Link:        "https://portal.example.edu/notice/1",
		PublishedAt: time.Unix(1, 0),
	}, "https://feeds.example.edu")

	if item.ID != item.Link {
		t.Fatalf("ID = %q, Link = %q", item.ID, item.Link)
	}
	if item.TypeName != "类型999" {
		t.Fatalf("TypeName = %q", item.TypeName)
	}
	if item.Title != "无标题" {
		t.Fatalf("Title = %q", item.Title)
	}
}

func TestNormalizeRejectsUnsafeLink(t *testing.T) {
	item := Normalize(Notice{Type: 5, Link: "javascript:alert(1)", PublishedAt: time.Unix(1, 0)}, "https://feeds.example.edu")
	if item.Link != "" {
		t.Fatalf("Link = %q", item.Link)
	}
	if !strings.HasPrefix(item.ID, "urn:muc-notice:sha256:") {
		t.Fatalf("ID = %q", item.ID)
	}
}
