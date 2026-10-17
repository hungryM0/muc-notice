package feed

import (
	"encoding/xml"
	"time"

	"muc-notice/internal/notice"
)

// RSS renders notifications as an RSS 2.0 document.
func RSS(metadata Metadata, notices []notice.Notice) ([]byte, error) {
	document := rssDocument{
		Version: "2.0",
		Channel: rssChannel{
			Title:       metadata.Title,
			Link:        metadata.BaseURL,
			Description: metadata.Description,
			Items:       make([]rssItem, 0, len(notices)),
		},
	}
	if len(notices) > 0 {
		document.Channel.LastBuildDate = latestUpdate(notices).Format(time.RFC1123Z)
	}
	for _, item := range notices {
		document.Channel.Items = append(document.Channel.Items, rssItem{
			Title:    item.Title,
			Link:     item.Link,
			GUID:     rssGUID{IsPermaLink: "false", Value: item.ID},
			PubDate:  item.PublishedAt.UTC().Format(time.RFC1123Z),
			Category: item.TypeName,
		})
	}
	contents, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), contents...), nil
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	Items         []rssItem `xml:"item"`
}

type rssItem struct {
	Title    string  `xml:"title"`
	Link     string  `xml:"link,omitempty"`
	GUID     rssGUID `xml:"guid"`
	PubDate  string  `xml:"pubDate"`
	Category string  `xml:"category"`
}

type rssGUID struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}
