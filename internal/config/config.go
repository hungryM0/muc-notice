// Package config loads the compatible notification client configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var defaultNoticeTypes = []int{9, 9977, 9978, 4, 5, 8, 6, 32, 10, 36, 11, 9980, 9979, 3}

// Config contains the TOML settings used by the notification client.
type Config struct {
	Server      ServerConfig      `toml:"server"`
	Feed        FeedConfig        `toml:"feed"`
	Storage     StorageConfig     `toml:"storage"`
	Telegram    TelegramConfig    `toml:"telegram"`
	URL         string            `toml:"url"`
	Username    string            `toml:"username"`
	Password    string            `toml:"password"`
	Headers     map[string]string `toml:"headers"`
	Cookies     map[string]string `toml:"cookies"`
	NoticeTypes []int             `toml:"notice_types"`
	PageSize    int               `toml:"page_size"`
}

// ServerConfig controls the local HTTP API listener and successful-result cache.
type ServerConfig struct {
	ListenAddr      string `toml:"listen_addr"`
	CacheTTLSeconds int    `toml:"cache_ttl_seconds"`
}

// FeedConfig controls public feed metadata and retention.
type FeedConfig struct {
	BaseURL     string `toml:"base_url"`
	Title       string `toml:"title"`
	Description string `toml:"description"`
	HistoryDays int    `toml:"history_days"`
	MaxItems    int    `toml:"max_items"`
}

// StorageConfig controls the local SQLite database.
type StorageConfig struct {
	Path string `toml:"path"`
}

// TelegramConfig controls outbound notifications; it never consumes bot updates.
type TelegramConfig struct {
	Enabled         bool   `toml:"enabled"`
	BotTokenFile    string `toml:"bot_token_file"`
	ChatID          int64  `toml:"chat_id"`
	NoticeTypes     []int  `toml:"notice_types"`
	IntervalSeconds int    `toml:"interval_seconds"`
	ProxyURL        string `toml:"proxy_url"`
}

// Default returns the portal notification request settings.
func Default() Config {
	return Config{
		Server: ServerConfig{
			ListenAddr:      "127.0.0.1:8080",
			CacheTTLSeconds: 300,
		},
		Feed: FeedConfig{
			Title:       "民大信息门户通知",
			Description: "民大信息门户通知订阅",
			HistoryDays: 30,
			MaxItems:    200,
		},
		Storage:  StorageConfig{Path: "data/notices.db"},
		Telegram: TelegramConfig{IntervalSeconds: 300},
		URL:      "https://my.muc.edu.cn/comsys-portal-notice-web/getNoticeByPage",
		Headers: map[string]string{
			"Accept":           "application/json, text/plain, */*",
			"Accept-Language":  "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6",
			"Content-Type":     "application/x-www-form-urlencoded; charset=UTF-8",
			"X-Requested-With": "XMLHttpRequest",
		},
		Cookies:     map[string]string{},
		NoticeTypes: append([]int(nil), defaultNoticeTypes...),
		PageSize:    20,
	}
}

// Load reads or creates a TOML configuration file.
func Load(path string) (Config, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		contents, err = toml.Marshal(cfg)
		if err != nil {
			return Config{}, false, fmt.Errorf("encode default TOML config: %w", err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			return Config{}, false, fmt.Errorf("create default config: %w", err)
		}
		return cfg, true, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(contents, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("decode config: %w", err)
	}
	normalize(&cfg)
	return cfg, false, nil
}

// Validate will check settings required by the notification client.
func (c Config) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return errors.New("config url is required")
	}
	if strings.TrimSpace(c.Server.ListenAddr) == "" {
		return errors.New("config server.listen_addr is required")
	}
	if c.Server.CacheTTLSeconds <= 0 {
		return errors.New("config server.cache_ttl_seconds must be positive")
	}
	baseURL, err := url.Parse(strings.TrimSpace(c.Feed.BaseURL))
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return errors.New("config feed.base_url must be an HTTP(S) URL without query or fragment")
	}
	if strings.TrimSpace(c.Feed.Title) == "" {
		return errors.New("config feed.title is required")
	}
	if c.Feed.HistoryDays <= 0 {
		return errors.New("config feed.history_days must be positive")
	}
	if c.Feed.MaxItems <= 0 {
		return errors.New("config feed.max_items must be positive")
	}
	if strings.TrimSpace(c.Storage.Path) == "" {
		return errors.New("config storage.path is required")
	}
	if len(c.NoticeTypes) == 0 {
		return errors.New("config notice_types must not be empty")
	}
	for _, noticeType := range c.NoticeTypes {
		if !supportedNoticeType(noticeType) {
			return fmt.Errorf("config notice_types contains unsupported type %d", noticeType)
		}
	}
	if c.Telegram.Enabled {
		if strings.TrimSpace(c.Telegram.BotTokenFile) == "" || c.Telegram.ChatID == 0 {
			return errors.New("telegram.bot_token_file and telegram.chat_id are required when enabled")
		}
		if c.Telegram.IntervalSeconds <= 0 || len(c.Telegram.NoticeTypes) == 0 {
			return errors.New("telegram.interval_seconds must be positive and telegram.notice_types must not be empty")
		}
		for _, selected := range c.Telegram.NoticeTypes {
			found := false
			for _, fetched := range c.NoticeTypes {
				found = found || selected == fetched
			}
			if !supportedNoticeType(selected) || !found {
				return fmt.Errorf("telegram.notice_types contains unsupported or unfetched type %d", selected)
			}
		}
		if c.Telegram.ProxyURL != "" {
			proxy, err := url.Parse(c.Telegram.ProxyURL)
			if err != nil || proxy.Hostname() == "" || proxy.Port() == "" || (proxy.Scheme != "socks5" && proxy.Scheme != "socks5h" && proxy.Scheme != "http" && proxy.Scheme != "https") || (proxy.Path != "" && proxy.Path != "/") || proxy.RawQuery != "" || proxy.Fragment != "" {
				return errors.New("telegram.proxy_url must be an HTTP(S) or SOCKS5 proxy URL with a host and port")
			}
		}
	}
	return nil
}

