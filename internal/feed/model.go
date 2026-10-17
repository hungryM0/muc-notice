// Package feed renders format-independent notifications as Atom and RSS.
package feed

import (
	"strings"
	"time"

	"muc-notice/internal/notice"
)

const (
	AtomPath = "/notices.atom"
	RSSPath  = "/notices.rss"
)

// Category identifies one homepage notification board and its public slug.
type Category struct {
	Slug string
	Name string
	Type int
}

var categories = []Category{
	{Slug: "campus-news", Name: "校园新闻", Type: 9},
	{Slug: "activity-reports", Name: "活动报道", Type: 9977},
	{Slug: "meeting-minutes", Name: "会议纪要", Type: 9978},
	{Slug: "current-affairs", Name: "时政头条", Type: 4},
	{Slug: "office", Name: "办公通知", Type: 5},
	{Slug: "research", Name: "科研通知", Type: 8},
	{Slug: "teaching", Name: "教学通知", Type: 6},
	{Slug: "student-affairs", Name: "学工通知", Type: 32},
	{Slug: "employment", Name: "就业信息", Type: 10},
	{Slug: "campus-events", Name: "活动报道", Type: 36},
	{Slug: "public-announcements", Name: "公示公告", Type: 11},
	{Slug: "academic", Name: "学术信息", Type: 9980},
	{Slug: "muc-highlights", Name: "民大要情", Type: 9979},
	{Slug: "minzu-news", Name: "民委要闻", Type: 3},
}

// Categories returns the supported homepage notification boards.
func Categories() []Category {
	return append([]Category(nil), categories...)
}

// CategoryPath returns the stable public path for one category and format.
func CategoryPath(category Category, extension string) string {
	return "/feeds/" + category.Slug + "." + extension
}

// LookupCategoryPath resolves a public category feed path.
func LookupCategoryPath(path string) (Category, string, bool) {
	for _, category := range categories {
		if path == CategoryPath(category, "atom") {
			return category, "atom", true
		}
		if path == CategoryPath(category, "rss") {
			return category, "rss", true
		}
	}
	return Category{}, "", false
}

var emptyFeedUpdated = time.Unix(0, 0).UTC()

// Metadata contains public information shared by both feed formats.
type Metadata struct {
	BaseURL     string
	Title       string
	Description string
	FeedPath    string
}

func (m Metadata) URL(path string) string {
	return strings.TrimRight(m.BaseURL, "/") + path
}

func latestUpdate(items []notice.Notice) time.Time {
	if len(items) == 0 {
		return emptyFeedUpdated
	}
	latest := items[0].UpdatedAt
	for _, item := range items[1:] {
		if item.UpdatedAt.After(latest) {
			latest = item.UpdatedAt
		}
	}
	return latest.UTC()
}
