package jobsubmit

import (
	"context"
	"jobwatch/internal/store"
	"path/filepath"
	"testing"
)

func TestImportValidatesWholeBatchAndIsIdempotent(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	valid := ImportRecord{URL: "https://jobs.lever.co/acme/123", Company: "Acme", Title: "Backend Engineer", JD: "Full pasted description", Source: "LinkedIn alert export"}
	result, err := ImportRecords(ctx, st, []ImportRecord{valid}, false)
	if err != nil || result.Preview != 1 || len(result.Boards) != 1 || result.Boards[0].Slug != "acme" {
		t.Fatal(result, err)
	}
	if _, err := ImportRecords(ctx, st, []ImportRecord{valid, {URL: "file:///secret", Company: "Acme", Title: "bad"}}, true); err == nil {
		t.Fatal("invalid batch accepted")
	}
	missingSource := valid
	missingSource.Source = ""
	if _, err := ImportRecords(ctx, st, []ImportRecord{missingSource}, true); err == nil {
		t.Fatal("missing source accepted")
	}
	rows, _ := st.ListJobs(ctx, store.JobFilter{})
	if len(rows) != 0 {
		t.Fatal("partially imported invalid batch")
	}
	result, err = ImportRecords(ctx, st, []ImportRecord{valid, valid}, true)
	if err != nil || result.Added != 1 || result.Existing != 1 {
		t.Fatal(result, err)
	}
	result, err = ImportRecords(ctx, st, []ImportRecord{valid}, true)
	if err != nil || result.Existing != 1 || result.Added != 0 {
		t.Fatal(result, err)
	}
	rows, _ = st.ListJobs(ctx, store.JobFilter{})
	if len(rows) != 1 || rows[0].ManualJDText != valid.JD || rows[0].CompanyName != "Acme" {
		t.Fatal(rows)
	}
}

func TestSuggestedBoardOnlyRecognizesKnownHosts(t *testing.T) {
	if SuggestedBoard("https://jobs.lever.co.evil.test/acme/123", "Acme") != nil {
		t.Fatal("lookalike host accepted")
	}
	board := SuggestedBoard("https://job-boards.greenhouse.io/acme/jobs/123", "Acme")
	if board == nil || board.Provider != "greenhouse" || board.Slug != "acme" {
		t.Fatal(board)
	}
}
