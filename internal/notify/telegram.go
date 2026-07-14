// Package notify sends Telegram notifications for new job postings.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
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

// Send posts a single HTML-formatted message to the configured chat.
func (t *Telegram) Send(ctx context.Context, text string) error {
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", t.BaseURL, t.Token)

	form := url.Values{}
	form.Set("chat_id", t.ChatID)
	form.Set("text", text)
	form.Set("parse_mode", "HTML")
	form.Set("disable_web_page_preview", "true")

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

// JobMessage formats a single job as the HTML message body described in
// the spec:
//
//	<b>{CompanyName}</b>
//	{Title}
//	{Location}
//	<a href="{URL}">Apply here</a>
func JobMessage(job providers.Job) string {
	return fmt.Sprintf("<b>%s</b>\n%s\n%s\n<a href=\"%s\">Apply here</a>",
		html.EscapeString(job.CompanyName),
		html.EscapeString(job.Title),
		html.EscapeString(job.Location),
		job.URL,
	)
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
