package jobsubmit

import (
	"context"
	"encoding/json"
	"fmt"
	"jobwatch/internal/config"
	"jobwatch/internal/providers"
	"jobwatch/internal/store"
	"net/url"
	"strings"
	"time"
)

// ImportRecord is an explicitly exported or pasted alert, not a scraped account.
type ImportRecord struct {
	URL      string `json:"url"`
	Company  string `json:"company"`
	Title    string `json:"title"`
	Location string `json:"location"`
	JD       string `json:"jd"`
	Source   string `json:"source"`
}

type ImportResult struct {
	Added    int              `json:"added"`
	Existing int              `json:"existing"`
	Preview  int              `json:"preview"`
	Boards   []config.Company `json:"suggested_boards"`
}

func SuggestedBoard(rawURL, name string) *config.Company {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return nil
	}
	provider := ""
	switch strings.ToLower(u.Hostname()) {
	case "boards.greenhouse.io", "job-boards.greenhouse.io":
		provider = "greenhouse"
	case "jobs.lever.co":
		provider = "lever"
	case "jobs.ashbyhq.com":
		provider = "ashby"
	}
	if provider == "" {
		return nil
	}
	return &config.Company{Name: name, Provider: provider, Slug: parts[0]}
}

// ImportRecords validates the entire batch before inserting and never fetches pages or sends messages.
func ImportRecords(ctx context.Context, st *store.Store, records []ImportRecord, apply bool) (ImportResult, error) {
	result := ImportResult{}
	boards := map[string]bool{}
	for i, record := range records {
		u, err := url.Parse(record.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.TrimSpace(record.Title) == "" || strings.TrimSpace(record.Company) == "" || strings.TrimSpace(record.Source) == "" {
			return result, fmt.Errorf("record %d requires an HTTPS URL without credentials, company, title and source", i+1)
		}
		if board := SuggestedBoard(record.URL, record.Company); board != nil {
			key := board.Provider + "/" + board.Slug
			if !boards[key] {
				result.Boards = append(result.Boards, *board)
				boards[key] = true
			}
		}
	}
	if !apply {
		result.Preview = len(records)
		return result, nil
	}
	tx, err := st.BeginTx(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	for _, record := range records {
		slug := manualSlug(record.URL)
		exists, err := st.ExistsTx(ctx, tx, "manual", slug, record.URL)
		if err != nil {
			return ImportResult{}, err
		}
		if exists {
			result.Existing++
			continue
		}
		raw, err := json.Marshal(record)
		if err != nil {
			return ImportResult{}, err
		}
		job := providers.Job{Provider: "manual", CompanySlug: slug, CompanyName: strings.TrimSpace(record.Company), ExternalID: record.URL,
			Title: strings.TrimSpace(record.Title), Location: record.Location, URL: record.URL, JDText: record.JD, Raw: raw, FirstSeenAt: time.Now().UTC()}
		id, err := st.InsertJob(ctx, tx, job, store.StatusNew)
		if err != nil {
			return ImportResult{}, err
		}
		if err := st.LinkSimilarJobsTx(ctx, tx, id, job, time.Now().Add(-30*24*time.Hour)); err != nil {
			return ImportResult{}, err
		}
		result.Added++
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}
