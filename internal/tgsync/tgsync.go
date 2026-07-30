// Package tgsync drains Telegram updates and applies job status/notes
// changes from replies to jobwatch's own notifications.
package tgsync

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobwatch/internal/jobsubmit"
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

const updateNeedsInstructionMessage = `update: needs instructions after the colon, e.g. "update: reword the second bullet".`

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

	// FixScript is the executable invoked when a user replies "fix" to a
	// job notification: `FixScript <job_id> <telegram_message_id>`. It
	// regenerates and resends that job's tailored resume PDF. Empty
	// disables the feature (replies explaining it isn't configured).
	//
	// This is jobwatch's single Telegram consumer (see Run/GetUpdates),
	// so "fix" is handled here rather than by a second, independent
	// getUpdates poller -- two consumers racing for the same bot's
	// updates means whichever polls first silently consumes the reply
	// before the other ever sees it.
	FixScript string

	// OutreachScript is the executable invoked when a user replies
	// "outreach" or "outreach: <email>" to a job notification:
	// `OutreachScript <job_id> [<founder_email>]`. Looks up a founder
	// (or uses the given email override) and creates a personalized
	// Gmail draft. Empty disables the feature (replies explaining it
	// isn't configured), same convention as FixScript.
	OutreachScript string

	// Pages fetches manually-submitted job URLs' titles. Defaults to a real
	// HTTP fetcher; overridable for tests.
	Pages jobsubmit.PageFetcher
}

func New(st *store.Store, tg *notify.Telegram, chatID int64, fixScript string, outreachScript string) *Syncer {
	return &Syncer{Store: st, TG: tg, ChatID: chatID, FixScript: fixScript, OutreachScript: outreachScript, Pages: jobsubmit.HTTPPageFetcher{}}
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
		if rawURL, ok := manualJobURL(msg.Text); ok {
			return false, s.addManualJob(ctx, msg, rawURL)
		}
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
		// Logged at Warn (not just sent back to Telegram) so a recurrence is
		// actually diagnosable -- update_id/message_id/replied-to text
		// pin down exactly which message triggered it, unlike the plain
		// Telegram reply alone.
		slog.Warn("tg-sync: job not found for reply",
			"update_id", upd.UpdateID, "message_id", msg.MessageID, "job_id", jobID,
			"reply_text", msg.Text, "replied_to_text", repliedText)
		return false, s.TG.Reply(ctx, msg.MessageID, fmt.Sprintf("Job #J%d not found.", jobID))
	}
	if err != nil {
		return false, err
	}

	text := strings.TrimSpace(msg.Text)
	lower := strings.ToLower(text)

	if mode, instruction, ok := parseFixOrUpdate(text); ok {
		if mode == "update" && instruction == "" {
			return false, s.TG.Reply(ctx, msg.MessageID, updateNeedsInstructionMessage)
		}
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			return false, err
		}
		return false, s.runFix(ctx, jobID, msg.MessageID, mode, instruction)
	}

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

	if email, ok := parseOutreach(text); ok {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			return false, err
		}
		return false, s.runOutreach(ctx, jobID, msg.MessageID, email)
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

// outreachRe matches a bare "outreach" reply or "outreach: <email>" with
// an explicit founder-email override -- optional whitespace around the
// colon, same convention as fixOrUpdateRe.
var outreachRe = regexp.MustCompile(`(?is)^outreach\s*(?::\s*(\S+))?\s*$`)

