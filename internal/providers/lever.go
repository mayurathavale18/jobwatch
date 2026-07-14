package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"jobwatch/internal/config"
)

// Lever fetches postings from the public Lever job board API.
// GET https://api.lever.co/v0/postings/{slug}?mode=json
type Lever struct {
	Client  *http.Client
	BaseURL string
}

func NewLever(client *http.Client) *Lever {
	if client == nil {
		client = http.DefaultClient
	}
	return &Lever{Client: client, BaseURL: "https://api.lever.co/v0/postings"}
}

type leverPosting struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	HostedURL  string `json:"hostedUrl"`
	ApplyURL   string `json:"applyUrl"`
	CreatedAt  int64  `json:"createdAt"` // epoch millis
	Categories struct {
		Location   string `json:"location"`
		Team       string `json:"team"`
		Commitment string `json:"commitment"`
	} `json:"categories"`
}

func (l *Lever) Fetch(ctx context.Context, company config.Company) ([]Job, error) {
	url := fmt.Sprintf("%s/%s?mode=json", l.BaseURL, company.Slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := l.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lever %s: %w", company.Slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lever %s: unexpected status %d", company.Slug, resp.StatusCode)
	}

	var postings []leverPosting
	if err := json.NewDecoder(resp.Body).Decode(&postings); err != nil {
		return nil, fmt.Errorf("lever %s: decode: %w", company.Slug, err)
	}

	return parseLever(postings, company)
}

func parseLeverJSON(data []byte, company config.Company) ([]Job, error) {
	var postings []leverPosting
	if err := json.Unmarshal(data, &postings); err != nil {
		return nil, fmt.Errorf("lever %s: decode: %w", company.Slug, err)
	}
	return parseLever(postings, company)
}

func parseLever(postings []leverPosting, company config.Company) ([]Job, error) {
	now := time.Now().UTC()
	jobs := make([]Job, 0, len(postings))
	for _, p := range postings {
		raw, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("lever %s: marshal raw: %w", company.Slug, err)
		}

		var postedAt *time.Time
		if p.CreatedAt > 0 {
			t := time.UnixMilli(p.CreatedAt).UTC()
			postedAt = &t
		}

		url := p.HostedURL
		if url == "" {
			url = p.ApplyURL
		}

		jobs = append(jobs, Job{
			Provider:    "lever",
			CompanySlug: company.Slug,
			CompanyName: company.Name,
			ExternalID:  p.ID,
			Title:       p.Text,
			Location:    p.Categories.Location,
			URL:         url,
			PostedAt:    postedAt,
			FirstSeenAt: now,
			Raw:         raw,
		})
	}
	return jobs, nil
}
