package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"jobwatch/internal/providers"
)

func newMockServer(t *testing.T, ok bool) (*httptest.Server, *int64) {
	t.Helper()
	var count int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&count, 1)
		w.Header().Set("Content-Type", "application/json")
		if ok {
			w.Write([]byte(`{"ok":true}`))
		} else {
			w.Write([]byte(`{"ok":false,"description":"bad request"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

func TestNewRequiresTokenAndChatID(t *testing.T) {
	if _, err := New("", "chat"); err == nil {
		t.Error("expected error for empty token")
	}
	if _, err := New("token", ""); err == nil {
		t.Error("expected error for empty chat id")
	}
	if _, err := New("token", "chat"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendSuccess(t *testing.T) {
	srv, count := newMockServer(t, true)
	tg := &Telegram{Client: srv.Client(), BaseURL: srv.URL, Token: "t", ChatID: "c"}

	if err := tg.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if atomic.LoadInt64(count) != 1 {
		t.Errorf("expected 1 request, got %d", *count)
	}
}

func TestSendFailure(t *testing.T) {
	srv, _ := newMockServer(t, false)
	tg := &Telegram{Client: srv.Client(), BaseURL: srv.URL, Token: "t", ChatID: "c"}

	err := tg.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad request") {
		t.Errorf("error = %v, want to contain 'bad request'", err)
	}
}

func TestJobMessageFormat(t *testing.T) {
	job := providers.Job{
		ID:          42,
		CompanyName: "Stripe",
		Title:       "Backend Engineer",
		Location:    "Remote",
		URL:         "https://stripe.com/jobs/1",
	}
	got := JobMessage(job)
	want := "<b>Stripe</b>\nBackend Engineer\nRemote\n<a href=\"https://stripe.com/jobs/1\">Apply here</a>\n#J42"
	if got != want {
		t.Errorf("JobMessage =\n%s\nwant:\n%s", got, want)
	}
}

func TestExtractJobID(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		wantID int64
		wantOK bool
	}{
		{"tag present", "Some text\n#J123", 123, true},
		{"tag in middle", "prefix #J7 suffix", 7, true},
		{"absent", "no tag here", 0, false},
		{"empty", "", 0, false},
		{"malformed hash only", "#J", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := ExtractJobID(tt.text)
			if ok != tt.wantOK || id != tt.wantID {
				t.Errorf("ExtractJobID(%q) = (%d, %v), want (%d, %v)", tt.text, id, ok, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestExtractJobIDInCaption(t *testing.T) {
	// Same extraction function is used against either .Text or .Caption of
	// the replied-to message; caption is just another string source.
	caption := "<b>Stripe</b>\nBackend Engineer\nRemote\n<a href=\"https://x\">Apply here</a>\n#J99"
	id, ok := ExtractJobID(caption)
	if !ok || id != 99 {
		t.Errorf("ExtractJobID(caption) = (%d, %v), want (99, true)", id, ok)
	}
}

// The >20-jobs summary-batching branch (21 messages, ~20s of real rate-limit
// sleeps) is intentionally not exercised end-to-end here to keep the suite
// fast; TestNotifyJobsBatchingLogicSmall covers the send/count wiring, and
// the batching threshold itself is a simple slice-index check in NotifyJobs.

func TestNotifyJobsBatchingLogicSmall(t *testing.T) {
	srv, count := newMockServer(t, true)
	tg := &Telegram{Client: srv.Client(), BaseURL: srv.URL, Token: "t", ChatID: "c"}

	jobs := []providers.Job{
		{CompanyName: "Acme", Title: "Engineer", Location: "Remote", URL: "https://x/1"},
		{CompanyName: "Acme", Title: "Engineer 2", Location: "Remote", URL: "https://x/2"},
	}

	if err := tg.NotifyJobs(context.Background(), jobs); err != nil {
		t.Fatalf("NotifyJobs: %v", err)
	}
	if atomic.LoadInt64(count) != 2 {
		t.Errorf("expected 2 requests for 2 jobs, got %d", *count)
	}
}

func TestNotifyJobsEmpty(t *testing.T) {
	tg := &Telegram{Client: http.DefaultClient, BaseURL: "http://unused", Token: "t", ChatID: "c"}
	if err := tg.NotifyJobs(context.Background(), nil); err != nil {
		t.Errorf("NotifyJobs(nil) should be a no-op, got: %v", err)
	}
}
