package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

type ApplicationContact struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Evidence string `json:"evidence"`
}

// PlatformApplication is a browser-observed application, independent of discovery filters.
type PlatformApplication struct {
	Platform     string               `json:"platform"`
	SourceID     string               `json:"sourceId"`
	Company      string               `json:"company"`
	Title        string               `json:"title"`
	Location     string               `json:"location"`
	URL          string               `json:"url"`
	AppliedAt    string               `json:"appliedAt"`
	SourceStatus string               `json:"sourceStatus"`
	JD           string               `json:"jd"`
	Contacts     []ApplicationContact `json:"contacts"`
	Metadata     map[string]string    `json:"metadata"`
	JobID        int64                `json:"jobId"`
	ImportedAt   string               `json:"importedAt"`
	DraftSubject string               `json:"draftSubject"`
	DraftBody    string               `json:"draftBody"`
	DraftLink    string               `json:"draftLink"`
	EmailTo      string               `json:"emailTo"`
	EmailDraftID string               `json:"emailDraftId"`
	EmailSentAt  string               `json:"emailSentAt"`
	JobStatus    string               `json:"jobStatus"`
}

func (a PlatformApplication) Validate() error {
	if a.Platform != "instahyre" {
		return fmt.Errorf("unsupported platform %q", a.Platform)
	}
	if strings.TrimSpace(a.SourceID) == "" || strings.TrimSpace(a.Company) == "" || strings.TrimSpace(a.Title) == "" {
		return fmt.Errorf("sourceId, company and title are required")
	}
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" || (u.Host != "www.instahyre.com" && u.Host != "instahyre.com") || u.User != nil {
		return fmt.Errorf("application URL must be an HTTPS Instahyre URL")
	}
	if a.SourceStatus != "Application Sent" {
		return fmt.Errorf("application must have observed Application Sent evidence")
	}
	for _, c := range a.Contacts {
		if c.Email != "" {
			addr, err := mail.ParseAddress(c.Email)
			if err != nil || addr.Address != c.Email || strings.ContainsAny(c.Email, "\r\n") || strings.TrimSpace(c.Evidence) == "" {
				return fmt.Errorf("contact email requires a valid address and source evidence")
			}
		}
	}
	return nil
}

// ImportApplications is atomic and repeatable; reimports preserve drafts and workflow status.
func (s *Store) ImportApplications(ctx context.Context, apps []PlatformApplication) error {
	seen := map[string]bool{}
	for _, a := range apps {
		if err := a.Validate(); err != nil {
			return err
		}
		key := a.Platform + ":" + a.SourceID
		if seen[key] {
			return fmt.Errorf("duplicate application identity %q", key)
		}
		seen[key] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, a := range apps {
		var id int64
		err = tx.QueryRowContext(ctx, `INSERT INTO jobs(provider,company_slug,company_name,external_id,title,location,url,first_seen_at,last_seen_at,status,manual_jd_text)
		VALUES(?, '', ?, ?, ?, ?, ?, ?, ?, 'applied', ?)
		ON CONFLICT(provider,company_slug,external_id) DO UPDATE SET company_name=excluded.company_name,title=excluded.title,location=excluded.location,url=excluded.url,last_seen_at=excluded.last_seen_at,
		manual_jd_text=CASE WHEN excluded.manual_jd_text!='' THEN excluded.manual_jd_text ELSE jobs.manual_jd_text END RETURNING id`, a.Platform, a.Company, a.SourceID, a.Title, a.Location, a.URL, now, now, a.JD).Scan(&id)
		if err != nil {
			return err
		}
		// Exclude server-owned state from the browser snapshot.
		a.JobID = 0
		a.ImportedAt = ""
		a.DraftSubject = ""
		a.DraftBody = ""
		a.DraftLink = ""
		a.EmailTo = ""
		a.EmailDraftID = ""
		a.EmailSentAt = ""
		a.JobStatus = ""
		payload, err := json.Marshal(a)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO platform_applications(job_id,platform,source_id,snapshot,imported_at) VALUES(?,?,?,?,?)
		ON CONFLICT(platform,source_id) DO UPDATE SET snapshot=excluded.snapshot,imported_at=excluded.imported_at`, id, a.Platform, a.SourceID, string(payload), now)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Applications(ctx context.Context, platform string, jobID int64) ([]PlatformApplication, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.snapshot,a.job_id,a.imported_at,a.draft_subject,a.draft_body,a.draft_link,j.email_to,j.email_draft_id,j.email_sent_at,j.status
	FROM platform_applications a JOIN jobs j ON j.id=a.job_id WHERE (?='' OR a.platform=?) AND (?=0 OR a.job_id=?) ORDER BY a.job_id DESC`, platform, platform, jobID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlatformApplication{}
	for rows.Next() {
		var a PlatformApplication
		var snapshot string
		if err := rows.Scan(&snapshot, &a.JobID, &a.ImportedAt, &a.DraftSubject, &a.DraftBody, &a.DraftLink, &a.EmailTo, &a.EmailDraftID, &a.EmailSentAt, &a.JobStatus); err != nil {
			return nil, err
		}
		var captured PlatformApplication
		if err := json.Unmarshal([]byte(snapshot), &captured); err != nil {
			return nil, err
		}
		a.Platform = captured.Platform
		a.SourceID = captured.SourceID
		a.Company = captured.Company
		a.Title = captured.Title
		a.Location = captured.Location
		a.URL = captured.URL
		a.AppliedAt = captured.AppliedAt
		a.SourceStatus = captured.SourceStatus
		a.JD = captured.JD
		a.Contacts = captured.Contacts
		a.Metadata = captured.Metadata
		out = append(out, a)
	}
	return out, rows.Err()
}

// QueueApplicationDrafts skips completed and in-flight work. Missing emails still get a local draft.
func (s *Store) QueueApplicationDrafts(ctx context.Context, platform string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO events(kind,payload,status,created_at,updated_at)
	SELECT ?, json_object('job_id',a.job_id), 'pending', ?, ? FROM platform_applications a JOIN jobs j ON j.id=a.job_id
	WHERE a.platform=? AND j.email_sent_at='' AND j.email_draft_id='' AND (a.draft_body='' OR j.email_to!='' OR EXISTS(SELECT 1 FROM json_each(a.snapshot,'$.contacts') c WHERE json_extract(c.value,'$.email')!=''))
	AND NOT EXISTS(SELECT 1 FROM events e WHERE e.kind=? AND e.status IN ('pending','running') AND json_extract(e.payload,'$.job_id')=a.job_id)`, EventApplicationDraft, nowRFC3339(), nowRFC3339(), platform, EventApplicationDraft)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) SaveApplicationDraft(ctx context.Context, jobID int64, to, subject, body string) error {
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(body) == "" {
		return fmt.Errorf("subject and body are required")
	}
	if to != "" {
		address, err := mail.ParseAddress(to)
		if err != nil || address.Address != to || strings.ContainsAny(to, "\r\n") {
			return fmt.Errorf("invalid recipient email")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE platform_applications SET draft_subject=?,draft_body=? WHERE job_id=?`, subject, body, jobID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("application not found")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET email_to=? WHERE id=?`, to, jobID); err != nil {
		return err
	}
	return tx.Commit()
}
