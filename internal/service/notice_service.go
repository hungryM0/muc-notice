// Package service coordinates portal refreshes and persisted notification history.
package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"muc-notice/internal/auth"
	"muc-notice/internal/config"
	"muc-notice/internal/notice"
)

var (
	// ErrPortalUnavailable means every configured notice request failed.
	ErrPortalUnavailable = errors.New("portal unavailable")
	// ErrLoginFailed means the saved session could not be renewed.
	ErrLoginFailed = errors.New("portal login failed")
)

const noticeRequestTimeout = 10 * time.Second

// NoticeStore persists and queries notification history.
type NoticeStore interface {
	Sync(ctx context.Context, notices []notice.Notice, cutoff, observedAt time.Time) error
	List(ctx context.Context, cutoff time.Time, noticeTypes []int, limit int) ([]notice.Notice, error)
}

type loginFunc func(context.Context, *http.Client, string, string) ([]*http.Cookie, error)
type saveCookiesFunc func(string, map[string]string) error

// NoticeService serializes portal work and caches successful results.
type NoticeService struct {
	mu       sync.Mutex
	running  bool
	done     chan struct{}
	last     []notice.Notice
	lastErr  error
	lastAt   time.Time
	complete bool
	cacheTTL time.Duration

	configPath  string
	cfg         config.Config
	store       NoticeStore
	baseClient  *http.Client
	location    *time.Location
	now         func() time.Time
	login       loginFunc
	saveCookies saveCookiesFunc
}

// NewNoticeService builds the refresh layer from validated local settings.
func NewNoticeService(configPath string, cfg config.Config, storage NoticeStore, baseClient *http.Client) (*NoticeService, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if storage == nil {
		return nil, errors.New("notification store is required")
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, fmt.Errorf("load Asia/Shanghai timezone: %w", err)
	}
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	return &NoticeService{
		configPath:  configPath,
		cfg:         cfg,
		store:       storage,
		baseClient:  baseClient,
		cacheTTL:    time.Duration(cfg.Server.CacheTTLSeconds) * time.Second,
		location:    location,
		now:         time.Now,
		login:       auth.Login,
		saveCookies: config.SaveCookies,
	}, nil
}

