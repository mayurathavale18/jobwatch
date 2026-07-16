package providers

import (
	"os"
	"testing"
	"time"

	"jobwatch/internal/config"
)

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", path, err)
	}
	return data
}

func TestParseGreenhouse(t *testing.T) {
	data := readFixture(t, "testdata/greenhouse/stripe_sample.json")
	company := config.Company{Name: "Stripe", Provider: "greenhouse", Slug: "stripe"}

	jobs, err := parseGreenhouseJSON(data, company)
	if err != nil {
		t.Fatalf("parseGreenhouseJSON: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("expected at least one job")
	}

	j := jobs[0]
	if j.Provider != "greenhouse" {
		t.Errorf("Provider = %q, want greenhouse", j.Provider)
	}
	if j.CompanySlug != "stripe" || j.CompanyName != "Stripe" {
		t.Errorf("company fields wrong: slug=%q name=%q", j.CompanySlug, j.CompanyName)
	}
	if j.ExternalID == "" {
		t.Error("ExternalID empty")
	}
	if j.Title == "" {
		t.Error("Title empty")
	}
	if j.URL == "" {
		t.Error("URL empty")
	}
	if len(j.Raw) == 0 {
		t.Error("Raw empty")
	}
}

func TestParseLever(t *testing.T) {
	data := readFixture(t, "testdata/lever/razorpay_sample.json")
	company := config.Company{Name: "Razorpay", Provider: "lever", Slug: "razorpay"}

	jobs, err := parseLeverJSON(data, company)
	if err != nil {
		t.Fatalf("parseLeverJSON: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}

	j := jobs[0]
	if j.Provider != "lever" {
		t.Errorf("Provider = %q, want lever", j.Provider)
	}
	if j.ExternalID != "a1b2c3d4-e5f6-4789-9abc-def012345678" {
		t.Errorf("ExternalID = %q", j.ExternalID)
	}
	if j.Title != "Backend Engineer - Payments" {
		t.Errorf("Title = %q", j.Title)
	}
	if j.Location != "Bengaluru, India" {
		t.Errorf("Location = %q", j.Location)
	}
	if j.PostedAt == nil {
		t.Fatal("PostedAt nil, want parsed timestamp")
	}
	wantTime := time.UnixMilli(1751328000000).UTC()
	if !j.PostedAt.Equal(wantTime) {
		t.Errorf("PostedAt = %v, want %v", j.PostedAt, wantTime)
	}
}

func TestParseAshby(t *testing.T) {
	data := readFixture(t, "testdata/ashby/ashby_sample.json")
	company := config.Company{Name: "Ashby", Provider: "ashby", Slug: "ashby"}

	jobs, err := parseAshbyJSON(data, company)
	if err != nil {
		t.Fatalf("parseAshbyJSON: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("expected at least one job")
	}

	for _, j := range jobs {
		if j.Provider != "ashby" {
			t.Errorf("Provider = %q, want ashby", j.Provider)
		}
		if j.ExternalID == "" {
			t.Error("ExternalID empty")
		}
		if j.URL == "" {
			t.Error("URL empty")
		}
	}
}

func TestParseWorkday(t *testing.T) {
	data := readFixture(t, "testdata/workday/wellsfargo_sample.json")
	company := config.Company{Name: "Wells Fargo", Provider: "workday", Slug: "wf", Host: "wd1", Site: "WellsFargoJobs"}
	// The public careers site, not the /wday/cxs/{tenant} API path -- linking
	// to the API returns raw JSON in a browser instead of the real job page.
	baseURL := "https://wf.wd1.myworkdayjobs.com/WellsFargoJobs"

	jobs, err := parseWorkdayJSON(data, baseURL, company)
	if err != nil {
		t.Fatalf("parseWorkdayJSON: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}

	j := jobs[0]
	if j.Provider != "workday" {
		t.Errorf("Provider = %q, want workday", j.Provider)
	}
	if j.CompanySlug != "wf" || j.CompanyName != "Wells Fargo" {
		t.Errorf("company fields wrong: slug=%q name=%q", j.CompanySlug, j.CompanyName)
	}
	if j.ExternalID != "R-100001" {
		t.Errorf("ExternalID = %q, want R-100001 (bulletFields[0], not externalPath)", j.ExternalID)
	}
	if j.Title != "Software Engineer, Backend Platform" {
		t.Errorf("Title = %q", j.Title)
	}
	if j.Location != "BENGALURU, India" {
		t.Errorf("Location = %q", j.Location)
	}
	wantURL := baseURL + "/job/BENGALURU-India/Software-Engineer--Backend-Platform_R-100001"
	if j.URL != wantURL {
		t.Errorf("URL = %q, want %q", j.URL, wantURL)
	}
	if j.PostedAt != nil {
		t.Errorf("PostedAt = %v, want nil (Workday only exposes relative text, not a timestamp)", j.PostedAt)
	}
}

func TestParseAshbySkipsUnlisted(t *testing.T) {
	data := []byte(`{"jobs":[
		{"id":"1","title":"Listed Job","location":"Remote","jobUrl":"https://x/1","isListed":true},
		{"id":"2","title":"Unlisted Job","location":"Remote","jobUrl":"https://x/2","isListed":false}
	]}`)
	company := config.Company{Name: "Acme", Provider: "ashby", Slug: "acme"}

	jobs, err := parseAshbyJSON(data, company)
	if err != nil {
		t.Fatalf("parseAshbyJSON: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 listed job, got %d", len(jobs))
	}
	if jobs[0].ExternalID != "1" {
		t.Errorf("expected job 1 to survive, got %q", jobs[0].ExternalID)
	}
}