// StoragePath resolves relative database paths from the configuration directory.
func StoragePath(configPath, storagePath string) string {
	if filepath.IsAbs(storagePath) {
		return filepath.Clean(storagePath)
	}
	return filepath.Join(filepath.Dir(configPath), storagePath)
}

// LoadTelegram optionally overrides inline settings with a private sibling file.
func LoadTelegram(configPath string, current TelegramConfig) (TelegramConfig, error) {
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(configPath), "telegram.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return current, nil
	}
	if err != nil {
		return TelegramConfig{}, fmt.Errorf("read Telegram config: %w", err)
	}
	var extra struct {
		Telegram TelegramConfig `toml:"telegram"`
	}
	if err := toml.Unmarshal(contents, &extra); err != nil {
		return TelegramConfig{}, errors.New("invalid Telegram TOML configuration")
	}
	if extra.Telegram.IntervalSeconds == 0 {
		extra.Telegram.IntervalSeconds = 300
	}
	return extra.Telegram, nil
}

// SaveCookies will update only the persisted session cookies.
func SaveCookies(path string, cookies map[string]string) error {
	cfg, _, err := Load(path)
	if err != nil {
		return fmt.Errorf("load config before saving cookies: %w", err)
	}
	cfg.Cookies = cloneCookies(cookies)

	contents, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config with updated cookies: %w", err)
	}
	contents = append(contents, '\n')

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err := temp.Write(contents); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace config with updated cookies: %w", err)
	}
	return nil
}

func cloneCookies(cookies map[string]string) map[string]string {
	if cookies == nil {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(cookies))
	for name, value := range cookies {
		cloned[name] = value
	}
	return cloned
}

func normalize(cfg *Config) {
	if cfg.Telegram.IntervalSeconds == 0 {
		cfg.Telegram.IntervalSeconds = 300
	}
	defaults := Default()
	if strings.TrimSpace(cfg.URL) == "" {
		cfg.URL = defaults.URL
	}
	if cfg.Headers == nil {
		cfg.Headers = cloneStringMap(defaults.Headers)
	}
	if cfg.Cookies == nil {
		cfg.Cookies = map[string]string{}
	}
	cfg.NoticeTypes = normalizeNoticeTypes(cfg.NoticeTypes, defaults.NoticeTypes)
	if cfg.PageSize <= 0 {
		cfg.PageSize = defaults.PageSize
	}
	if strings.TrimSpace(cfg.Server.ListenAddr) == "" {
		cfg.Server.ListenAddr = defaults.Server.ListenAddr
	}
	if cfg.Server.CacheTTLSeconds <= 0 {
		cfg.Server.CacheTTLSeconds = defaults.Server.CacheTTLSeconds
	}
	cfg.Feed.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.Feed.BaseURL), "/")
	cfg.Feed.Title = strings.TrimSpace(cfg.Feed.Title)
	cfg.Feed.Description = strings.TrimSpace(cfg.Feed.Description)
	if strings.TrimSpace(cfg.Feed.Title) == "" {
		cfg.Feed.Title = defaults.Feed.Title
	}
	if strings.TrimSpace(cfg.Feed.Description) == "" {
		cfg.Feed.Description = defaults.Feed.Description
	}
	if cfg.Feed.HistoryDays == 0 {
		cfg.Feed.HistoryDays = defaults.Feed.HistoryDays
	}
	if cfg.Feed.MaxItems == 0 {
		cfg.Feed.MaxItems = defaults.Feed.MaxItems
	}
	if strings.TrimSpace(cfg.Storage.Path) == "" {
		cfg.Storage.Path = defaults.Storage.Path
	} else {
		cfg.Storage.Path = strings.TrimSpace(cfg.Storage.Path)
	}
}

func normalizeNoticeTypes(noticeTypes, defaults []int) []int {
	seen := make(map[int]struct{}, len(noticeTypes))
	normalized := make([]int, 0, len(defaults))
	for _, noticeType := range noticeTypes {
		if !supportedNoticeType(noticeType) {
			continue
		}
		if _, exists := seen[noticeType]; exists {
			continue
		}
		seen[noticeType] = struct{}{}
		normalized = append(normalized, noticeType)
	}
	if len(normalized) == 0 {
		return append([]int(nil), defaults...)
	}
	return normalized
}

func supportedNoticeType(noticeType int) bool {
	for _, supported := range defaultNoticeTypes {
		if noticeType == supported {
			return true
		}
	}
	return false
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}