// Get returns recent notifications, refreshing the portal after the cache expires.
func (s *NoticeService) Get(ctx context.Context) ([]notice.Notice, error) {
	s.mu.Lock()
	if s.running {
		done := s.done
		s.mu.Unlock()
		select {
		case <-done:
			s.mu.Lock()
			items, err := cloneNotices(s.last), s.lastErr
			s.mu.Unlock()
			return items, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	now := s.now()
	if !s.lastAt.IsZero() && now.Sub(s.lastAt) >= 0 && now.Sub(s.lastAt) < s.cacheTTL {
		items := cloneNotices(s.last)
		s.mu.Unlock()
		return items, nil
	}
	s.running = true
	s.done = make(chan struct{})
	done := s.done
	s.mu.Unlock()

	items, err := s.refresh(ctx, now)

	s.mu.Lock()
	if err == nil {
		s.last = cloneNotices(items)
		s.lastAt = now
	}
	s.lastErr = err
	s.running = false
	close(done)
	s.mu.Unlock()
	return items, err
}

// GetComplete refuses a partial refresh so the initial push baseline is complete.
func (s *NoticeService) GetComplete(ctx context.Context) error {
	if _, err := s.Get(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.complete {
		return errors.New("notification refresh is incomplete")
	}
	return nil
}

func (s *NoticeService) refresh(ctx context.Context, now time.Time) ([]notice.Notice, error) {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	cutoff := historyCutoff(now, s.location, cfg.Feed.HistoryDays)

	var outcome fetchOutcome
	if hasCookies(cfg.Cookies) {
		outcome = fetchNotices(ctx, s.baseClient, cfg, cfg.NoticeTypes[:1], cutoff, now, s.location)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if outcome.allAuthenticationFailures(1) {
			var err error
			cfg, err = s.renewSession(ctx, cfg)
			if err != nil {
				return nil, err
			}
			outcome = fetchNotices(ctx, s.baseClient, cfg, cfg.NoticeTypes, cutoff, now, s.location)
		} else {
			outcome.merge(fetchNotices(ctx, s.baseClient, cfg, cfg.NoticeTypes[1:], cutoff, now, s.location))
		}
	} else {
		var err error
		cfg, err = s.renewSession(ctx, cfg)
		if err != nil {
			return nil, err
		}
		outcome = fetchNotices(ctx, s.baseClient, cfg, cfg.NoticeTypes, cutoff, now, s.location)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(outcome.succeededTypes) == 0 {
		if outcome.allAuthenticationFailures(len(cfg.NoticeTypes)) {
			return nil, ErrLoginFailed
		}
		return nil, ErrPortalUnavailable
	}
	for _, warning := range outcome.warnings {
		log.Printf("notification type %d refresh warning: %v", warning.noticeType, warning.err)
	}

	normalized := make([]notice.Notice, 0, len(outcome.items))
	for _, item := range outcome.items {
		normalized = append(normalized, notice.Normalize(item, cfg.Feed.BaseURL))
	}
	if err := s.store.Sync(ctx, normalized, cutoff, now); err != nil {
		return nil, err
	}
	queryLimit := cfg.Feed.MaxItems
	if len(outcome.warnings) > 0 && len(cfg.NoticeTypes) > 1 {
		queryLimit *= len(cfg.NoticeTypes)
	}
	items, err := s.store.List(ctx, cutoff, cfg.NoticeTypes, queryLimit)
	if err != nil {
		return nil, err
	}
	if len(outcome.warnings) == 0 {
		s.mu.Lock()
		s.complete = true
		s.mu.Unlock()
		return items, nil
	}
	s.mu.Lock()
	s.complete = false
	s.mu.Unlock()
	filtered := make([]notice.Notice, 0, len(items))
	for _, item := range items {
		if _, ok := outcome.succeededTypes[item.Type]; ok {
			filtered = append(filtered, item)
			if len(filtered) == cfg.Feed.MaxItems {
				break
			}
		}
	}
	return filtered, nil
}

func (s *NoticeService) renewSession(ctx context.Context, cfg config.Config) (config.Config, error) {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return config.Config{}, ErrLoginFailed
	}
	cookies, err := s.login(ctx, s.baseClient, cfg.Username, cfg.Password)
	if err != nil {
		return config.Config{}, fmt.Errorf("%w: %v", ErrLoginFailed, err)
	}
	cfg.Cookies = cookiesToMap(cookies)
	if err := s.saveCookies(s.configPath, cfg.Cookies); err != nil {
		return config.Config{}, fmt.Errorf("persist refreshed portal session: %w", err)
	}
	s.mu.Lock()
	s.cfg.Cookies = cfg.Cookies
	s.mu.Unlock()
	return cfg, nil
}

type fetchWarning struct {
	noticeType int
	err        error
}

type fetchOutcome struct {
	items          []notice.Notice
	warnings       []fetchWarning
	succeededTypes map[int]struct{}
}

func fetchNotices(ctx context.Context, baseClient *http.Client, cfg config.Config, noticeTypes []int, startDate, endDate time.Time, location *time.Location) fetchOutcome {
	outcome := fetchOutcome{succeededTypes: make(map[int]struct{})}
	client, err := clientWithCookies(baseClient, cfg.URL, cfg.Cookies)
	if err != nil {
		outcome.warnings = append(outcome.warnings, fetchWarning{err: err})
		return outcome
	}
	portal := notice.NewClient(client, cfg.URL, cfg.Headers, cfg.PageSize, location)
	for _, noticeType := range noticeTypes {
		requestCtx, cancel := context.WithTimeout(ctx, noticeRequestTimeout)
		page, err := portal.FetchType(requestCtx, noticeType, startDate, endDate)
		cancel()
		if err != nil {
			outcome.warnings = append(outcome.warnings, fetchWarning{noticeType: noticeType, err: err})
			continue
		}
		outcome.succeededTypes[noticeType] = struct{}{}
		outcome.items = append(outcome.items, page...)
	}
	return outcome
}

func (o *fetchOutcome) merge(other fetchOutcome) {
	o.items = append(o.items, other.items...)
	o.warnings = append(o.warnings, other.warnings...)
	for noticeType := range other.succeededTypes {
		o.succeededTypes[noticeType] = struct{}{}
	}
}

func (o fetchOutcome) allAuthenticationFailures(expected int) bool {
	if expected == 0 || len(o.warnings) != expected || len(o.succeededTypes) != 0 {
		return false
	}
	for _, warning := range o.warnings {
		if !errors.Is(warning.err, notice.ErrAuthenticationRequired) {
			return false
		}
	}
	return true
}

func historyCutoff(now time.Time, location *time.Location, historyDays int) time.Time {
	local := now.In(location)
	startOfToday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	return startOfToday.AddDate(0, 0, -(historyDays - 1))
}

func clientWithCookies(base *http.Client, endpoint string, cookies map[string]string) (*http.Client, error) {
	target, err := url.Parse(endpoint)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, errors.New("invalid notification URL")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	values := make([]*http.Cookie, 0, len(cookies))
	for name, value := range cookies {
		if name != "" && value != "" {
			values = append(values, &http.Cookie{Name: name, Value: value, Path: "/"})
		}
	}
	jar.SetCookies(target, values)
	client := *base
	client.Jar = jar
	return &client, nil
}

func hasCookies(cookies map[string]string) bool {
	for name, value := range cookies {
		if name != "" && value != "" {
			return true
		}
	}
	return false
}

func cookiesToMap(cookies []*http.Cookie) map[string]string {
	values := make(map[string]string, len(cookies))
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name != "" && cookie.Value != "" {
			values[cookie.Name] = cookie.Value
		}
	}
	return values
}

func cloneNotices(items []notice.Notice) []notice.Notice {
	return append([]notice.Notice(nil), items...)
}
