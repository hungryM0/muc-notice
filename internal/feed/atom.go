package feed

import (
	"encoding/xml"
	"time"

	"muc-notice/internal/notice"
)

const atomNamespace = "http://www.w3.org/2005/Atom"

// Atom renders notifications as an Atom 1.0 document.
func Atom(metadata Metadata, notices []notice.Notice) ([]byte, error) {
	path := metadata.FeedPath
	if path == "" {
		path = AtomPath
	}
	document := atomFeed{
		XMLNS:    atomNamespace,
		ID:       metadata.URL(path),
		Title:    metadata.Title,
		Subtitle: metadata.Description,
		Updated:  latestUpdate(notices).Format(time.RFC3339),
		Links: []atomLink{
			{Rel: "self", Href: metadata.URL(path), Type: "application/atom+xml"},
			{Rel: "alternate", Href: metadata.BaseURL},
		},
		Entries: make([]atomEntry, 0, len(notices)),
	}
	for _, item := range notices {
		entry := atomEntry{
			ID:        item.ID,
			Title:     item.Title,
			Published: item.PublishedAt.UTC().Format(time.RFC3339),
			Updated:   item.UpdatedAt.UTC().Format(time.RFC3339),
			Category:  atomCategory{Term: item.TypeName, Label: item.TypeName},
		}
		if item.Link != "" {
			entry.Link = &atomLink{Rel: "alternate", Href: item.Link}
		}
		document.Entries = append(document.Entries, entry)
	}
	contents, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), contents...), nil
}

type atomFeed struct {
	XMLName  xml.Name    `xml:"feed"`
	XMLNS    string      `xml:"xmlns,attr"`
	ID       string      `xml:"id"`
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle,omitempty"`
	Updated  string      `xml:"updated"`
	Links    []atomLink  `xml:"link"`
	Entries  []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr,omitempty"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr,omitempty"`
}

type atomEntry struct {
	ID        string       `xml:"id"`
	Title     string       `xml:"title"`
	Link      *atomLink    `xml:"link,omitempty"`
	Published string       `xml:"published"`
	Updated   string       `xml:"updated"`
	Category  atomCategory `xml:"category"`
}
