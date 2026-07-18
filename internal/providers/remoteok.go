package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"jobwatch/internal/config"
)

// RemoteOK fetches postings from RemoteOK's public jobs API. Unlike the
// ATS providers (Greenhouse, Lever, ...), this is a search aggregator: one
// Fetch call returns jobs across many different real companies, each
// carrying its own CompanySlug/CompanyName rather than the config entry's.
// GET https://remoteok.com/api
type RemoteOK struct {
	Client  *http.Client
	BaseURL string
}

func NewRemoteOK(client *http.Client) *RemoteOK {
	if client == nil {
		client = http.DefaultClient
	}
	return &RemoteOK{Client: client, BaseURL: "https://remoteok.com/api"}
}

type remoteOKJob struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Date     string `json:"date"`
	Company  string `json:"company"`
	Position string `json:"position"`
	Location string `json:"location"`
	URL      string `json:"url"`
	ApplyURL string `json:"apply_url"`
}

func (r *RemoteOK) Fetch(ctx context.Context, company config.Company) ([]Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.BaseURL, nil)
	if err != nil {
		return nil, err
	}
	// RemoteOK's API terms ask that consumers identify themselves and link
	// back to remoteok.com (see the "legal" field in every response) --
	// the default Go User-Agent also gets blocked by some CDN rules.
	req.Header.Set("User-Agent", "jobwatch (personal job tracker; https://remoteok.com)")

	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remoteok: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remoteok: unexpected status %d", resp.StatusCode)
	}

	var raw []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("remoteok: decode: %w", err)
	}

	return parseRemoteOK(raw)
}

func parseRemoteOKJSON(data []byte) ([]Job, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("remoteok: decode: %w", err)
	}
	return parseRemoteOK(raw)
}

func parseRemoteOK(raw []json.RawMessage) ([]Job, error) {
	jobs := make([]Job, 0, len(raw))
	for _, r := range raw {
		var j remoteOKJob
		if err := json.Unmarshal(r, &j); err != nil {
			return nil, fmt.Errorf("remoteok: decode entry: %w", err)
		}
		// The API's first entry is a "legal"/attribution blurb, not a job
		// -- it has no id. Skip anything without one.
		if j.ID == "" {
			continue
		}

		var postedAt *time.Time
		if j.Date != "" {
			if t, err := time.Parse(time.RFC3339, j.Date); err == nil {
				postedAt = &t
			}
		}

		url := j.URL
		if url == "" {
			url = j.ApplyURL
		}

		jobs = append(jobs, Job{
			Provider:    "remoteok",
			CompanySlug: slugify(j.Company),
			CompanyName: j.Company,
			ExternalID:  j.ID,
			Title:       j.Position,
			Location:    j.Location,
			URL:         url,
			PostedAt:    postedAt,
			FirstSeenAt: time.Now().UTC(),
			Raw:         json.RawMessage(r),
		})
	}
	return jobs, nil
}

var slugifyNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify normalizes a company name into a stable, URL-safe slug. Search
// aggregators (RemoteOK, We Work Remotely) don't hand us a company slug the
// way per-company ATS APIs do, so we derive one from the display name.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugifyNonAlnum.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
