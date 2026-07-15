// Package notify sends Telegram notifications for new job postings.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobwatch/internal/providers"
)

const maxIndividualMessages = 20

// Telegram sends messages via the Telegram Bot API.
type Telegram struct {
	Client  *http.Client
	BaseURL string // override for tests, defaults to https://api.telegram.org
	Token   string
	ChatID  string
}

// New constructs a Telegram notifier. Returns an error if token or chatID
// are empty, since every caller needs a clear failure rather than a silent
// no-op (see `jobwatch test-notify`).
func New(token, chatID string) (*Telegram, error) {
	if token == "" {
		return nil, fmt.Errorf("telegram bot token is empty (check the env var named in telegram.bot_token_env)")
	}
	if chatID == "" {
		return nil, fmt.Errorf("telegram chat id is empty (check the env var named in telegram.chat_id_env)")
	}
	return &Telegram{
		Client:  http.DefaultClient,
		BaseURL: "https://api.telegram.org",
		Token:   token,
		ChatID:  chatID,
	}, nil
}

type sendMessageResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

type sendParams struct {
	Text             string
	ParseMode        string // empty means no parse_mode (plain text)
	ReplyToMessageID int64  // 0 means not a reply
}

func (t *Telegram) sendMessage(ctx context.Context, p sendParams) error {
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", t.BaseURL, t.Token)

	form := url.Values{}
	form.Set("chat_id", t.ChatID)
	form.Set("text", p.Text)
	form.Set("disable_web_page_preview", "true")
	if p.ParseMode != "" {
		form.Set("parse_mode", p.ParseMode)
	}
	if p.ReplyToMessageID != 0 {
		form.Set("reply_to_message_id", strconv.FormatInt(p.ReplyToMessageID, 10))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.Client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()

	var body sendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("telegram send: decoding response: %w", err)
	}
	if !body.OK {
		return fmt.Errorf("telegram send: %s", body.Description)
	}
	return nil
}

// Send posts a single HTML-formatted message to the configured chat.
func (t *Telegram) Send(ctx context.Context, text string) error {
	return t.sendMessage(ctx, sendParams{Text: text, ParseMode: "HTML"})
}

// Reply posts a plain-text reply to a specific message in the configured
// chat, used by tg-sync's confirmations and error messages.
func (t *Telegram) Reply(ctx context.Context, replyToMessageID int64, text string) error {
	return t.sendMessage(ctx, sendParams{Text: text, ReplyToMessageID: replyToMessageID})
}

// JobMessage formats a single job as the HTML message body described in
// the spec:
//
//	<b>{CompanyName}</b>
//	{Title}
//	{Location}
//	<a href="{URL}">Apply here</a>
//	#J{job_id}
//
// The trailing #J{job_id} tag lets tg-sync map a reply back to this job.
func JobMessage(job providers.Job) string {
	return fmt.Sprintf("<b>%s</b>\n%s\n%s\n<a href=\"%s\">Apply here</a>\n#J%d",
		html.EscapeString(job.CompanyName),
		html.EscapeString(job.Title),
		html.EscapeString(job.Location),
		job.URL,
		job.ID,
	)
}

var jobTagRe = regexp.MustCompile(`#J(\d+)`)

// ExtractJobID finds the #J{id} tag jobwatch appends to job notifications
// and returns the job id it encodes, if present.
func ExtractJobID(text string) (int64, bool) {
	m := jobTagRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// Update is a Telegram Bot API update, as returned by getUpdates.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// Message is a Telegram Bot API message.
type Message struct {
	MessageID      int64    `json:"message_id"`
	Chat           Chat     `json:"chat"`
	Text           string   `json:"text"`
	Caption        string   `json:"caption"`
	ReplyToMessage *Message `json:"reply_to_message"`
}

// Chat identifies a Telegram chat.
type Chat struct {
	ID int64 `json:"id"`
}

type getUpdatesResponse struct {
	OK          bool     `json:"ok"`
	Result      []Update `json:"result"`
	Description string   `json:"description"`
}

// GetUpdates fetches updates (messages, replies) delivered to the bot since
// offset, following the Telegram getUpdates long-polling convention:
// pass the previous call's max update_id + 1 as offset to acknowledge
// prior updates. timeout=0 makes this a single non-blocking poll, suited to
// jobwatch's run-once-and-exit tg-sync command.
func (t *Telegram) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	endpoint := fmt.Sprintf("%s/bot%s/getUpdates?offset=%d&timeout=0", t.BaseURL, t.Token, offset)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates: %w", err)
	}
	defer resp.Body.Close()

	var body getUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("telegram getUpdates: decoding response: %w", err)
	}
	if !body.OK {
		return nil, fmt.Errorf("telegram getUpdates: %s", body.Description)
	}
	return body.Result, nil
}

// NotifyJobs sends one message per job, rate-limited to 1/sec. If more than
// maxIndividualMessages jobs are passed, only the first maxIndividualMessages
// are sent individually, followed by one summary message for the rest.
func (t *Telegram) NotifyJobs(ctx context.Context, jobs []providers.Job) error {
	if len(jobs) == 0 {
		return nil
	}

	individual := jobs
	remaining := 0
	if len(jobs) > maxIndividualMessages {
		individual = jobs[:maxIndividualMessages]
		remaining = len(jobs) - maxIndividualMessages
	}

	var errs []string
	for i, job := range individual {
		if i > 0 {
			if err := sleepOrCancel(ctx, time.Second); err != nil {
				return err
			}
		}
		if err := t.Send(ctx, JobMessage(job)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if remaining > 0 {
		if err := sleepOrCancel(ctx, time.Second); err != nil {
			return err
		}
		summary := fmt.Sprintf("…and %d more, check dashboard", remaining)
		if err := t.Send(ctx, summary); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("telegram: %d/%d messages failed: %s", len(errs), len(individual)+boolToInt(remaining > 0), strings.Join(errs, "; "))
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sleepOrCancel(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
