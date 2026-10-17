package telegram

import (
	"context"
	"errors"
	"log"
	"time"

	"muc-notice/internal/config"
	"muc-notice/internal/notice"
)

type Refresher interface{ GetComplete(context.Context) error }

type Store interface {
	QueueTelegram(context.Context, int64, []int) error
	PendingTelegram(context.Context, int64, []int) ([]notice.Notice, error)
	MarkTelegramDelivered(context.Context, int64, string) error
}

type Sender interface {
	Send(context.Context, int64, notice.Notice) error
}

type Worker struct {
	cfg     config.TelegramConfig
	refresh Refresher
	store   Store
	sender  Sender
	retryAt time.Time
}

func NewWorker(cfg config.TelegramConfig, refresh Refresher, storage Store, sender Sender) *Worker {
	return &Worker{cfg: cfg, refresh: refresh, store: storage, sender: sender}
}

// Run serializes cycles and terminates promptly when the service stops.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(w.cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		if err := w.Check(ctx); err != nil && ctx.Err() == nil {
			log.Printf("Telegram notification cycle: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Check preserves failed messages for later cycles, even if portal refresh fails.
func (w *Worker) Check(ctx context.Context) error {
	refreshErr := w.refresh.GetComplete(ctx)
	if refreshErr == nil {
		if err := w.store.QueueTelegram(ctx, w.cfg.ChatID, w.cfg.NoticeTypes); err != nil {
			return err
		}
	}
	if time.Now().Before(w.retryAt) {
		return refreshErr
	}
	items, err := w.store.PendingTelegram(ctx, w.cfg.ChatID, w.cfg.NoticeTypes)
	if err != nil {
		return err
	}
	for i, item := range items {
		// Telegram group limits are 20 messages per minute; keep sends 4s apart.
		if i > 0 {
			timer := time.NewTimer(4 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := w.sender.Send(ctx, w.cfg.ChatID, item); err != nil {
			var deliveryErr *DeliveryError
			if errors.As(err, &deliveryErr) && deliveryErr.RetryAfter > 0 {
				w.retryAt = time.Now().Add(deliveryErr.RetryAfter)
			}
			return err
		}
		if err := w.store.MarkTelegramDelivered(ctx, w.cfg.ChatID, item.ID); err != nil {
			return err
		}
	}
	return refreshErr
}
