package store

import (
	"context"
	"database/sql"
	"time"
)

// Event statuses. An event is written as pending, claimed as running by the
// worker, and finished as done or failed. A running event found at worker
// startup was interrupted (pod restart/deploy) and is re-queued.
const (
	EventPending = "pending"
	EventRunning = "running"
	EventDone    = "done"
	EventFailed  = "failed"
)

// Event kinds.
const (
	EventTelegramUpdate = "telegram_update" // payload: raw Telegram update JSON
	EventTailorOne      = "tailor_one"      // payload: {"job_id": N}
	EventOutreach       = "outreach"        // payload: {"job_id": N, "founder_email": "..."}
	EventCronRun        = "cron_run"        // payload: {"name": "poll"}
)

type Event struct {
	ID        int64
	Kind      string
	Payload   string
	Status    string
	Attempts  int
	Error     string
	CreatedAt string
	UpdatedAt string
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// EnqueueEvent adds one pending event and returns its id.
func (s *Store) EnqueueEvent(ctx context.Context, kind, payload string) (int64, error) {
	now := nowRFC3339()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO events (kind, payload, status, created_at, updated_at) VALUES (?, ?, 'pending', ?, ?)`,
		kind, payload, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// EnqueueEventsSetKV inserts events and upserts a kv entry in one
// transaction -- used to persist Telegram updates together with the advanced
// getUpdates offset, so an update is never acknowledged without being queued
// (or queued twice after a crash between the two writes).
func (s *Store) EnqueueEventsSetKV(ctx context.Context, kind string, payloads []string, key, value string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	now := nowRFC3339()
	for _, p := range payloads {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events (kind, payload, status, created_at, updated_at) VALUES (?, ?, 'pending', ?, ?)`,
			kind, p, now, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value); err != nil {
		return err
	}
	return tx.Commit()
}

// ClaimEvent atomically marks the oldest pending event running and returns
// it, or nil when the queue is empty. A single UPDATE ... RETURNING statement
// holds SQLite's write lock, so two claimers can never take the same event.
func (s *Store) ClaimEvent(ctx context.Context) (*Event, error) {
	var e Event
	err := s.db.QueryRowContext(ctx, `
		UPDATE events SET status = 'running', attempts = attempts + 1, updated_at = ?
		WHERE id = (SELECT id FROM events WHERE status = 'pending' ORDER BY id LIMIT 1)
		RETURNING id, kind, payload, status, attempts, error, created_at, updated_at`,
		nowRFC3339(),
	).Scan(&e.ID, &e.Kind, &e.Payload, &e.Status, &e.Attempts, &e.Error, &e.CreatedAt, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// FinishEvent marks an event done (errMsg empty) or failed.
func (s *Store) FinishEvent(ctx context.Context, id int64, errMsg string) error {
	status := EventDone
	if errMsg != "" {
		status = EventFailed
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE events SET status = ?, error = ?, updated_at = ? WHERE id = ?`,
		status, errMsg, nowRFC3339(), id)
	return err
}

// RecoverRunningEvents re-queues events left running by an interrupted
// worker, or fails them once they've been attempted maxAttempts times (an
// event that crashes the worker every time must not crash-loop forever).
// Only call this with no other worker process alive.
func (s *Store) RecoverRunningEvents(ctx context.Context, maxAttempts int) (requeued, failed int64, err error) {
	now := nowRFC3339()
	res, err := s.db.ExecContext(ctx,
		`UPDATE events SET status = 'failed', error = 'interrupted too many times', updated_at = ?
		 WHERE status = 'running' AND attempts >= ?`, now, maxAttempts)
	if err != nil {
		return 0, 0, err
	}
	failed, _ = res.RowsAffected()
	res, err = s.db.ExecContext(ctx,
		`UPDATE events SET status = 'pending', updated_at = ? WHERE status = 'running'`, now)
	if err != nil {
		return 0, failed, err
	}
	requeued, _ = res.RowsAffected()
	return requeued, failed, nil
}

// RecentEvents returns the newest events first, for the dashboard.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, payload, status, attempts, error, created_at, updated_at
		 FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Kind, &e.Payload, &e.Status, &e.Attempts, &e.Error, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
