package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"jobwatch/internal/store"
)

func TestLogInstahyreApplicationQueuesOnce(t *testing.T) {
	srv, st := newTestServer(t)
	submit := func(posting, pdf string) (int, apiManualJobResponse) {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for key, value := range map[string]string{
			"url": posting, "company": "Example Labs", "title": "Applied AI Engineer",
			"jd": "Build production agents with Python.",
		} {
			if err := form.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		file, err := form.CreateFormFile("resume", "submitted.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(file, pdf); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/jobs/instahyre", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		var result apiManualJobResponse
		if w.Code < 300 {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, result
	}

	url := "https://www.instahyre.com/job-example-42/"
	if code, _ := submit(url, "not a PDF"); code != http.StatusBadRequest {
		t.Fatalf("invalid attachment status = %d", code)
	}
	if code, _ := submit("https://instahyre.com.example.org/job/", "%PDF-1.7"); code != http.StatusBadRequest {
		t.Fatalf("untrusted posting origin status = %d", code)
	}
	code, first := submit(url+"?tracking=1", "%PDF-1.7 exact first upload")
	if code != http.StatusAccepted || first.ID == 0 {
		t.Fatalf("first submission = %d %+v", code, first)
	}
	code, duplicate := submit("https://instahyre.com/job-example-42", "%PDF-1.7 different file")
	if code != http.StatusOK || duplicate.ID != first.ID || !duplicate.AlreadyExisted {
		t.Fatalf("duplicate submission = %d %+v", code, duplicate)
	}
	job, err := st.GetJob(t.Context(), first.ID)
	if err != nil || job.Status != store.StatusApplied || job.Provider != "instahyre" {
		t.Fatalf("job = %+v, error = %v", job, err)
	}
	tx, err := st.BeginTx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var pdf []byte
	err = tx.QueryRowContext(t.Context(), "SELECT resume_pdf FROM instahyre_applications WHERE job_id = ?", first.ID).Scan(&pdf)
	tx.Rollback()
	if err != nil || string(pdf) != "%PDF-1.7 exact first upload" {
		t.Fatalf("saved attachment changed, error = %v", err)
	}
	assertOnlyEvent(t, st, store.EventInstahyreDraft, fmt.Sprintf(`{"job_id":%d}`, first.ID))
	event, err := st.ClaimEvent(t.Context())
	if err != nil || event == nil {
		t.Fatalf("claiming draft event: %v", err)
	}
	if err := st.FinishEvent(t.Context(), event.ID, "Gmail unavailable before submission"); err != nil {
		t.Fatal(err)
	}
	if code, _ := submit(url, "%PDF-1.7 replacement"); code != http.StatusOK {
		t.Fatalf("retry request status = %d", code)
	}
	retry, err := st.ClaimEvent(t.Context())
	if err != nil || retry == nil || retry.ID != event.ID {
		t.Fatalf("expected the failed event to be retried, got %+v: %v", retry, err)
	}

}
