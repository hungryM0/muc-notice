package feed

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"muc-notice/internal/notice"
)

func TestAtomAndRSSExposeEquivalentEntries(t *testing.T) {
	metadata := Metadata{BaseURL: "https://feeds.example.edu", Title: "民大 & 通知", Description: "公开订阅"}
	items := []notice.Notice{
		{
			ID:          "urn:muc-notice:1",
			TypeName:    "办公通知",
			Title:       "A < B & C",
			Link:        "https://portal.example.edu/notice/1?a=1&b=2",
			PublishedAt: time.Date(2026, 8, 17, 2, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 8, 17, 3, 0, 0, 0, time.UTC),
		},
	}

	atom, err := Atom(metadata, items)
	if err != nil {
		t.Fatal(err)
	}
	rss, err := RSS(metadata, items)
	if err != nil {
		t.Fatal(err)
	}
	for name, document := range map[string][]byte{"atom": atom, "rss": rss} {
		if err := xml.Unmarshal(document, new(any)); err != nil {
			t.Fatalf("%s XML: %v", name, err)
		}
		text := string(document)
		if !strings.Contains(text, "urn:muc-notice:1") || !strings.Contains(text, "办公通知") || !strings.Contains(text, "A &lt; B &amp; C") {
			t.Fatalf("%s document missing entry fields:\n%s", name, text)
		}
	}
	if !strings.Contains(string(atom), `rel="self" href="https://feeds.example.edu/notices.atom"`) {
		t.Fatalf("Atom self link missing:\n%s", atom)
	}
	if strings.Contains(string(atom), "<content") || strings.Contains(string(rss), "<item><description") {
		t.Fatal("feed contains notification body")
	}
}

func TestEmptyFeedsAreValidAndStable(t *testing.T) {
	metadata := Metadata{BaseURL: "https://feeds.example.edu", Title: "民大通知", Description: "公开订阅"}
	firstAtom, err := Atom(metadata, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondAtom, err := Atom(metadata, []notice.Notice{})
	if err != nil {
		t.Fatal(err)
	}
	if string(firstAtom) != string(secondAtom) {
		t.Fatal("empty Atom output is not stable")
	}
	if !strings.Contains(string(firstAtom), "1970-01-01T00:00:00Z") || strings.Contains(string(firstAtom), "<entry>") {
		t.Fatalf("invalid empty Atom:\n%s", firstAtom)
	}
	rss, err := RSS(metadata, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(rss, new(any)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rss), "<item>") {
		t.Fatalf("invalid empty RSS:\n%s", rss)
	}
}
