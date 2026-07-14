package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"jobwatch/internal/config"
)

// Greenhouse fetches postings from the public Greenhouse job board API.
// GET https://boards-api.greenhouse.io/v1/boards/{slug}/jobs?content=true
type Greenhouse struct {
	Client  *http.Client
	BaseURL string
}

func NewGreenhouse(client *http.Client) *Greenhouse {
	if client == nil {
		client = http.DefaultClient
	}
	return &Greenhouse{Client: client, BaseURL: "https://boards-api.greenhouse.io/v1/boards"}
}

type ghResponse struct {
	Jobs []ghJob `json:"jobs"`
}

type ghJob struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	AbsoluteURL    string `json:"absolute_url"`
	UpdatedAt      string `json:"updated_at"`
	FirstPublished string `json:"first_published"`
	Location       struct {
		Name string `json:"name"`
	} `json:"location"`
}

func (g *Greenhouse) Fetch(ctx context.Context, company config.Company) ([]Job, error) {
	url := fmt.Sprintf("%s/%s/jobs?content=true", g.BaseURL, company.Slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("greenhouse %s: %w", company.Slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("greenhouse %s: unexpected status %d", company.Slug, resp.StatusCode)
	}

	var body ghResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("greenhouse %s: decode: %w", company.Slug, err)
	}

	return parseGreenhouse(body, company)
}

func parseGreenhouseJSON(data []byte, company config.Company) ([]Job, error) {
	var body ghResponse
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("greenhouse %s: decode: %w", company.Slug, err)
	}
	return parseGreenhouse(body, company)
}

func parseGreenhouse(body ghResponse, company config.Company) ([]Job, error) {
	now := time.Now().UTC()
	jobs := make([]Job, 0, len(body.Jobs))
	for _, j := range body.Jobs {
		raw, err := json.Marshal(j)
		if err != nil {
			return nil, fmt.Errorf("greenhouse %s: marshal raw: %w", company.Slug, err)
		}

		var postedAt *time.Time
		if ts := firstNonEmpty(j.FirstPublished, j.UpdatedAt); ts != "" {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				postedAt = &t
			}
		}

		jobs = append(jobs, Job{
			Provider:    "greenhouse",
			CompanySlug: company.Slug,
			CompanyName: company.Name,
			ExternalID:  strconv.FormatInt(j.ID, 10),
			Title:       j.Title,
			Location:    j.Location.Name,
			URL:         j.AbsoluteURL,
			PostedAt:    postedAt,
			FirstSeenAt: now,
			Raw:         raw,
		})
	}
	return jobs, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
