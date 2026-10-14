package store

import (
	"context"
	"testing"
)

func TestImportApplicationsPreservesWorkflowAndDrafts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := PlatformApplication{Platform: "instahyre", SourceID: "derived:role-1", Company: "Acme", Title: "Engineer", URL: "https://www.instahyre.com/candidate/opportunities/?status=1", SourceStatus: "Application Sent", JD: "Production Python systems", Contacts: []ApplicationContact{{Name: "Recruiter", Email: "hr@example.test", Evidence: "JD says contact hr@example.test"}}}
	if err := s.ImportApplications(ctx, []PlatformApplication{a}); err != nil {
		t.Fatal(err)
	}
	apps, err := s.Applications(ctx, "instahyre", 0)
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps=%v err=%v", apps, err)
	}
	id := apps[0].JobID
	if id == 0 {
		t.Fatal("missing linked job")
	}
	if n, err := s.QueueApplicationDrafts(ctx, "instahyre"); err != nil || n != 1 {
		t.Fatalf("queue=%d err=%v", n, err)
	}
	if n, err := s.QueueApplicationDrafts(ctx, "instahyre"); err != nil || n != 0 {
		t.Fatalf("duplicate queue=%d err=%v", n, err)
	}
	_, err = s.db.Exec(`UPDATE platform_applications SET draft_subject='Follow-up',draft_body='Saved body',draft_link='https://mail.google.com/mail/u/0/#all/123' WHERE job_id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`UPDATE jobs SET status='interview',email_draft_id='d123',email_to='hr@example.test' WHERE id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	a.Title = "Updated Engineer"
	a.DraftBody = "untrusted replacement"
	a.JobID = 999
	if err := s.ImportApplications(ctx, []PlatformApplication{a}); err != nil {
		t.Fatal(err)
	}
	apps, err = s.Applications(ctx, "", id)
	if err != nil || len(apps) != 1 {
		t.Fatal(err)
	}
	got := apps[0]
	if got.JobID != id || got.Title != a.Title || got.DraftBody != "Saved body" || got.EmailDraftID != "d123" || got.JobStatus != "interview" || got.ImportedAt == "" {
		t.Fatalf("lost state: %+v", got)
	}
	job, err := s.GetJob(ctx, id)
	if err != nil || job.ManualJDText != a.JD || job.Status != "interview" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	apps, err = s.Applications(ctx, "other", 0)
	if err != nil || len(apps) != 0 {
		t.Fatalf("platform isolation: %v %v", apps, err)
	}
	a.SourceID = "new"
	bad := a
	bad.SourceID = "bad"
	bad.URL = "https://evil.test"
	if err := s.ImportApplications(ctx, []PlatformApplication{a, bad}); err == nil {
		t.Fatal("accepted invalid batch")
	}
	apps, _ = s.Applications(ctx, "", 0)
	if len(apps) != 1 {
		t.Fatal("partial import")
	}
	if err := s.ImportApplications(ctx, []PlatformApplication{a, a}); err == nil {
		t.Fatal("accepted identity collision")
	}
	a.Contacts[0].Evidence = ""
	if err := a.Validate(); err == nil {
		t.Fatal("accepted email without evidence")
	}
}
