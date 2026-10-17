// Package api exposes public Atom and RSS feeds over HTTP.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"muc-notice/internal/feed"
	"muc-notice/internal/notice"
	"muc-notice/internal/service"
)

// NoticeProvider returns the current feed entries.
type NoticeProvider interface {
	Get(context.Context) ([]notice.Notice, error)
}

type renderer func(feed.Metadata, []notice.Notice) ([]byte, error)

// Server is the public feed HTTP handler.
type Server struct {
	notices         NoticeProvider
	metadata        feed.Metadata
	cacheTTLSeconds int
}

// NewServer creates an HTTP handler for the supplied notification service.
func NewServer(notices NoticeProvider, metadata feed.Metadata, cacheTTLSeconds int) *Server {
	if cacheTTLSeconds <= 0 {
		cacheTTLSeconds = 300
	}
	return &Server{notices: notices, metadata: metadata, cacheTTLSeconds: cacheTTLSeconds}
}

// ServeHTTP routes the public feeds and process health check.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case feed.AtomPath:
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.handleFeed(w, r, "application/atom+xml; charset=utf-8", feed.Atom)
	case feed.RSSPath:
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.handleFeed(w, r, "application/rss+xml; charset=utf-8", feed.RSS)
	default:
		category, extension, ok := feed.LookupCategoryPath(r.URL.Path)
		if !ok {
			http.Error(w, "接口不存在", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		contentType := "application/atom+xml; charset=utf-8"
		render := feed.Atom
		if extension == "rss" {
			contentType = "application/rss+xml; charset=utf-8"
			render = feed.RSS
		}
		metadata := s.metadata
		metadata.Title = metadata.Title + " - " + category.Name
		metadata.FeedPath = feed.CategoryPath(category, extension)
		s.handleFeedWithType(w, r, contentType, render, category.Type, metadata)
	}
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request, contentType string, render renderer) {
	s.handleFeedWithType(w, r, contentType, render, 0, s.metadata)
}

func (s *Server) handleFeedWithType(w http.ResponseWriter, r *http.Request, contentType string, render renderer, noticeType int, metadata feed.Metadata) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	items, err := s.notices.Get(ctx)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if noticeType != 0 {
		items = filterNoticeType(items, noticeType)
	}
	contents, err := render(metadata, items)
	if err != nil {
		http.Error(w, "生成订阅失败", http.StatusInternalServerError)
		return
	}
	etag := contentETag(contents)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(s.cacheTTLSeconds))
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(contents)
}

func filterNoticeType(items []notice.Notice, noticeType int) []notice.Notice {
	filtered := make([]notice.Notice, 0, len(items))
	for _, item := range items {
		if item.Type == noticeType {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrLoginFailed), errors.Is(err, service.ErrPortalUnavailable):
		http.Error(w, "校园门户暂时不可用", http.StatusBadGateway)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "通知请求超时", http.StatusGatewayTimeout)
	default:
		http.Error(w, "服务内部错误", http.StatusInternalServerError)
	}
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, "请求方法不被支持", http.StatusMethodNotAllowed)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func contentETag(contents []byte) string {
	sum := sha256.Sum256(contents)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
