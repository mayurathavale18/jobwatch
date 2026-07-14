package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"jobwatch/internal/config"
)

// Ashby fetches postings from the public Ashby job board API.
//
// Ashby's docs describe this endpoint as POST, but the live API only
// accepts GET (POST returns 401 Unauthorized; GET returns the board with
// no auth required). Verified against https://api.ashbyhq.com/posting-api/job-board/ashby.
type Ashby struct {
	Client  *http.Client
	BaseURL string
}

func NewAshby(client *http.Client) *Ashby {
	if client == nil {
		client = http.DefaultClient
	}
	return &Ashby{Client: client, BaseURL: "https://api.ashbyhq.com/posting-api/job-board"}
}

type ashbyResponse struct {
	Jobs []ashbyJob `json:"jobs"`
}

type ashbyJob struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Location    string `json:"location"`
	JobURL      string `json:"jobUrl"`
	ApplyURL    string `json:"applyUrl"`
	PublishedAt string `json:"publishedAt"`
	IsListed    bool   `json:"isListed"`
}

func (a *Ashby) Fetch(ctx context.Context, company config.Company) ([]Job, error) {
	url := fmt.Sprintf("%s/%s", a.BaseURL, company.Slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ashby %s: %w", company.Slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ashby %s: unexpected status %d", company.Slug, resp.StatusCode)
	}

	var body ashbyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("ashby %s: decode: %w", company.Slug, err)
	}

	return parseAshby(body, company)
}

func parseAshbyJSON(data []byte, company config.Company) ([]Job, error) {
	var body ashbyResponse
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("ashby %s: decode: %w", company.Slug, err)
	}
	return parseAshby(body, company)
}

func parseAshby(body ashbyResponse, company config.Company) ([]Job, error) {
	now := time.Now().UTC()
	jobs := make([]Job, 0, len(body.Jobs))
	for _, j := range body.Jobs {
		if !j.IsListed {
			continue
		}

		raw, err := json.Marshal(j)
		if err != nil {
			return nil, fmt.Errorf("ashby %s: marshal raw: %w", company.Slug, err)
		}

		var postedAt *time.Time
		if j.PublishedAt != "" {
			if t, err := time.Parse(time.RFC3339, j.PublishedAt); err == nil {
				postedAt = &t
			}
		}

		url := j.JobURL
		if url == "" {
			url = j.ApplyURL
		}

		jobs = append(jobs, Job{
			Provider:    "ashby",
			CompanySlug: company.Slug,
			CompanyName: company.Name,
			ExternalID:  j.ID,
			Title:       j.Title,
			Location:    j.Location,
			URL:         url,
			PostedAt:    postedAt,
			FirstSeenAt: now,
			Raw:         raw,
		})
	}
	return jobs, nil
}
