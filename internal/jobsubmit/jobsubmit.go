// Package jobsubmit inserts a job from a bare URL jobwatch can't poll
// directly (Naukri/Keka/YC/Wellfound/etc), shared by both the Telegram
// manual-submit path (internal/tgsync) and the dashboard's "add job link"
// endpoint (internal/web) so the dedupe/slug logic exists exactly once.
package jobsubmit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"jobwatch/internal/providers"
	"jobwatch/internal/store"
)

// PageFetcher fetches a job posting page's <title>, used as a best-effort
// display title for a freshly-submitted URL. Interfaced so tests and the
// tg-sync fixture-backed fake don't make real HTTP requests.
type PageFetcher interface {
	FetchTitle(ctx context.Context, rawURL string) (string, error)
}

const maxPageFetchBytes = 200 * 1024

var titleTagRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// HTTPPageFetcher is the real PageFetcher used outside tests.
type HTTPPageFetcher struct{ Client *http.Client }

func (h HTTPPageFetcher) FetchTitle(ctx context.Context, rawURL string) (string, error) {
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; jobwatch/1.0; personal job tracker)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageFetchBytes))
	if err != nil {
		return "", err
	}

	m := titleTagRe.FindSubmatch(body)
	if m == nil {
		return "", nil
	}
	return strings.TrimSpace(html.UnescapeString(string(m[1]))), nil
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// companyFromURL guesses a display company name from a job URL's host
// (e.g. "www.keka.com" -> "Keka"). Placeholder, not a real company lookup
// -- correct it via a "note:" Telegram reply if it's wrong.
func companyFromURL(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "Unknown"
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	parts := strings.Split(host, ".")

	var root string
	if len(parts) > 2 {
		// Has subdomain beyond www, take the main domain (second from left)
		root = parts[1]
	} else if len(parts) >= 1 {
		// No subdomain or just www, take the first part
		root = parts[0]
	}

	if root == "" {
		return "Unknown"
	}
	return strings.ToUpper(root[:1]) + root[1:]
}

// manualSlug derives a stable company_slug from a job URL's host, for the
// (provider, company_slug, external_id) dedupe key.
func manualSlug(rawURL string) string {
	return nonAlnum.ReplaceAllString(strings.ToLower(companyFromURL(rawURL)), "-")
}

// InsertManualJob inserts rawURL as a new "manual" provider job (status
// "new", so it rides the existing tailor-resume pipeline the same as any
// polled job) and reports whether it already existed. Re-submitting the
// same URL is a no-op (dedupe key includes the URL itself as external_id).
func InsertManualJob(ctx context.Context, st *store.Store, rawURL string, pages PageFetcher) (id int64, alreadyExisted bool, company string, title string, err error) {
	company = companyFromURL(rawURL)
	slug := manualSlug(rawURL)

	tx, err := st.BeginTx(ctx)
	if err != nil {
		return 0, false, "", "", err
	}
	defer tx.Rollback() //nolint:errcheck // no-op if already committed

	exists, err := st.ExistsTx(ctx, tx, "manual", slug, rawURL)
	if err != nil {
		return 0, false, "", "", err
	}
	if exists {
		existingID, gErr := existingJobIDTx(ctx, tx, "manual", slug, rawURL)
		if gErr != nil {
			return 0, false, "", "", gErr
		}
		return existingID, true, company, "", nil
	}

	fetchedTitle, ferr := pages.FetchTitle(ctx, rawURL)
	if ferr != nil {
		fetchedTitle = ""
	}
	if fetchedTitle == "" {
		fetchedTitle = "(title unknown — reply \"note: <real title>\" to fix)"
	}

	job := providers.Job{
		Provider:    "manual",
		CompanySlug: slug,
		CompanyName: company,
		ExternalID:  rawURL,
		Title:       fetchedTitle,
		URL:         rawURL,
		FirstSeenAt: time.Now().UTC(),
		Raw:         json.RawMessage(`{}`),
	}

	newID, err := st.InsertJob(ctx, tx, job, store.StatusNew)
	if err != nil {
		return 0, false, "", "", err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, "", "", err
	}

	return newID, false, company, fetchedTitle, nil
}

// existingJobIDTx looks up the row id for an already-existing (provider,
// company_slug, external_id) triple within a transaction, used when
// InsertManualJob finds a duplicate and needs to report which job it already is.
func existingJobIDTx(ctx context.Context, tx *sql.Tx, provider, companySlug, externalID string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM jobs WHERE provider = ? AND company_slug = ? AND external_id = ? LIMIT 1`,
		provider, companySlug, externalID,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("job existed per ExistsTx but not found in query")
	}
	return id, err
}
