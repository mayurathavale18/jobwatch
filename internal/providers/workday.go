package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"jobwatch/internal/config"
)

// Workday fetches postings from a company's public Workday CXS job-search
// API: POST https://{tenant}.{host}.myworkdayjobs.com/wday/cxs/{tenant}/{site}/jobs
// where tenant is company.Slug and host/site come from company.Host/Site
// (e.g. Wells Fargo is tenant "wf", host "wd1", site "WellsFargoJobs").
//
// Large employers can have thousands of postings, and Workday rejects any
// page size over 20 (HTTP 400), so Fetch stops after MaxPages rather than
// paginating exhaustively -- fine for jobwatch's purpose (surfacing recent
// matches), since Workday returns postings most-recent-first by default.
type Workday struct {
	Client   *http.Client
	PageSize int
	MaxPages int
}

const (
	workdayPageSize = 20 // Workday CXS rejects any larger page size
	workdayMaxPages = 15 // 300 postings/company/cycle cap -- see doc comment above
)

func NewWorkday(client *http.Client) *Workday {
	if client == nil {
		client = http.DefaultClient
	}
	return &Workday{Client: client, PageSize: workdayPageSize, MaxPages: workdayMaxPages}
}

type wdRequest struct {
	AppliedFacets map[string]any `json:"appliedFacets"`
	Limit         int            `json:"limit"`
	Offset        int            `json:"offset"`
	SearchText    string         `json:"searchText"`
}

type wdResponse struct {
	Total       int         `json:"total"`
	JobPostings []wdPosting `json:"jobPostings"`
}

type wdPosting struct {
	Title         string   `json:"title"`
	ExternalPath  string   `json:"externalPath"`
	LocationsText string   `json:"locationsText"`
	PostedOn      string   `json:"postedOn"`
	BulletFields  []string `json:"bulletFields"`
}

func (w *Workday) Fetch(ctx context.Context, company config.Company) ([]Job, error) {
	baseURL := fmt.Sprintf("https://%s.%s.myworkdayjobs.com/wday/cxs/%s/%s",
		company.Slug, company.Host, company.Slug, company.Site)

	var all []wdPosting
	total := -1 // Workday only reports an accurate "total" on the first page; later pages report 0
	for page := range w.MaxPages {
		reqBody, err := json.Marshal(wdRequest{
			AppliedFacets: map[string]any{},
			Limit:         w.PageSize,
			Offset:        page * w.PageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("workday %s: encode request: %w", company.Slug, err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/jobs", bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := w.Client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("workday %s: %w", company.Slug, err)
		}

		var body wdResponse
		decErr := json.NewDecoder(resp.Body).Decode(&body)
		statusCode := resp.StatusCode
		resp.Body.Close()

		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("workday %s: unexpected status %d", company.Slug, statusCode)
		}
		if decErr != nil {
			return nil, fmt.Errorf("workday %s: decode: %w", company.Slug, decErr)
		}

		all = append(all, body.JobPostings...)
		if total < 0 {
			total = body.Total
		}
		if len(body.JobPostings) < w.PageSize || len(all) >= total {
			break
		}
	}

	return parseWorkday(all, baseURL, company)
}

func parseWorkdayJSON(data []byte, baseURL string, company config.Company) ([]Job, error) {
	var body wdResponse
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("workday %s: decode: %w", company.Slug, err)
	}
	return parseWorkday(body.JobPostings, baseURL, company)
}

func parseWorkday(postings []wdPosting, baseURL string, company config.Company) ([]Job, error) {
	now := time.Now().UTC()
	jobs := make([]Job, 0, len(postings))
	for _, p := range postings {
		raw, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("workday %s: marshal raw: %w", company.Slug, err)
		}

		// bulletFields[0] is the requisition ID (e.g. "R-561638") on every
		// Workday tenant observed so far -- stable and unique, so prefer it
		// as the dedupe key; externalPath is the fallback for postings
		// without one.
		externalID := p.ExternalPath
		if len(p.BulletFields) > 0 && p.BulletFields[0] != "" {
			externalID = p.BulletFields[0]
		}

		jobs = append(jobs, Job{
			Provider:    "workday",
			CompanySlug: company.Slug,
			CompanyName: company.Name,
			ExternalID:  externalID,
			Title:       p.Title,
			Location:    p.LocationsText,
			URL:         baseURL + p.ExternalPath,
			// PostedOn is relative text ("Posted Today", "Posted 3 Days
			// Ago") with no absolute-timestamp field available from the
			// list endpoint, so PostedAt is left nil rather than guessed.
			FirstSeenAt: now,
			Raw:         raw,
		})
	}
	return jobs, nil
}
