package tgsync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jobwatch/internal/notify"
	"jobwatch/internal/providers"
	"jobwatch/internal/store"
)

func TestParseStatusKeywordAllSynonyms(t *testing.T) {
	tests := map[string]string{
		"applied": store.StatusApplied, "done": store.StatusApplied, "apld": store.StatusApplied,
		"skip": store.StatusIgnored, "skipped": store.StatusIgnored, "ignore": store.StatusIgnored, "no": store.StatusIgnored,
		"shortlist": store.StatusShortlisted, "sl": store.StatusShortlisted, "later": store.StatusShortlisted,
		"rejected": store.StatusRejected, "reject": store.StatusRejected, "rej": store.StatusRejected,
		"interview": store.StatusInterview, "iv": store.StatusInterview,
		"offer": store.StatusOffer,
		"new":   store.StatusNew, "reset": store.StatusNew,
	}
	for word, want := range tests {
		t.Run(word, func(t *testing.T) {
			got, ok := ParseStatusKeyword(word)
			if !ok {
				t.Fatalf("ParseStatusKeyword(%q) ok=false, want true", word)
			}
			if got != want {
				t.Errorf("ParseStatusKeyword(%q) = %q, want %q", word, got, want)
			}
		})
	}
}

func TestParseStatusKeywordUnknown(t *testing.T) {
	for _, word := range []string{"banana", "", "maybe", "applyed"} {
		if _, ok := ParseStatusKeyword(word); ok {
			t.Errorf("ParseStatusKeyword(%q) ok=true, want false", word)
		}
	}
}

func TestAppendNote(t *testing.T) {
	at := time.Date(2026, 7, 15, 14, 30, 0, 0, time.UTC)

	got := appendNote("", "applied via referral", at)
	want := "[2026-07-15 14:30] applied via referral"
	if got != want {
		t.Errorf("appendNote(empty) = %q, want %q", got, want)
	}

	got2 := appendNote(want, "recruiter called back", at)
	want2 := want + "\n[2026-07-15 14:30] recruiter called back"
	if got2 != want2 {
		t.Errorf("appendNote(existing) = %q, want %q", got2, want2)
	}
}

// fakeTelegram mimics the Telegram Bot API's getUpdates semantics closely
// enough for tests: GetUpdates only returns updates with update_id >= offset,
// mirroring the real API's acknowledgement behavior.
type fakeTelegram struct {
	all     []notify.Update
	replies []reply
}

type reply struct {
	MessageID int64
	Text      string
}

func (f *fakeTelegram) GetUpdates(ctx context.Context, offset int64) ([]notify.Update, error) {
	var out []notify.Update
	for _, u := range f.all {
		if u.UpdateID >= offset {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeTelegram) Reply(ctx context.Context, replyToMessageID int64, text string) error {
	f.replies = append(f.replies, reply{MessageID: replyToMessageID, Text: text})
	return nil
}

func loadFixtureUpdates(t *testing.T) []notify.Update {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "updates_sample.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var updates []notify.Update
	if err := json.Unmarshal(data, &updates); err != nil {
		t.Fatalf("unmarshaling fixture: %v", err)
	}
	return updates
}

func newTestStoreWithJob(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	job := providers.Job{
		Provider: "greenhouse", CompanySlug: "stripe", CompanyName: "Stripe",
		ExternalID: "1", Title: "Backend Engineer", Location: "Remote",
		URL: "https://stripe.com/jobs/1", FirstSeenAt: time.Now().UTC(), Raw: []byte(`{}`),
	}
	ctx := context.Background()
	tx, err := st.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	id, err := st.InsertJob(ctx, tx, job, store.StatusNew)
	if err != nil {
		t.Fatalf("InsertJob: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if id != 1 {
		t.Fatalf("expected job id 1 (fixture references #J1), got %d", id)
	}
	return st
}

func TestRunProcessesValidReplyIgnoresWrongChatAndNonReply(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: loadFixtureUpdates(t)}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555}

	result, err := syncer.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Processed != 3 {
		t.Errorf("Processed = %d, want 3", result.Processed)
	}
	if result.StatusChanges != 1 {
		t.Errorf("StatusChanges = %d, want 1 (only the valid reply from the matching chat)", result.StatusChanges)
	}

	job, err := st.GetJob(ctx, 1)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.StatusApplied {
		t.Errorf("job status = %q, want %q", job.Status, store.StatusApplied)
	}

	if len(fake.replies) != 1 {
		t.Fatalf("expected exactly 1 reply sent, got %d: %+v", len(fake.replies), fake.replies)
	}
	if fake.replies[0].MessageID != 501 {
		t.Errorf("reply sent to message %d, want 501 (the valid update's message)", fake.replies[0].MessageID)
	}
	wantText := "✓ Stripe — Backend Engineer → applied"
	if fake.replies[0].Text != wantText {
		t.Errorf("reply text = %q, want %q", fake.replies[0].Text, wantText)
	}

	offsetStr, ok, err := st.GetKV(ctx, kvOffsetKey)
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if !ok {
		t.Fatal("expected tg_offset to be persisted")
	}
	if offsetStr != "103" {
		t.Errorf("offset = %q, want %q (max update_id 102 + 1)", offsetStr, "103")
	}
}

func TestRunSecondCallProcessesNothing(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: loadFixtureUpdates(t)}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	fake.replies = nil // reset to observe only the second run's activity

	result, err := syncer.Run(ctx)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Processed != 0 {
		t.Errorf("second run Processed = %d, want 0 (offset should have suppressed re-delivery)", result.Processed)
	}
	if len(fake.replies) != 0 {
		t.Errorf("second run sent %d replies, want 0", len(fake.replies))
	}
}

