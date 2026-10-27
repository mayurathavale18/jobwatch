package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"jobwatch/internal/config"
)

type workdayTransport func(*http.Request) (*http.Response, error)

func (f workdayTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWorkdaySearchTextAcrossPages(t *testing.T) {
	for _, search := range []string{"", "India"} {
		t.Run(search, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: workdayTransport(func(r *http.Request) (*http.Response, error) {
				var body wdRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.SearchText != search || body.Offset != calls || body.Limit != 1 {
					t.Fatalf("page %d request = %+v, search %q", calls, body, search)
				}
				calls++
				response := `{"total":2,"jobPostings":[{"title":"Engineer","externalPath":"/job/Engineer_R1","bulletFields":["R1"]}]}`
				if calls == 2 {
					response = `{"total":0,"jobPostings":[{"title":"Engineer II","externalPath":"/job/Engineer-II_R2","bulletFields":["R2"]}]}`
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
			})}
			provider := NewWorkday(client)
			provider.PageSize, provider.MaxPages = 1, 3
			jobs, err := provider.Fetch(context.Background(), config.Company{Slug: "test", Host: "wd1", Site: "Jobs", SearchText: search})
			if err != nil || len(jobs) != 2 || calls != 2 {
				t.Fatalf("jobs=%d calls=%d err=%v", len(jobs), calls, err)
			}
		})
	}
}

func TestWorkdayLabelsDoNotCollapseRequisitions(t *testing.T) {
	postings := []wdPosting{
		{ExternalPath: "/job/Engineer_JR0284700", BulletFields: []string{"Spotlight Job", "JR0284700"}},
		{ExternalPath: "/job/Engineer-II_JR0288356", BulletFields: []string{"Spotlight Job", "JR0288356"}},
		{ExternalPath: "/job/Other_R3", BulletFields: []string{"Spotlight Job"}},
		{ExternalPath: "/job/No-Bullets_R4"},
	}
	jobs, err := parseWorkday(postings, "https://test.wd1.myworkdayjobs.com/Jobs", config.Company{Slug: "test"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"JR0284700", "JR0288356", "/job/Other_R3", "/job/No-Bullets_R4"}
	for i, job := range jobs {
		if job.ExternalID != want[i] {
			t.Errorf("job %d ID=%q, want %q", i, job.ExternalID, want[i])
		}
	}
}
