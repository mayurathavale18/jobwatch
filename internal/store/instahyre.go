package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"jobwatch/internal/providers"
)

const EventInstahyreDraft = "instahyre_draft"

type InstahyreApplication struct {
	URL, Company, Title, Description        string
	RecipientName, RecipientEmail, WorkLink string
	ResumeName                              string
	ResumePDF                               []byte
}

// LogInstahyreApplication saves the job, exact submitted PDF, and draft
// event together. Repeated submissions retain the original application.
func (s *Store) LogInstahyreApplication(ctx context.Context, a InstahyreApplication) (id int64, existed bool, err error) {
	tx, err := s.BeginTx(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(ctx, "SELECT job_id FROM instahyre_applications WHERE url = ?", a.URL).Scan(&id)
	if err == nil {
		// A safe failure (for example, Gmail credentials were unavailable
		// before a request was made) can be retried by logging the same URL.
		payload, err := json.Marshal(map[string]int64{"job_id": id})
		if err != nil {
			return 0, false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE events SET status = 'pending', attempts = 0, error = '', updated_at = ?
			WHERE id = (SELECT MAX(id) FROM events WHERE kind = ? AND payload = ?)
			AND status = 'failed' AND EXISTS (
				SELECT 1 FROM instahyre_applications a JOIN jobs j ON j.id = a.job_id
				WHERE a.job_id = ? AND a.draft_submitting = 0 AND j.status = 'applied'
				AND j.email_draft_id = '' AND j.email_sent_at = ''
			)`, nowRFC3339(), EventInstahyreDraft, string(payload), id)
		if err != nil {
			return 0, false, err
		}
		return id, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}

	now := time.Now().UTC()
	job := providers.Job{Provider: "instahyre", CompanySlug: "instahyre", CompanyName: a.Company,
		ExternalID: a.URL, Title: a.Title, URL: a.URL, JDText: a.Description, FirstSeenAt: now}
	id, err = s.InsertJob(ctx, tx, job, StatusNew)
	if err != nil {
		return 0, false, err
	}
	if err = s.UpdateStatusTx(ctx, tx, id, StatusApplied); err != nil {
		return 0, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO instahyre_applications
		(job_id,url,recipient_name,recipient_email,work_link,resume_name,resume_pdf)
		VALUES (?,?,?,?,?,?,?)`, id, a.URL, a.RecipientName, a.RecipientEmail, a.WorkLink, a.ResumeName, a.ResumePDF)
	if err != nil {
		return 0, false, err
	}
	payload, err := json.Marshal(map[string]int64{"job_id": id})
	if err != nil {
		return 0, false, err
	}
	stamp := now.Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `INSERT INTO events (kind,payload,status,created_at,updated_at)
		VALUES (?,?,'pending',?,?)`, EventInstahyreDraft, string(payload), stamp, stamp)
	if err != nil {
		return 0, false, err
	}
	return id, false, tx.Commit()
}
