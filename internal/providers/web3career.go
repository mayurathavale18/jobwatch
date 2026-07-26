package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"jobwatch/internal/config"
)

// Web3CareerAPITokenEnv names the environment variable holding the API
// token issued by web3.career's free Jobs API signup. See
// https://web3.career/web3-jobs-api.
const Web3CareerAPITokenEnv = "WEB3CAREER_API_TOKEN"

// Web3Career fetches postings from web3.career's official Jobs API. Like
// RemoteOK and We Work Remotely, this is a search aggregator: one Fetch
// call returns jobs across many different real companies, each carrying
// its own CompanySlug/CompanyName rather than the config entry's.
//
// The API's response shape is inconsistent across query parameter
// combinations -- passing remote=true or show_description=false has been
// observed (via live testing against a real token) to both change the
// wrapper array's length AND silently drop fields (id, company,
// date_epoch) this provider depends on. To stay robust to that, Fetch
// requests only token+limit and locates the jobs list by taking the
// *last* element of the top-level response array, rather than assuming
// a fixed index.
type Web3Career struct {
	Client   *http.Client
	BaseURL  string
	APIToken string
}

func NewWeb3Career(client *http.Client) *Web3Career {
	if client == nil {
		client = http.DefaultClient
	}
	return &Web3Career{
		Client:   client,
		BaseURL:  "https://web3.career/api/v1",
		APIToken: os.Getenv(Web3CareerAPITokenEnv),
	}
}

type web3CareerJob struct {
	ID        int64  `json:"id"`
	DateEpoch int64  `json:"date_epoch"`
	IsRemote  bool   `json:"is_remote"`
	Title     string `json:"title"`
	Company   string `json:"company"`
	Location  string `json:"location"`
	ApplyURL  string `json:"apply_url"`
}

func (w *Web3Career) Fetch(ctx context.Context, _ config.Company) ([]Job, error) {
	if w.APIToken == "" {
		return nil, fmt.Errorf("web3career: %s env var not set", Web3CareerAPITokenEnv)
	}

	reqURL := fmt.Sprintf("%s?token=%s&limit=100", w.BaseURL, w.APIToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := w.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web3career: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("web3career: unexpected status %d", resp.StatusCode)
	}

	var raw []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("web3career: decode: %w", err)
	}

	return parseWeb3Career(raw)
}

func parseWeb3CareerJSON(data []byte) ([]Job, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("web3career: decode: %w", err)
	}
	return parseWeb3Career(raw)
}

func parseWeb3Career(raw []json.RawMessage) ([]Job, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("web3career: empty response array")
	}

	var jobsRaw []json.RawMessage
	if err := json.Unmarshal(raw[len(raw)-1], &jobsRaw); err != nil {
		return nil, fmt.Errorf("web3career: decode jobs list: %w", err)
	}

	now := time.Now().UTC()
	jobs := make([]Job, 0, len(jobsRaw))
	for _, jr := range jobsRaw {
		var j web3CareerJob
		if err := json.Unmarshal(jr, &j); err != nil {
			return nil, fmt.Errorf("web3career: decode entry: %w", err)
		}
		if j.ApplyURL == "" || j.ID == 0 {
			continue
		}

		var postedAt *time.Time
		if j.DateEpoch > 0 {
			t := time.Unix(j.DateEpoch, 0).UTC()
			postedAt = &t
		}

		// web3.career's own location text often omits the word "remote"
		// even when is_remote is true (e.g. "CA San Francisco US") --
		// left as-is, that silently fails every locations_include filter
		// for a posting that's exactly what it's looking for.
		location := j.Location
		if j.IsRemote && !strings.Contains(strings.ToLower(location), "remote") {
			if strings.TrimSpace(location) == "" {
				location = "Remote"
			} else {
				location = location + " (Remote)"
			}
		}

		jobs = append(jobs, Job{
			Provider:    "web3career",
			CompanySlug: slugify(j.Company),
			CompanyName: j.Company,
			ExternalID:  strconv.FormatInt(j.ID, 10),
			Title:       j.Title,
			Location:    location,
			URL:         j.ApplyURL,
			PostedAt:    postedAt,
			FirstSeenAt: now,
			Raw:         json.RawMessage(jr),
		})
	}
	return jobs, nil
}
