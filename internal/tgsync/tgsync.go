// Package tgsync drains Telegram updates and applies job status/notes
// changes from replies to jobwatch's own notifications.
package tgsync

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"jobwatch/internal/notify"
	"jobwatch/internal/store"
)

const kvOffsetKey = "tg_offset"

const helpMessage = `Didn't recognize that. Reply with one of:
  applied / done / apld       -> applied
  skip / skipped / ignore / no -> ignored
  shortlist / sl / later       -> shortlisted
  rejected / reject / rej      -> rejected
  interview / iv                -> interview
  offer                          -> offer
  new / reset                    -> new
Or start your reply with "note: " to add a note without changing status.`

// telegramClient is the subset of *notify.Telegram tg-sync needs, so tests
// can substitute a fixture-backed fake.
type telegramClient interface {
	GetUpdates(ctx context.Context, offset int64) ([]notify.Update, error)
	Reply(ctx context.Context, replyToMessageID int64, text string) error
}

// Syncer drains Telegram updates once and applies any status/notes changes
// found in replies to jobwatch notifications.
type Syncer struct {
	Store  *store.Store
	TG     telegramClient
	ChatID int64
}

func New(st *store.Store, tg *notify.Telegram, chatID int64) *Syncer {
	return &Syncer{Store: st, TG: tg, ChatID: chatID}
}

// Result summarizes one sync run.
type Result struct {
	Processed     int
	StatusChanges int
	Errors        []string
}

// Run fetches pending updates, applies any valid status/notes changes, and
// persists the new offset so the next run doesn't reprocess them. It runs
// once and returns; callers (e.g. cron) re-invoke it on a schedule.
func (s *Syncer) Run(ctx context.Context) (Result, error) {
	offset, err := s.loadOffset(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("loading offset: %w", err)
	}

	updates, err := s.TG.GetUpdates(ctx, offset)
	if err != nil {
		return Result{}, fmt.Errorf("getUpdates: %w", err)
	}

	var result Result
	maxUpdateID := offset - 1
	for _, upd := range updates {
		if upd.UpdateID > maxUpdateID {
			maxUpdateID = upd.UpdateID
		}

		changed, err := s.processUpdate(ctx, upd)
		result.Processed++
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			slog.Error("tg-sync: processing update failed", "update_id", upd.UpdateID, "error", err)
			continue
		}
		if changed {
			result.StatusChanges++
		}
	}

	if len(updates) > 0 {
		if err := s.Store.SetKV(ctx, kvOffsetKey, strconv.FormatInt(maxUpdateID+1, 10)); err != nil {
			return result, fmt.Errorf("persisting offset: %w", err)
		}
	}

	return result, nil
}

func (s *Syncer) loadOffset(ctx context.Context) (int64, error) {
	value, ok, err := s.Store.GetKV(ctx, kvOffsetKey)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	offset, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing stored offset %q: %w", value, err)
	}
	return offset, nil
}

// processUpdate applies one update, if applicable, and reports whether it
// resulted in a job status change (notes-only changes and error replies
// don't count).
func (s *Syncer) processUpdate(ctx context.Context, upd notify.Update) (bool, error) {
	msg := upd.Message
	if msg == nil {
		return false, nil
	}

	if msg.Chat.ID != s.ChatID {
		slog.Warn("tg-sync: ignoring update from unexpected chat", "chat_id", msg.Chat.ID, "update_id", upd.UpdateID)
		return false, nil
	}

	if msg.ReplyToMessage == nil {
		return false, nil
	}

	repliedText := firstNonEmpty(msg.ReplyToMessage.Text, msg.ReplyToMessage.Caption)
	jobID, ok := notify.ExtractJobID(repliedText)
	if !ok {
		return false, s.TG.Reply(ctx, msg.MessageID, "Couldn't find a job tag in that message.")
	}

	tx, err := s.Store.BeginTx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op if already committed

	job, err := s.Store.GetJobTx(ctx, tx, jobID)
	if err == sql.ErrNoRows {
		return false, s.TG.Reply(ctx, msg.MessageID, fmt.Sprintf("Job #J%d not found.", jobID))
	}
	if err != nil {
		return false, err
	}

	text := strings.TrimSpace(msg.Text)
	lower := strings.ToLower(text)

	if strings.HasPrefix(lower, "note:") {
		noteText := strings.TrimSpace(text[len("note:"):])
		newNotes := appendNote(job.Notes, noteText, time.Now())
		if err := s.Store.UpdateNotesTx(ctx, tx, jobID, newNotes); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, s.TG.Reply(ctx, msg.MessageID, fmt.Sprintf("✓ Note added — %s — %s", job.CompanyName, job.Title))
	}

	fields := strings.Fields(lower)
	var firstWord string
	if len(fields) > 0 {
		firstWord = fields[0]
	}

	status, ok := ParseStatusKeyword(firstWord)
	if !ok {
		return false, s.TG.Reply(ctx, msg.MessageID, helpMessage)
	}

	if err := s.Store.UpdateStatusTx(ctx, tx, jobID, status); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, s.TG.Reply(ctx, msg.MessageID, fmt.Sprintf("✓ %s — %s → %s", job.CompanyName, job.Title, status))
}

// ParseStatusKeyword maps a lowercased reply keyword to a job status.
func ParseStatusKeyword(word string) (string, bool) {
	switch word {
	case "applied", "done", "apld":
		return store.StatusApplied, true
	case "skip", "skipped", "ignore", "no":
		return store.StatusIgnored, true
	case "shortlist", "sl", "later":
		return store.StatusShortlisted, true
	case "rejected", "reject", "rej":
		return store.StatusRejected, true
	case "interview", "iv":
		return store.StatusInterview, true
	case "offer":
		return store.StatusOffer, true
	case "new", "reset":
		return store.StatusNew, true
	default:
		return "", false
	}
}

func appendNote(existing, note string, at time.Time) string {
	entry := fmt.Sprintf("[%s] %s", at.UTC().Format("2006-01-02 15:04"), note)
	if existing == "" {
		return entry
	}
	return existing + "\n" + entry
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
