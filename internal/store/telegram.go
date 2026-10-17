package store

import (
	"context"
	"fmt"
	"time"

	"muc-notice/internal/notice"
)

// QueueTelegram snapshots new records and establishes a baseline for each category.
// Baselines and queued messages survive restarts and notice-history pruning.
func (s *SQLite) QueueTelegram(ctx context.Context, chatID int64, types []int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Telegram queue: %w", err)
	}
	defer tx.Rollback()
	for _, kind := range types {
		var initialized int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_baselines WHERE chat_id = ? AND type = ?`, chatID, kind).Scan(&initialized); err != nil {
			return fmt.Errorf("read Telegram baseline: %w", err)
		}
		// Historical records are acknowledged on the first successful complete refresh.
		var delivered any
		if initialized == 0 {
			delivered = formatTime(time.Now())
		}
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO telegram_messages (chat_id, notice_id, type, title, link, published_at, delivered_at)
SELECT ?, id, type, title, link, published_at, ? FROM notices WHERE type = ?`, chatID, delivered, kind); err != nil {
			return fmt.Errorf("queue Telegram notices: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO telegram_baselines (chat_id, type) VALUES (?, ?)`, chatID, kind); err != nil {
			return fmt.Errorf("initialize Telegram baseline: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Telegram queue: %w", err)
	}
	return nil
}

// PendingTelegram includes only currently selected categories, oldest first.
func (s *SQLite) PendingTelegram(ctx context.Context, chatID int64, types []int) ([]notice.Notice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT notice_id, type, title, link FROM telegram_messages
WHERE chat_id = ? AND delivered_at IS NULL ORDER BY published_at, notice_id`, chatID)
	if err != nil {
		return nil, fmt.Errorf("read Telegram queue: %w", err)
	}
	defer rows.Close()
	selected := make(map[int]bool, len(types))
	for _, kind := range types {
		selected[kind] = true
	}
	var items []notice.Notice
	for rows.Next() {
		var item notice.Notice
		if err := rows.Scan(&item.ID, &item.Type, &item.Title, &item.Link); err != nil {
			return nil, fmt.Errorf("scan Telegram queue: %w", err)
		}
		if selected[item.Type] {
			items = append(items, item)
		}
	}
	return items, rows.Err()
}

// MarkTelegramDelivered acknowledges a message only after Telegram accepts it.
func (s *SQLite) MarkTelegramDelivered(ctx context.Context, chatID int64, noticeID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE telegram_messages SET delivered_at = ?
WHERE chat_id = ? AND notice_id = ? AND delivered_at IS NULL`, formatTime(time.Now()), chatID, noticeID)
	if err != nil {
		return fmt.Errorf("acknowledge Telegram message: %w", err)
	}
	return nil
}
