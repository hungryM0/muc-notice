package notice

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var typeNames = map[int]string{
	9:    "校园新闻",
	9977: "活动报道",
	9978: "会议纪要",
	4:    "时政头条",
	5:    "办公通知",
	6:    "教学通知",
	8:    "科研通知",
	32:   "学工通知",
	10:   "就业信息",
	36:   "活动报道",
	11:   "公示公告",
	3:    "民委要闻",
	9980: "学术信息",
	9979: "民大要情",
}

// Notice is the format-independent notification stored and rendered by the service.
type Notice struct {
	ID          string
	PortalID    string
	Type        int
	TypeName    string
	Title       string
	Link        string
	PublishedAt time.Time
	UpdatedAt   time.Time
}

// TypeName returns the portal display name for a notification category.
func TypeName(noticeType int) string {
	if name, ok := typeNames[noticeType]; ok {
		return name
	}
	return fmt.Sprintf("类型%d", noticeType)
}

// Normalize prepares one portal record for stable storage and feed rendering.
func Normalize(item Notice, baseURL string) Notice {
	item.TypeName = TypeName(item.Type)
	item.Title = strings.TrimSpace(item.Title)
	if item.Title == "" {
		item.Title = "无标题"
	}
	item.Link = absoluteHTTPURL(item.Link, baseURL)
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.PublishedAt
	}
	item.ID = stableID(item)
	return item
}

func absoluteHTTPURL(rawLink, baseURL string) string {
	rawLink = strings.TrimSpace(rawLink)
	if rawLink == "" {
		return ""
	}
	link, err := url.Parse(rawLink)
	if err != nil {
		return ""
	}
	if !link.IsAbs() {
		base, err := url.Parse(baseURL)
		if err != nil {
			return ""
		}
		link = base.ResolveReference(link)
	}
	if link.Scheme != "http" && link.Scheme != "https" {
		return ""
	}
	// The portal's Vue detail route lives in the URL fragment.
	if !strings.HasPrefix(link.Fragment, "/") {
		link.Fragment = ""
	}
	return link.String()
}

func stableID(item Notice) string {
	if strings.TrimSpace(item.PortalID) != "" {
		return hashURN("portal:" + strings.TrimSpace(item.PortalID))
	}
	if item.Link != "" {
		return item.Link
	}
	return hashURN(fmt.Sprintf("fallback:%d\x00%s\x00%s\x00%s", item.Type, item.Title, item.PublishedAt.UTC().Format(time.RFC3339Nano), item.Link))
}

func hashURN(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "urn:muc-notice:sha256:" + hex.EncodeToString(sum[:])
}
