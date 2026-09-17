package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"jobwatch/internal/config"
)

// Remotive fetches postings from Remotive's public remote-jobs API. Like
// RemoteOK, it's a search aggregator: one Fetch call returns jobs across
// many real companies, each carrying its own CompanySlug/CompanyName.
// GET https://remotive.com/api/remote-jobs?category=software-dev
type Remotive struct {
	Client  *http.Client
	BaseURL string
}

func NewRemotive(client *http.Client) *Remotive {
	if client == nil {
		client = http.DefaultClient
	}
	return &Remotive{Client: client, BaseURL: "https://remotive.com/api/remote-jobs?category=software-dev"}
}

type remotiveJob struct {
	ID       int64  `json:"id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Company  string `json:"company_name"`
	Location string `json:"candidate_required_location"`
	Date     string `json:"publication_date"`
}

type remotiveResponse struct {
	Jobs []json.RawMessage `json:"jobs"`
}

func (r *Remotive) Fetch(ctx context.Context, company config.Company) ([]Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.BaseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jobwatch (personal job tracker)")

	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remotive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remotive: unexpected status %d", resp.StatusCode)
	}

	var body remotiveResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("remotive: decode: %w", err)
	}
	return parseRemotive(body.Jobs)
}

func parseRemotive(raw []json.RawMessage) ([]Job, error) {
	jobs := make([]Job, 0, len(raw))
	for _, r := range raw {
		var j remotiveJob
		if err := json.Unmarshal(r, &j); err != nil {
			return nil, fmt.Errorf("remotive: decode entry: %w", err)
		}
		if j.ID == 0 {
			continue
		}

		var postedAt *time.Time
		if j.Date != "" {
			// Remotive's timestamps have no timezone suffix, e.g.
			// "2026-09-14T20:33:27".
			if t, err := time.Parse("2006-01-02T15:04:05", j.Date); err == nil {
				postedAt = &t
			}
		}

		// Remote-only board: an empty required-location means "anywhere",
		// not "unknown" -- same reasoning as RemoteOK.
		location := j.Location
		if strings.TrimSpace(location) == "" {
			location = "Worldwide"
		}

		jobs = append(jobs, Job{
			Provider:    "remotive",
			CompanySlug: slugify(j.Company),
			CompanyName: j.Company,
			ExternalID:  strconv.FormatInt(j.ID, 10),
			Title:       j.Title,
			Location:    location,
			URL:         j.URL,
			PostedAt:    postedAt,
			FirstSeenAt: time.Now().UTC(),
			Raw:         json.RawMessage(r),
		})
	}
	return jobs, nil
}
