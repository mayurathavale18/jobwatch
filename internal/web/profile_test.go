package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"jobwatch/internal/profile"
	"jobwatch/internal/store"
)

func profileCall(s *Server, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/profile", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestProfileAPIReadCreateUpdateAndConflict(t *testing.T) {
	s, st := newTestServer(t)
	w := profileCall(s, "GET", "")
	if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("empty read: %d %v", w.Code, w.Header())
	}
	fixture, err := os.ReadFile("../../examples/profile.example.json")
	if err != nil {
		t.Fatal(err)
	}
	w = profileCall(s, "PUT", string(fixture))
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var saved profile.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || saved.UpdatedAt.IsZero() || saved.Profile.Identity.Name != "Alex Example" {
		t.Fatalf("unexpected snapshot: %+v", saved)
	}
	saved.Profile.Identity.Name = "Sam Fictional"
	request, _ := json.Marshal(saved)
	w = profileCall(s, "PUT", string(request))
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	// Another browser or the CLI has already advanced the revision.
	w = profileCall(s, "PUT", string(request))
	if w.Code != 409 {
		t.Fatalf("stale update: %d", w.Code)
	}
	w = profileCall(s, "PUT", string(fixture))
	if w.Code != 409 {
		t.Fatalf("stale create: %d", w.Code)
	}
	w = profileCall(s, "GET", "")
	got, err := profile.Decode(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 || got.Profile.Identity.Name != "Sam Fictional" || len(got.Profile.Search.Sources) != 1 || got.Profile.Automation.DailyBudget == nil {
		t.Fatal("round trip lost data or conflict changed saved profile")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("profile must not be cached")
	}
	stored, err := st.GetProfile(context.Background())
	if err != nil || stored.Revision != 2 {
		t.Fatalf("stored revision: %d, %v", stored.Revision, err)
	}
}

func TestProfileAPIUsesSharedValidationWithoutWriting(t *testing.T) {
	tests := map[string]string{
		"invalid email":     `{"revision":0,"profile":{"schemaVersion":1,"identity":{"email":"not-an-email"}}}`,
		"unknown field":     `{"revision":0,"profile":{"schemaVersion":1,"typo":true}}`,
		"duplicate keys":    `{"revision":0,"Revision":1,"profile":{"schemaVersion":1}}`,
		"trailing document": `{"profile":{"schemaVersion":1}} {}`,
		"oversized":         strings.Repeat(" ", profile.MaxImportBytes+1),
		"missing evidence":  `{"profile":{"schemaVersion":1,"background":{"skills":[{"id":"go","name":"Go","level":"used","evidenceIds":["missing"]}]}}}`,
		"reversed dates":    `{"profile":{"schemaVersion":1,"background":{"education":[{"id":"school","institution":"Fictional U","qualification":"B.Tech","startDate":"2024-01","endDate":"2023-01"}]}}}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			s, st := newTestServer(t)
			w := profileCall(s, "PUT", body)
			if w.Code != 400 {
				t.Fatalf("status: %d %s", w.Code, w.Body)
			}
			if _, err := st.GetProfile(context.Background()); err != store.ErrProfileNotFound {
				t.Fatalf("invalid profile was written: %v", err)
			}
		})
	}
}

func TestProfileAPIFailedEditKeepsSavedProfile(t *testing.T) {
	s, st := newTestServer(t)
	_, err := st.SaveProfile(context.Background(), profile.Profile{SchemaVersion: 1, Identity: profile.Identity{Name: "Fictional Candidate"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	w := profileCall(s, "PUT", `{"revision":1,"profile":{"schemaVersion":1,"identity":{"email":"invalid"}}}`)
	if w.Code != 400 {
		t.Fatalf("status: %d", w.Code)
	}
	stored, err := st.GetProfile(context.Background())
	if err != nil || stored.Revision != 1 || stored.Profile.Identity.Name != "Fictional Candidate" {
		t.Fatal("invalid edit modified stored data")
	}
}

func TestProfileAPIRejectsCrossOriginAndWrongContentType(t *testing.T) {
	s, _ := newTestServer(t)
	for _, test := range []struct {
		contentType, origin, site string
		status                    int
	}{
		{"text/plain", "", "", 415},
		{"application/json", "https://unrelated.example", "", 403},
		{"application/json", "", "cross-site", 403},
		{"application/json", "", "same-origin", 200},
	} {
		r := httptest.NewRequest("PUT", "http://jobwatch.example/api/profile", strings.NewReader(`{"profile":{"schemaVersion":1}}`))
		r.Header.Set("Content-Type", test.contentType)
		r.Header.Set("Origin", test.origin)
		r.Header.Set("Sec-Fetch-Site", test.site)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%+v: %d %s", test, w.Code, w.Body)
		}
	}
}
