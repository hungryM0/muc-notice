package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"muc-notice/internal/api"
	"muc-notice/internal/config"
	"muc-notice/internal/feed"
	"muc-notice/internal/service"
	"muc-notice/internal/store"
	"muc-notice/internal/telegram"
)

const defaultConfigPath = "config.toml"

func main() {
	configPath := flag.String("config", defaultConfigPath, "feed service TOML configuration path")
	listenAddr := flag.String("listen", "", "override HTTP listen address")
	flag.Parse()
	if !flagWasSet("config") {
		path, err := defaultConfigFilePath()
		if err != nil {
			log.Fatal(err)
		}
		*configPath = path
	}

	cfg, created, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(fmt.Errorf("load configuration: %w", err))
	}
	if created {
		log.Printf("created configuration template: %s", *configPath)
		return
	}
	if *listenAddr != "" {
		cfg.Server.ListenAddr = *listenAddr
	}
	cfg.Telegram, err = config.LoadTelegram(*configPath, cfg.Telegram)
	if err != nil {
		log.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatal(fmt.Errorf("validate configuration: %w", err))
	}
	storagePath := config.StoragePath(*configPath, cfg.Storage.Path)
	storage, err := store.Open(context.Background(), storagePath)
	if err != nil {
		log.Fatal(fmt.Errorf("initialize notification storage: %w", err))
	}
	defer func() {
		if err := storage.Close(); err != nil {
			log.Printf("close notification storage: %v", err)
		}
	}()
	noticeService, err := service.NewNoticeService(*configPath, cfg, storage, http.DefaultClient)
	if err != nil {
		log.Fatal(fmt.Errorf("initialize notification service: %w", err))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var worker *telegram.Worker
	if cfg.Telegram.Enabled {
		token, err := os.ReadFile(config.StoragePath(*configPath, cfg.Telegram.BotTokenFile))
		if err != nil {
			log.Fatal(fmt.Errorf("read Telegram bot token file: %w", err))
		}
		sender, err := telegram.NewClient(string(token), cfg.Telegram.ProxyURL)
		if err != nil {
			log.Fatal(err)
		}
		worker = telegram.NewWorker(cfg.Telegram, noticeService, storage, sender)
	}

	server := &http.Server{
		Addr: cfg.Server.ListenAddr,
		Handler: api.NewServer(noticeService, feed.Metadata{
			BaseURL:     cfg.Feed.BaseURL,
			Title:       cfg.Feed.Title,
			Description: cfg.Feed.Description,
		}, cfg.Server.CacheTTLSeconds),
		ReadHeaderTimeout: 10 * time.Second,
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if worker != nil {
			worker.Run(ctx)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
	}()
	log.Printf("muc notice feeds listening on http://%s", cfg.Server.ListenAddr)
	listenErr := server.ListenAndServe()
	stop()
	<-workerDone
	if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
		log.Fatal(listenErr)
	}
}

func flagWasSet(name string) bool {
	set := false
	flag.Visit(func(item *flag.Flag) { set = item.Name == name })
	return set
}

func defaultConfigFilePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable for default config: %w", err)
	}
	return filepath.Join(filepath.Dir(path), defaultConfigPath), nil
}
