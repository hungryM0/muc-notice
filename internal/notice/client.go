// Package notice fetches and decodes portal notification records.
package notice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxResponseBodyBytes = 8 * 1024 * 1024
	defaultMaxPages      = 100
)

var portalTimeLayouts = []string{"2006-01-02 15:04:05", "2006-01-02 15:04"}

// ErrAuthenticationRequired marks the portal's bare session-expiry response.
var ErrAuthenticationRequired = errors.New("portal authentication required")

// Client holds the HTTP request settings for portal notice requests.
type Client struct {
	httpClient *http.Client
	endpoint   string
	headers    map[string]string
	pageSize   int
	maxPages   int
	location   *time.Location
}

// NewClient creates a notification client using the supplied HTTP session.
func NewClient(httpClient *http.Client, endpoint string, headers map[string]string, pageSize int, location *time.Location) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	session := *httpClient
	session.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if pageSize <= 0 {
		pageSize = 20
	}
	if location == nil {
		location = time.Local
	}

	clonedHeaders := make(map[string]string, len(headers))
	for name, value := range headers {
		clonedHeaders[name] = value
	}
	return &Client{
		httpClient: &session,
		endpoint:   endpoint,
		headers:    clonedHeaders,
		pageSize:   pageSize,
		maxPages:   defaultMaxPages,
		location:   location,
	}
}

// FetchType fetches every page for one notification type in the date range.
func (c *Client) FetchType(ctx context.Context, noticeType int, startDate, endDate time.Time) ([]Notice, error) {
	items := make([]Notice, 0)
	for page := 1; page <= c.maxPages; page++ {
		pageItems, err := c.fetchPage(ctx, noticeType, page, startDate, endDate)
		if err != nil {
			return nil, err
		}
		items = append(items, pageItems...)
		if len(pageItems) < c.pageSize {
			return items, nil
		}
	}
	return nil, fmt.Errorf("request type %d: exceeded %d-page limit", noticeType, c.maxPages)
}

func (c *Client) fetchPage(ctx context.Context, noticeType, page int, startDate, endDate time.Time) ([]Notice, error) {
	form := url.Values{
		"currentPage": {strconv.Itoa(page)},
		"pageSize":    {strconv.Itoa(c.pageSize)},
		"type":        {strconv.Itoa(noticeType)},
		"start_date":  {startDate.In(c.location).Format("2006-01-02")},
		"end_date":    {endDate.In(c.location).Format("2006-01-02")},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create type %d page %d request: %w", noticeType, page, err)
	}
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request type %d page %d: %w", noticeType, page, err)
	}
	defer resp.Body.Close()
	if isAuthenticationRedirect(resp) {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("request type %d page %d: %w", noticeType, page, ErrAuthenticationRequired)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("request type %d page %d: unexpected HTTP status %s", noticeType, page, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read type %d page %d response: %w", noticeType, page, err)
	}
	if len(body) > maxResponseBodyBytes {
		return nil, fmt.Errorf("decode type %d page %d response: response exceeds 8 MiB limit", noticeType, page)
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(body))), "error_comsys_session_invalid") {
		return nil, fmt.Errorf("request type %d page %d: %w", noticeType, page, ErrAuthenticationRequired)
	}

	var envelope struct {
		State bool `json:"state"`
		Datas struct {
			Tables []portalNotice `json:"tables"`
		} `json:"datas"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode type %d page %d response: %w", noticeType, page, err)
	}
	if !envelope.State {
		return nil, fmt.Errorf("request type %d page %d: portal returned unsuccessful state", noticeType, page)
	}

	items := make([]Notice, 0, len(envelope.Datas.Tables))
	for _, raw := range envelope.Datas.Tables {
		publishedAt, err := parsePortalTime(raw.ReleaseTime, c.location)
		if err != nil {
			return nil, fmt.Errorf("decode type %d page %d publication time %q: %w", noticeType, page, raw.ReleaseTime, err)
		}
		recordType := raw.Type
		if recordType == 0 {
			recordType = noticeType
		}
		link := raw.Link
		if strings.TrimSpace(link) == "" && raw.ID() != "" && (raw.Source == 0 || raw.LinkState == 1) {
			base, err := url.Parse(c.endpoint)
			if err != nil {
				return nil, fmt.Errorf("parse portal origin: %w", err)
			}
			params := url.Values{"type": {strconv.Itoa(recordType)}, "notice_id": {raw.ID()}, "show_type": {"1"}}
			base.Path, base.RawPath, base.RawQuery = "/", "", ""
			base.Fragment = "/print?" + params.Encode()
			link = base.String()
		}
		items = append(items, Notice{
			PortalID:    raw.ID(),
			Type:        recordType,
			Title:       raw.Title,
			Link:        link,
			PublishedAt: publishedAt,
			UpdatedAt:   publishedAt,
		})
	}
	return items, nil
}

func parsePortalTime(value string, location *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	var lastErr error
	for _, layout := range portalTimeLayouts {
		parsed, err := time.ParseInLocation(layout, value, location)
		if err == nil {
			return parsed, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}

func isAuthenticationRedirect(response *http.Response) bool {
	if response.StatusCode < http.StatusMultipleChoices || response.StatusCode >= http.StatusBadRequest {
		return false
	}
	location := strings.ToLower(response.Header.Get("Location"))
	return strings.Contains(location, "/user/simplessologin") || strings.Contains(location, "/zfca/login")
}

type portalNotice struct {
	Source      int             `json:"notice_source"`
	LinkState   int             `json:"notice_link_state"`
	NoticeID    json.RawMessage `json:"notice_id"`
	IDValue     json.RawMessage `json:"id"`
	CamelID     json.RawMessage `json:"noticeId"`
	Type        int             `json:"notice_type"`
	Title       string          `json:"notice_title"`
	Link        string          `json:"notice_link"`
	ReleaseTime string          `json:"notice_release_time"`
}

func (n portalNotice) ID() string {
	for _, raw := range []json.RawMessage{n.NoticeID, n.IDValue, n.CamelID} {
		if value := rawID(raw); value != "" {
			return value
		}
	}
	return ""
}

func rawID(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String()
	}
	return ""
}
