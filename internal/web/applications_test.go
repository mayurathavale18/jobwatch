package web

import (
	"encoding/json"
	"fmt"
	"jobwatch/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplicationImportAndComposerContext(t *testing.T) {
	srv, _ := newTestServer(t)
	body := `{"applications":[{"platform":"instahyre","sourceId":"role-1","company":"Acme","title":"Backend engineer","url":"https://www.instahyre.com/candidate/opportunities/?status=1","sourceStatus":"Application Sent","appliedAt":"October 4, 2026","jd":"Python and Go"}]}`
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/applications/import", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/applications?platform=instahyre", nil))
	var resp struct {
		Applications []store.PlatformApplication `json:"applications"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Applications) != 1 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/jobs/%d", resp.Applications[0].JobID), nil))
	var job apiJob
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Application == nil || job.Application.JD != "Python and Go" || job.Status != "applied" {
		t.Fatalf("missing context: %+v", job)
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/jobs/%d/email", job.ID), strings.NewReader(`{"action":"save_local","subject":"Follow-up","body":"Edited by candidate","to":""}`)))
	if w.Code != 200 {
		t.Fatalf("save local: %d %s", w.Code, w.Body.String())
	}
	for _, invalid := range []string{`{}`, strings.ReplaceAll(body, "Application Sent", "Recommended"), strings.ReplaceAll(body, "https://www.instahyre.com", "javascript:evil"), body + ` {}`} {
		w = httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/applications/import", strings.NewReader(invalid)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("accepted bad import: %d %s", w.Code, w.Body.String())
		}
	}
}
