package tgsync

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestParseFixOrUpdateBareCommands(t *testing.T) {
	mode, instruction, ok := parseFixOrUpdate("fix")
	if !ok || mode != "fix" || instruction != "" {
		t.Errorf("parseFixOrUpdate(fix) = (%q, %q, %v), want (fix, \"\", true)", mode, instruction, ok)
	}

	mode, instruction, ok = parseFixOrUpdate("UPDATE")
	if !ok || mode != "update" || instruction != "" {
		t.Errorf("parseFixOrUpdate(UPDATE) = (%q, %q, %v), want (update, \"\", true)", mode, instruction, ok)
	}
}

func TestParseFixOrUpdateWithInstructions(t *testing.T) {
	cases := []struct {
		text         string
		wantMode     string
		wantInstruct string
	}{
		{"fix: reword the top bullet", "fix", "reword the top bullet"},
		{"fix : reword the top bullet", "fix", "reword the top bullet"},
		{"UPDATE: Mention Postgres more", "update", "Mention Postgres more"},
		{"update:   drop the Kafka bullet  ", "update", "drop the Kafka bullet"},
		{"fix:", "fix", ""},
		{"fix: reword the top bullet\nalso mention Kubernetes", "fix", "reword the top bullet\nalso mention Kubernetes"},
	}
	for _, c := range cases {
		mode, instruction, ok := parseFixOrUpdate(c.text)
		if !ok {
			t.Fatalf("parseFixOrUpdate(%q) ok=false, want true", c.text)
		}
		if mode != c.wantMode {
			t.Errorf("parseFixOrUpdate(%q) mode = %q, want %q", c.text, mode, c.wantMode)
		}
		if instruction != c.wantInstruct {
			t.Errorf("parseFixOrUpdate(%q) instruction = %q, want %q", c.text, instruction, c.wantInstruct)
		}
	}
}

func TestParseFixOrUpdateNoMatch(t *testing.T) {
	for _, text := range []string{"applied", "note: something", "fixing this myself", "updated the doc", ""} {
		if _, _, ok := parseFixOrUpdate(text); ok {
			t.Errorf("parseFixOrUpdate(%q) ok=true, want false", text)
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

func fixUpdate(text string) []notify.Update {
	return []notify.Update{
		{
			UpdateID: 600,
			Message: &notify.Message{
				MessageID: 1100,
				Chat:      notify.Chat{ID: 555},
				Text:      text,
				ReplyToMessage: &notify.Message{
					MessageID: 1099,
					Chat:      notify.Chat{ID: 555},
					Text:      "#J1",
				},
			},
		},
	}
}

func TestRunFixNotConfigured(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("fix")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555} // FixScript left empty

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.replies) != 1 || fake.replies[0].Text != "Resume-fix isn't configured on this install." {
		t.Errorf("expected not-configured reply, got %+v", fake.replies)
	}

	job, err := st.GetJob(ctx, 1)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.StatusNew {
		t.Errorf("job status = %q, want unchanged %q (fix must not touch job status)", job.Status, store.StatusNew)
	}
}

func TestRunFixInvokesScriptWithJobAndReplyIDs(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fix.sh")
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("fix")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, FixScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fake.replies) != 1 || fake.replies[0].Text != "Fixing layout, resending shortly…" {
		t.Errorf("expected immediate ack reply, got %+v", fake.replies)
	}
	if fake.replies[0].MessageID != 1100 {
		t.Errorf("ack sent to message %d, want 1100 (the user's \"fix\" message)", fake.replies[0].MessageID)
	}

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("script was not invoked: %v", err)
	}
	if strings.TrimSpace(string(gotArgs)) != "1 1100 fix" {
		t.Errorf("script args = %q, want \"1 1100 fix\" (job id, reply-to message id, mode)", strings.TrimSpace(string(gotArgs)))
	}
}

func TestRunFixReportsScriptFailure(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fix.sh")
	script := "#!/bin/sh\necho 'boom: compile failed' >&2\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("fix")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, FixScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fake.replies) != 2 {
		t.Fatalf("expected an ack + a failure reply, got %+v", fake.replies)
	}
	if !strings.Contains(fake.replies[1].Text, "Fix failed for #J1") {
		t.Errorf("expected failure reply to mention the job, got %q", fake.replies[1].Text)
	}
}

func TestRunFixCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("FIX")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.replies) != 1 || fake.replies[0].Text != "Resume-fix isn't configured on this install." {
		t.Errorf("expected \"FIX\" to be treated case-insensitively, got %+v", fake.replies)
	}
}

func TestRunFixWithInstructionPassesItToScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fix.sh")
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("fix: reword the top bullet")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, FixScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("script was not invoked: %v", err)
	}
	want := "1 1100 fix reword the top bullet"
	if strings.TrimSpace(string(gotArgs)) != want {
		t.Errorf("script args = %q, want %q", strings.TrimSpace(string(gotArgs)), want)
	}
}

func TestRunUpdateWithInstructionPassesItToScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fix.sh")
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("update: drop the Kafka bullet")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, FixScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fake.replies) != 1 || fake.replies[0].Text != "Applying edits, resending shortly…" {
		t.Errorf("expected update-specific ack reply, got %+v", fake.replies)
	}

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("script was not invoked: %v", err)
	}
	want := "1 1100 update drop the Kafka bullet"
	if strings.TrimSpace(string(gotArgs)) != want {
		t.Errorf("script args = %q, want %q", strings.TrimSpace(string(gotArgs)), want)
	}
}

func TestRunBareUpdateRepliesNeedsInstructionsWithoutInvokingScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fix.sh")
	script := "#!/bin/sh\nexit 1\n" // would surface as a test failure if ever invoked
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("update")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, FixScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.replies) != 1 || fake.replies[0].Text != updateNeedsInstructionMessage {
		t.Errorf("expected needs-instructions reply, got %+v", fake.replies)
	}

	job, err := st.GetJob(ctx, 1)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.StatusNew {
		t.Errorf("job status = %q, want unchanged %q", job.Status, store.StatusNew)
	}
}

// fakePages is a pageFetcher stub for manual-submit tests.
type fakePages struct {
	titles map[string]string
	err    error
}

func (f fakePages) FetchTitle(ctx context.Context, rawURL string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.titles[rawURL], nil
}

func urlUpdate(updateID, messageID int64, text string) []notify.Update {
	return []notify.Update{{
		UpdateID: updateID,
		Message: &notify.Message{
			MessageID: messageID,
			Chat:      notify.Chat{ID: 555},
			Text:      text,
		},
	}}
}

func TestManualJobURL(t *testing.T) {
	cases := map[string]bool{
		"https://www.naukri.com/job-listings-backend-engineer-123": true,
		"http://example.com/job":                                   true,
		"  https://example.com/job  ":                              true, // trimmed
		"check this out https://example.com/job":                   false,
		"not a url":                                                false,
		"":                                                         false,
	}
	for text, want := range cases {
		_, ok := manualJobURL(text)
		if ok != want {
			t.Errorf("manualJobURL(%q) ok=%v, want %v", text, ok, want)
		}
	}
}

func TestAddManualJobInsertsNewJob(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	const jobURL = "https://www.naukri.com/job-listings-backend-engineer-123"
	fake := &fakeTelegram{all: urlUpdate(1, 900, jobURL)}
	syncer := &Syncer{
		Store: st, TG: fake, ChatID: 555,
		Pages: fakePages{titles: map[string]string{jobURL: "Backend Engineer - Naukri.com"}},
	}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rows, err := st.ListJobs(ctx, store.JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 job inserted, got %d", len(rows))
	}
	j := rows[0]
	if j.Provider != "manual" {
		t.Errorf("Provider = %q, want manual", j.Provider)
	}
	if j.CompanyName != "Naukri" {
		t.Errorf("CompanyName = %q, want Naukri (guessed from host)", j.CompanyName)
	}
	if j.Title != "Backend Engineer - Naukri.com" {
		t.Errorf("Title = %q", j.Title)
	}
	if j.Status != store.StatusNew {
		t.Errorf("Status = %q, want new (rides the existing tailor-resume cron)", j.Status)
	}
	if j.URL != jobURL {
		t.Errorf("URL = %q", j.URL)
	}

	if len(fake.replies) != 1 {
		t.Fatalf("expected 1 reply, got %+v", fake.replies)
	}
	if !strings.Contains(fake.replies[0].Text, fmt.Sprintf("#J%d", j.ID)) {
		t.Errorf("reply %q missing #J%d tag", fake.replies[0].Text, j.ID)
	}
}

func TestAddManualJobDuplicateURLIsNoOp(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	const jobURL = "https://www.naukri.com/job-listings-backend-engineer-123"
	pages := fakePages{titles: map[string]string{jobURL: "Backend Engineer"}}

	syncer1 := &Syncer{Store: st, TG: &fakeTelegram{all: urlUpdate(1, 900, jobURL)}, ChatID: 555, Pages: pages}
	if _, err := syncer1.Run(ctx); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	fake2 := &fakeTelegram{all: urlUpdate(2, 901, jobURL)}
	syncer2 := &Syncer{Store: st, TG: fake2, ChatID: 555, Pages: pages}
	if _, err := syncer2.Run(ctx); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	rows, err := st.ListJobs(ctx, store.JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected still just 1 job after resubmitting the same URL, got %d", len(rows))
	}
	if len(fake2.replies) != 1 || fake2.replies[0].Text != "Already added that one." {
		t.Errorf("expected a no-op reply, got %+v", fake2.replies)
	}
}

func TestAddManualJobTitleFetchFailsStillInserts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	const jobURL = "https://wellfound.com/jobs/12345"
	fake := &fakeTelegram{all: urlUpdate(1, 900, jobURL)}
	syncer := &Syncer{
		Store: st, TG: fake, ChatID: 555,
		Pages: fakePages{err: fmt.Errorf("connection refused")},
	}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rows, err := st.ListJobs(ctx, store.JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected the job to still be inserted despite the title-fetch failure, got %d rows", len(rows))
	}
	if !strings.Contains(rows[0].Title, "title unknown") {
		t.Errorf("Title = %q, want a placeholder mentioning the fetch failure", rows[0].Title)
	}
}
