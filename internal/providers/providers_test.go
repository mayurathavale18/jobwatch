package providers

import (
	"encoding/json"
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

func TestParseRemoteOK(t *testing.T) {
	data := readFixture(t, "testdata/remoteok/sample.json")

	jobs, err := parseRemoteOKJSON(data)
	if err != nil {
		t.Fatalf("parseRemoteOKJSON: %v", err)
	}
	if len(jobs) != 4 {
		t.Fatalf("expected 4 jobs (legal blurb entry skipped), got %d", len(jobs))
	}

	j := jobs[0]
	if j.Provider != "remoteok" {
		t.Errorf("Provider = %q, want remoteok", j.Provider)
	}
	if j.CompanySlug != "acme-corp" || j.CompanyName != "Acme Corp" {
		t.Errorf("company fields wrong: slug=%q name=%q", j.CompanySlug, j.CompanyName)
	}
	if j.ExternalID != "1234" {
		t.Errorf("ExternalID = %q, want 1234", j.ExternalID)
	}
	if j.Title != "Backend Engineer" {
		t.Errorf("Title = %q", j.Title)
	}
	if j.URL != "https://remoteok.com/remote-jobs/acme-backend-engineer-1234" {
		t.Errorf("URL = %q", j.URL)
	}
	if j.PostedAt == nil {
		t.Fatal("PostedAt nil, want parsed timestamp")
	}

	j2 := jobs[1]
	if j2.URL != "https://remoteok.com/remote-jobs/widgetco-designer-5678" {
		t.Errorf("second entry URL = %q", j2.URL)
	}

	// Third entry has no url, only apply_url -- falls back to it.
	j3 := jobs[2]
	if j3.URL != "https://remoteok.com/remote-jobs/apply/9999" {
		t.Errorf("third entry URL = %q, want apply_url fallback", j3.URL)
	}

	// Fourth entry has no location in the API response -- RemoteOK is a
	// remote-only board, so this should normalize to "Worldwide" rather
	// than staying empty and silently failing every locations_include filter.
	j4 := jobs[3]
	if j4.Location != "Worldwide" {
		t.Errorf("fourth entry Location = %q, want \"Worldwide\" (empty-location fallback)", j4.Location)
	}
}

func TestParseWeWorkRemotely(t *testing.T) {
	data := readFixture(t, "testdata/wwr/sample.xml")

	jobs, err := parseWWRXML(data)
	if err != nil {
		t.Fatalf("parseWWRXML: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}

	j := jobs[0]
	if j.Provider != "wwr" {
		t.Errorf("Provider = %q, want wwr", j.Provider)
	}
	if j.CompanySlug != "acme-corp" || j.CompanyName != "Acme Corp" {
		t.Errorf("company fields wrong: slug=%q name=%q", j.CompanySlug, j.CompanyName)
	}
	if j.Title != "Backend Engineer" {
		t.Errorf("Title = %q, want %q (split off company prefix)", j.Title, "Backend Engineer")
	}
	if j.ExternalID != "acme-corp-backend-engineer" {
		t.Errorf("ExternalID = %q, want URL's last path segment", j.ExternalID)
	}
	if j.Location != "Anywhere in the World" {
		t.Errorf("Location = %q", j.Location)
	}
	if j.PostedAt == nil {
		t.Fatal("PostedAt nil, want parsed timestamp")
	}

	j2 := jobs[1]
	if j2.CompanyName != "WidgetCo" || j2.Title != "Senior Product Designer (d/w/m*)" {
		t.Errorf("second job wrong: company=%q title=%q", j2.CompanyName, j2.Title)
	}
}

func TestParseWeb3Career(t *testing.T) {
	data := readFixture(t, "testdata/web3career/sample.json")

	jobs, err := parseWeb3CareerJSON(data)
	if err != nil {
		t.Fatalf("parseWeb3CareerJSON: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs (malformed entry with empty apply_url and id=0 skipped), got %d", len(jobs))
	}

	j := jobs[0]
	if j.Provider != "web3career" {
		t.Errorf("Provider = %q, want web3career", j.Provider)
	}
	if j.CompanySlug != "acme-chain" || j.CompanyName != "Acme Chain" {
		t.Errorf("company fields wrong: slug=%q name=%q", j.CompanySlug, j.CompanyName)
	}
	if j.ExternalID != "151862" {
		t.Errorf("ExternalID = %q, want 151862", j.ExternalID)
	}
	if j.Title != "Backend Engineer" {
		t.Errorf("Title = %q", j.Title)
	}
	if j.URL != "https://web3.career/r/aaaa1111" {
		t.Errorf("URL = %q, want apply_url verbatim", j.URL)
	}
	if j.Location != " KA Bengaluru IN" {
		t.Errorf("Location = %q, want unmodified (is_remote=false)", j.Location)
	}
	if j.PostedAt == nil || j.PostedAt.Unix() != 1784958386 {
		t.Errorf("PostedAt = %v, want parsed from date_epoch 1784958386", j.PostedAt)
	}

	// is_remote=true but location text doesn't say "remote" -- must be
	// appended so the existing locations_include filter catches it.
	j2 := jobs[1]
	if j2.Location != " CA San Francisco US (Remote)" {
		t.Errorf("Location = %q, want \"(Remote)\" appended for is_remote job with non-remote-sounding location text", j2.Location)
	}

	// is_remote=true and location text already says "Remote" -- must not
	// be double-appended.
	j3 := jobs[2]
	if j3.Location != "Remote" {
		t.Errorf("Location = %q, want unchanged (already says Remote)", j3.Location)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Acme Corp":     "acme-corp",
		"  WidgetCo  ":  "widgetco",
		"A/B & C, Inc.": "a-b-c-inc",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
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

func TestParseRemotive(t *testing.T) {
	data := readFixture(t, "testdata/remotive/sample.json")

	var body remotiveResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	jobs, err := parseRemotive(body.Jobs)
	if err != nil {
		t.Fatalf("parseRemotive: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}

	j := jobs[0]
	if j.Provider != "remotive" {
		t.Errorf("Provider = %q, want remotive", j.Provider)
	}
	if j.CompanySlug != "acme-corp" || j.CompanyName != "Acme Corp" {
		t.Errorf("company fields wrong: slug=%q name=%q", j.CompanySlug, j.CompanyName)
	}
	if j.ExternalID != "2069746" {
		t.Errorf("ExternalID = %q, want 2069746", j.ExternalID)
	}
	if j.Location != "India" {
		t.Errorf("Location = %q, want India", j.Location)
	}
	if j.PostedAt == nil {
		t.Fatal("PostedAt nil, want parsed timestamp")
	}

	// Empty required-location means anywhere on a remote-only board.
	if jobs[1].Location != "Worldwide" {
		t.Errorf("empty location = %q, want Worldwide", jobs[1].Location)
	}
	if jobs[1].PostedAt != nil {
		t.Errorf("empty publication_date should give nil PostedAt")
	}
}
