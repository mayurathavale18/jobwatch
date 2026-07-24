// Package providers normalizes job postings from supported ATS APIs into a
// single Job struct.
package providers

import (
	"context"
	"encoding/json"
	"time"

	"jobwatch/internal/config"
)

// Job is the normalized representation of a single job posting, regardless
// of which ATS it came from.
type Job struct {
	// ID is the database row id. It is zero for freshly-fetched jobs and
	// populated by the poller after insert, so it can be embedded in
	// outgoing Telegram notifications for the #J{id} reply-tag.
	ID          int64
	Provider    string
	CompanySlug string
	CompanyName string
	ExternalID  string
	Title       string
	Location    string
	URL         string
	PostedAt    *time.Time
	FirstSeenAt time.Time
	Raw         json.RawMessage
	// JDText is user-supplied JD text (pasted or from an uploaded file)
	// from the dashboard's manual-submit form. Empty for every polled
	// provider -- only jobsubmit.InsertManualJob ever sets it.
	JDText string
}

// Provider fetches the current set of open postings for one company.
type Provider interface {
	Fetch(ctx context.Context, company config.Company) ([]Job, error)
}

// ForName returns the Provider implementation for the given provider name
// ("greenhouse", "lever", "ashby", "workday", "remoteok", "wwr"), as
// configured in config.yaml.
func ForName(name string) Provider {
	switch name {
	case "greenhouse":
		return NewGreenhouse(nil)
	case "lever":
		return NewLever(nil)
	case "ashby":
		return NewAshby(nil)
	case "workday":
		return NewWorkday(nil)
	case "remoteok":
		return NewRemoteOK(nil)
	case "wwr":
		return NewWeWorkRemotely(nil)
	default:
		return nil
	}
}
