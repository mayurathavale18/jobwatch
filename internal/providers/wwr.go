package providers

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"jobwatch/internal/config"
)

// WeWorkRemotely fetches postings from We Work Remotely's public RSS feed.
// Like RemoteOK, this is a search aggregator -- each Job carries its own
// real CompanySlug/CompanyName rather than the config entry's.
// GET https://weworkremotely.com/categories/remote-programming-jobs.rss
type WeWorkRemotely struct {
	Client  *http.Client
	FeedURL string
}

func NewWeWorkRemotely(client *http.Client) *WeWorkRemotely {
	if client == nil {
		client = http.DefaultClient
	}
	return &WeWorkRemotely{
		Client:  client,
		FeedURL: "https://weworkremotely.com/categories/remote-programming-jobs.rss",
	}
}

type wwrRSS struct {
	Channel struct {
		Items []wwrItem `xml:"item"`
	} `xml:"channel"`
}

type wwrItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	Region  string `xml:"region"`
	PubDate string `xml:"pubDate"`
}

func (w *WeWorkRemotely) Fetch(ctx context.Context, company config.Company) ([]Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.FeedURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := w.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weworkremotely: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weworkremotely: unexpected status %d", resp.StatusCode)
	}

	var feed wwrRSS
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("weworkremotely: decode: %w", err)
	}

	return parseWWR(feed.Channel.Items)
}

func parseWWRXML(data []byte) ([]Job, error) {
	var feed wwrRSS
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("weworkremotely: decode: %w", err)
	}
	return parseWWR(feed.Channel.Items)
}

func parseWWR(items []wwrItem) ([]Job, error) {
	now := time.Now().UTC()
	jobs := make([]Job, 0, len(items))
	for _, it := range items {
		if it.Link == "" {
			continue
		}

		// WWR's RSS title is always "{Company}: {Position}" -- there's no
		// separate company field in the feed.
		company, title := it.Title, it.Title
		if idx := strings.Index(it.Title, ": "); idx >= 0 {
			company = it.Title[:idx]
			title = it.Title[idx+2:]
		}

		externalID := it.Link
		if idx := strings.LastIndexByte(it.Link, '/'); idx >= 0 {
			externalID = it.Link[idx+1:]
		}

		var postedAt *time.Time
		if it.PubDate != "" {
			if t, err := time.Parse(time.RFC1123Z, it.PubDate); err == nil {
				postedAt = &t
			}
		}

		raw, err := xml.Marshal(it)
		if err != nil {
			return nil, fmt.Errorf("weworkremotely: marshal raw: %w", err)
		}

		jobs = append(jobs, Job{
			Provider:    "wwr",
			CompanySlug: slugify(company),
			CompanyName: strings.TrimSpace(company),
			ExternalID:  externalID,
			Title:       strings.TrimSpace(title),
			Location:    it.Region,
			URL:         it.Link,
			PostedAt:    postedAt,
			FirstSeenAt: now,
			Raw:         raw,
		})
	}
	return jobs, nil
}
