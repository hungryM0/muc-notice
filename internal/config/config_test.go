package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestLoadNormalizesFeedAndStorageDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := []byte(`
url = "https://portal.example.edu/notices"
notice_types = [5]

[feed]
base_url = "https://feeds.example.edu/"
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, created, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("existing config reported as created")
	}
	if cfg.Feed.BaseURL != "https://feeds.example.edu" {
		t.Fatalf("BaseURL = %q", cfg.Feed.BaseURL)
	}
	if cfg.Feed.HistoryDays != 30 || cfg.Feed.MaxItems != 200 {
		t.Fatalf("Feed defaults = %#v", cfg.Feed)
	}
	if got := StoragePath(path, cfg.Storage.Path); got != filepath.Join(filepath.Dir(path), "data", "notices.db") {
		t.Fatalf("StoragePath = %q", got)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAppliesPortalDefaultsToMinimalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := []byte(`
username = "student"
password = "password"

[feed]
base_url = "https://feeds.example.edu"
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != Default().URL || cfg.PageSize != Default().PageSize || !slices.Equal(cfg.NoticeTypes, Default().NoticeTypes) || len(cfg.Headers) == 0 {
		t.Fatalf("portal defaults = %#v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDropsUnsupportedNoticeTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := []byte(`
	notice_types = [9, 9977, 5, 8, 6, 32, 10, 11, 9980, 1234]

[feed]
base_url = "https://feeds.example.edu"
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.NoticeTypes, []int{9, 9977, 5, 8, 6, 32, 10, 11, 9980}) {
		t.Fatalf("NoticeTypes = %v", cfg.NoticeTypes)
	}
}

func TestValidateRequiresPublicBaseURL(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate succeeded without feed.base_url")
	}
	cfg.Feed.BaseURL = "https://feeds.example.edu/path?token=secret"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted feed.base_url with query")
	}
}

func TestSaveCookiesPreservesFeedAndStorageConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	cfg.Feed.BaseURL = "https://feeds.example.edu"
	cfg.Feed.Title = "自定义标题"
	cfg.Storage.Path = "private/notices.db"
	cfg.Telegram = TelegramConfig{Enabled: true, BotTokenFile: "telegram.token", ChatID: -42, NoticeTypes: []int{5, 6, 32}, IntervalSeconds: 300, ProxyURL: "socks5://127.0.0.1:1080"}
	contents, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveCookies(path, map[string]string{"JSESSIONID": "fresh"}); err != nil {
		t.Fatal(err)
	}

	loaded, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Feed.BaseURL != cfg.Feed.BaseURL || loaded.Feed.Title != cfg.Feed.Title || loaded.Storage.Path != cfg.Storage.Path {
		t.Fatalf("configuration changed: %#v", loaded)
	}
	if loaded.Cookies["JSESSIONID"] != "fresh" {
		t.Fatalf("cookies = %#v", loaded.Cookies)
	}
	if loaded.Telegram.ChatID != -42 || loaded.Telegram.BotTokenFile != "telegram.token" || !slices.Equal(loaded.Telegram.NoticeTypes, []int{5, 6, 32}) {
		t.Fatal("Telegram configuration changed during cookie save")
	}
}