func TestRunUnknownKeywordSendsHelpAndLeavesStatus(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithJob(t)

	updates := []notify.Update{
		{
			UpdateID: 200,
			Message: &notify.Message{
				MessageID: 700,
				Chat:      notify.Chat{ID: 555},
				Text:      "banana",
				ReplyToMessage: &notify.Message{
					MessageID: 699,
					Chat:      notify.Chat{ID: 555},
					Text:      "job stuff #J1",
				},
			},
		},
	}
	fake := &fakeTelegram{all: updates}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555}

	result, err := syncer.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.StatusChanges != 0 {
		t.Errorf("StatusChanges = %d, want 0", result.StatusChanges)
	}

	job, err := st.GetJob(ctx, 1)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.StatusNew {
		t.Errorf("job status = %q, want unchanged %q", job.Status, store.StatusNew)
	}

	if len(fake.replies) != 1 || fake.replies[0].Text != helpMessage {
		t.Errorf("expected help message reply, got %+v", fake.replies)
	}
}

func TestRunMissingJobTagRepliesWithError(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithJob(t)

	updates := []notify.Update{
		{
			UpdateID: 300,
			Message: &notify.Message{
				MessageID: 800,
				Chat:      notify.Chat{ID: 555},
				Text:      "applied",
				ReplyToMessage: &notify.Message{
					MessageID: 799,
					Chat:      notify.Chat{ID: 555},
					Text:      "no tag in this message",
				},
			},
		},
	}
	fake := &fakeTelegram{all: updates}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.replies) != 1 || fake.replies[0].Text != "Couldn't find a job tag in that message." {
		t.Errorf("expected tag-not-found reply, got %+v", fake.replies)
	}
}

func TestRunJobNotFoundReplies(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithJob(t)

	updates := []notify.Update{
		{
			UpdateID: 400,
			Message: &notify.Message{
				MessageID: 900,
				Chat:      notify.Chat{ID: 555},
				Text:      "applied",
				ReplyToMessage: &notify.Message{
					MessageID: 899,
					Chat:      notify.Chat{ID: 555},
					Text:      "#J999",
				},
			},
		},
	}
	fake := &fakeTelegram{all: updates}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.replies) != 1 || fake.replies[0].Text != "Job #J999 not found." {
		t.Errorf("expected not-found reply, got %+v", fake.replies)
	}
}

func TestRunNotePrefixAppendsWithoutChangingStatus(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithJob(t)

	updates := []notify.Update{
		{
			UpdateID: 500,
			Message: &notify.Message{
				MessageID: 1000,
				Chat:      notify.Chat{ID: 555},
				Text:      "note: applied via referral",
				ReplyToMessage: &notify.Message{
					MessageID: 999,
					Chat:      notify.Chat{ID: 555},
					Text:      "#J1",
				},
			},
		},
	}
	fake := &fakeTelegram{all: updates}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555}

	result, err := syncer.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.StatusChanges != 0 {
		t.Errorf("StatusChanges = %d, want 0 for a note-only update", result.StatusChanges)
	}

	job, err := st.GetJob(ctx, 1)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.StatusNew {
		t.Errorf("job status = %q, want unchanged %q", job.Status, store.StatusNew)
	}
	if !strings.Contains(job.Notes, "applied via referral") {
		t.Errorf("job notes = %q, want it to contain the note text", job.Notes)
	}
}