// parseOutreach recognizes an "outreach"/"outreach: <email>" reply. It
// reports the email override (empty for a bare "outreach", meaning "use
// the automatic Apollo lookup") and whether text matched this command at
// all.
func parseOutreach(text string) (email string, ok bool) {
	m := outreachRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// fixOrUpdateRe matches a "fix:"/"update:" reply carrying free-text edit
// instructions -- optional whitespace is allowed on either side of the
// colon since Mayur's own usage includes "fix : {instructions}".
var fixOrUpdateRe = regexp.MustCompile(`(?is)^(fix|update)\s*:\s*(.*)$`)

// parseFixOrUpdate recognizes a "fix"/"fix: ..."/"update"/"update: ..."
// reply. It reports the lowercased mode, the free-text instruction
// (verbatim casing, empty for bare fix/update), and whether text matched
// this command family at all. A bare "update" (ok=true, instruction=="")
// is intentionally still reported as a match -- it's the caller's job to
// reject it, since only the caller knows how to reply back asking for
// instructions.
func parseFixOrUpdate(text string) (mode string, instruction string, ok bool) {
	lower := strings.ToLower(text)
	if lower == "fix" {
		return "fix", "", true
	}
	if lower == "update" {
		return "update", "", true
	}
	if m := fixOrUpdateRe.FindStringSubmatchIndex(text); m != nil {
		mode = strings.ToLower(text[m[2]:m[3]])
		instruction = strings.TrimSpace(text[m[4]:m[5]])
		return mode, instruction, true
	}
	return "", "", false
}

// runFix invokes FixScript to apply a fix/update instruction and
// regenerate + resend a job's tailored resume PDF. The script itself
// sends the corrected PDF as its own Telegram message (threaded as a
// reply to replyToMessageID), so runFix only needs to ack immediately
// (regeneration + LaTeX compile takes a few seconds) and report failure
// if the script errors.
//
// mode is "fix" (full rule-based regen, today's original behavior) or
// "update" (content-only edit reusing the job's cached base content --
// see tailor_resume.py's rebuild_one()). instruction is the free-text
// edit request from a "fix: ..."/"update: ..." reply, or "" for a bare
// "fix" -- omitted from FixScript's argv entirely when empty, so bare
// "fix" keeps invoking FixScript exactly as it always has.
func (s *Syncer) runFix(ctx context.Context, jobID int64, replyToMessageID int64, mode string, instruction string) error {
	if s.FixScript == "" {
		return s.TG.Reply(ctx, replyToMessageID, "Resume-fix isn't configured on this install.")
	}

	ack := "Fixing layout, resending shortly…"
	if mode == "update" {
		ack = "Applying edits, resending shortly…"
	}
	if err := s.TG.Reply(ctx, replyToMessageID, ack); err != nil {
		return err
	}

	fixCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	args := []string{
		strconv.FormatInt(jobID, 10),
		strconv.FormatInt(replyToMessageID, 10),
		mode,
	}
	if instruction != "" {
		args = append(args, instruction)
	}
	cmd := exec.CommandContext(fixCtx, s.FixScript, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("tg-sync: fix script failed", "job_id", jobID, "mode", mode, "error", err, "output", string(output))
		return s.TG.Reply(ctx, replyToMessageID, fmt.Sprintf("Fix failed for #J%d: %s", jobID, lastLine(string(output))))
	}
	return nil
}

// runOutreach invokes OutreachScript to look up a founder (or use the
// given email override) and create a Gmail draft. Mirrors runFix's
// ack-then-background-script pattern: the actual "draft ready" success
// notification comes from tailor_resume.py's own Telegram send inside
// run_outreach_step, not from this reply -- this only acks immediately
// and reports a failure if the script errors.
func (s *Syncer) runOutreach(ctx context.Context, jobID int64, replyToMessageID int64, founderEmail string) error {
	if s.OutreachScript == "" {
		return s.TG.Reply(ctx, replyToMessageID, "Outreach isn't configured on this install.")
	}

	if err := s.TG.Reply(ctx, replyToMessageID, "Looking up founder / drafting outreach…"); err != nil {
		return err
	}

	outreachCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	args := []string{strconv.FormatInt(jobID, 10)}
	if founderEmail != "" {
		args = append(args, founderEmail)
	}
	cmd := exec.CommandContext(outreachCtx, s.OutreachScript, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("tg-sync: outreach script failed", "job_id", jobID, "error", err, "output", string(output))
		return s.TG.Reply(ctx, replyToMessageID, fmt.Sprintf("Outreach failed for #J%d: %s", jobID, lastLine(string(output))))
	}
	return nil
}

// manualJobURL reports whether text is a message consisting of nothing but
// a job posting URL, for the manual-submit flow: forwarding a bare URL
// (not a reply to an existing notification) adds it as a new job. Sources
// jobwatch can't poll directly (Naukri, YC, Wellfound -- all actively
// block automated access) go through this path instead of a provider.
func manualJobURL(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
		return "", false
	}
	if strings.ContainsAny(text, " \t\n") {
		return "", false
	}
	u, err := url.Parse(text)
	if err != nil || u.Host == "" {
		return "", false
	}
	return text, true
}

// addManualJob inserts rawURL as a new manual job via the shared
// jobsubmit package and confirms with the #J{id} tag reply-based status
// control depends on.
func (s *Syncer) addManualJob(ctx context.Context, msg *notify.Message, rawURL string) error {
	id, existed, company, title, err := jobsubmit.InsertManualJob(ctx, s.Store, rawURL, "", s.Pages)
	if err != nil {
		return err
	}
	if existed {
		return s.TG.Reply(ctx, msg.MessageID, "Already added that one.")
	}
	return s.TG.Reply(ctx, msg.MessageID, fmt.Sprintf("✓ Added — %s — %s\n#J%d", company, title, id))
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if idx := strings.LastIndexByte(s, '\n'); idx >= 0 {
		s = s[idx+1:]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
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
