// Package telegram sends outbound bot messages without polling Telegram updates.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf16"

	"muc-notice/internal/notice"
)

type Client struct {
	http     *http.Client
	endpoint string
}

// NewClient uses a dedicated transport so the proxy affects only Telegram.
func NewClient(token, proxyURL string) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, "/?# \t\r\n") {
		return nil, errors.New("invalid Telegram bot token")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil {
			return nil, errors.New("invalid Telegram proxy URL")
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	return &Client{
		http: &http.Client{Transport: transport, Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		endpoint: "https://api.telegram.org/bot" + token + "/sendMessage",
	}, nil
}

// DeliveryError preserves Telegram's retry delay without exposing credentials.
type DeliveryError struct {
	Code       int
	RetryAfter time.Duration
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("Telegram rejected message (code %d, retry after %s)", e.Code, e.RetryAfter)
}

func (c *Client) Send(ctx context.Context, chatID int64, item notice.Notice) error {
	text := item.Title + "\n" + item.Link
	if len(utf16.Encode([]rune(text))) > 4096 {
		return errors.New("Telegram title and link exceed 4096 UTF-16 units")
	}
	if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Link) == "" {
		return errors.New("Telegram notice requires a title and link")
	}
	payload, err := json.Marshal(struct {
		ChatID             int64  `json:"chat_id"`
		Text               string `json:"text"`
		LinkPreviewOptions struct {
			IsDisabled bool `json:"is_disabled"`
		} `json:"link_preview_options"`
	}{ChatID: chatID, Text: text, LinkPreviewOptions: struct {
		IsDisabled bool `json:"is_disabled"`
	}{true}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return errors.New("could not create Telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// net/http URL errors contain the token-bearing URL.
		return errors.New("Telegram request failed; check proxy connectivity or timeout")
	}
	defer response.Body.Close()
	var result struct {
		OK         bool `json:"ok"`
		ErrorCode  int  `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return fmt.Errorf("invalid Telegram response (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode != http.StatusOK || !result.OK {
		code := result.ErrorCode
		if code == 0 {
			code = response.StatusCode
		}
		return &DeliveryError{Code: code, RetryAfter: time.Duration(result.Parameters.RetryAfter) * time.Second}
	}
	return nil
}
