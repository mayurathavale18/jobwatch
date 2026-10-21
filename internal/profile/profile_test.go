package profile

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func example(t *testing.T) Snapshot {
	t.Helper()
	input, err := os.Open("../../examples/profile.example.json")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	snapshot, err := Decode(input)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestExampleAndUnknownValuesRoundTrip(t *testing.T) {
	snapshot := example(t)
	if snapshot.Profile.Search.MaxRequiredExperienceYears != nil || snapshot.Profile.Search.Preferences.WorkAuthorization[0].RequiresSponsorship != nil {
		t.Fatal("unspecified constraints must stay unknown")
	}
	if snapshot.Profile.Outreach.AutoDraft == nil || *snapshot.Profile.Outreach.AutoDraft {
		t.Fatal("explicit false must survive decoding")
	}
	snapshot.Profile.Search.Preferences.Compensation[0].Minimum = nil
	target := int64(2000000)
	snapshot.Profile.Search.Preferences.Compensation[0].Target = &target
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Decode(strings.NewReader(string(data)))
	if err != nil || again.Profile.Search.Preferences.Compensation[0].Minimum != nil {
		t.Fatalf("round trip lost unknown minimum: %v", err)
	}
}

func TestPartialProfileHasNoPersonalDefaults(t *testing.T) {
	snapshot, err := Decode(strings.NewReader(`{"revision":0,"profile":{"schemaVersion":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	p := snapshot.Profile
	if p.Identity.Name != "" || p.Search.MaxRequiredExperienceYears != nil || p.Automation.MinFitToNotify != nil || p.Outreach.AutoDraft != nil {
		t.Fatal("partial profiles must not inherit candidate facts or policy defaults")
	}
}

func TestDecodeRejectsAmbiguousAndUnsupportedImports(t *testing.T) {
	for name, input := range map[string]string{
		"null":              `null`,
		"array":             `[]`,
		"missing schema":    `{"profile":{}}`,
		"future schema":     `{"profile":{"schemaVersion":2}}`,
		"unknown field":     `{"profile":{"schemaVersion":1,"apiKey":"no-secrets-here"}}`,
		"nested unknown":    `{"profile":{"schemaVersion":1,"identity":{"nickname":"Alex"}}}`,
		"duplicate":         `{"profile":{"schemaVersion":1,"schemaVersion":2}}`,
		"case duplicate":    `{"profile":{"schemaVersion":1,"SchemaVersion":1}}`,
		"extra document":    `{"profile":{"schemaVersion":1}} {}`,
		"trailing garbage":  `{"profile":{"schemaVersion":1}} no`,
		"negative revision": `{"revision":-1,"profile":{"schemaVersion":1}}`,
		"revision overflow": `{"revision":9223372036854775807,"profile":{"schemaVersion":1}}`,
		"bad timestamp":     `{"updatedAt":"yesterday","profile":{"schemaVersion":1}}`,
		"oversized":         strings.Repeat(" ", MaxImportBytes+1),
		"invalid UTF-8":     "{\"profile\":{\"schemaVersion\":1,\"identity\":{\"name\":\"\xff\"}}}",
		"encoded size":      `{"profile":{"schemaVersion":1,"outreach":{"instructions":"` + strings.Repeat("<", MaxImportBytes/4) + `"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(input)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestValidationRejectsBrokenReferencesAndPolicies(t *testing.T) {
	integer := func(value int) *int { return &value }
	for name, mutate := range map[string]func(*Profile){
		"duplicate id":            func(p *Profile) { p.Background.Skills[0].ID = "api-work" },
		"invalid id":              func(p *Profile) { p.Background.Skills[0].ID = "../skill" },
		"missing evidence":        func(p *Profile) { p.Background.Skills[0].EvidenceIDs = []string{"absent"} },
		"repeated evidence":       func(p *Profile) { p.Background.Skills[0].EvidenceIDs = []string{"api-work", "api-work"} },
		"unknown skill tier":      func(p *Profile) { p.Background.Skills[0].Level = "expert" },
		"blank evidence":          func(p *Profile) { p.Background.Evidence[0].Text = " " },
		"missing source":          func(p *Profile) { p.Background.Evidence[0].Source = "" },
		"unknown evidence status": func(p *Profile) { p.Background.Evidence[0].Status = "probably_true" },
		"bad email":               func(p *Profile) { p.Identity.Email = "Alex <alex@example.com>" },
		"bad public link":         func(p *Profile) { p.Identity.Links[0].URL = "javascript:alert(1)" },
		"reverse dates":           func(p *Profile) { p.Background.Experience[0].EndDate = "2023-12" },
		"bad month":               func(p *Profile) { p.Background.Education[0].StartDate = "2020-13" },
		"missing employer":        func(p *Profile) { p.Background.Experience[0].Company = "" },
		"bad project":             func(p *Profile) { p.Background.Projects[0].Status = "shipped_maybe" },
		"bad project URL":         func(p *Profile) { p.Background.Projects[0].URL = "file:///private" },
		"unknown provider":        func(p *Profile) { p.Search.Sources[0].Provider = "linkedin" },
		"provider casing":         func(p *Profile) { p.Search.Sources[0].Provider = "LEVER" },
		"missing slug":            func(p *Profile) { p.Search.Sources[0].Slug = "" },
		"workday config":          func(p *Profile) { p.Search.Sources[0].Provider = "workday" },
		"unknown work mode":       func(p *Profile) { p.Search.Preferences.WorkModes = []string{"any"} },
		"duplicate work mode":     func(p *Profile) { p.Search.Preferences.WorkModes = []string{"remote", "remote"} },
		"bad country":             func(p *Profile) { p.Search.Preferences.WorkAuthorization[0].Country = "India" },
		"duplicate country": func(p *Profile) {
			p.Search.Preferences.WorkAuthorization = append(p.Search.Preferences.WorkAuthorization, p.Search.Preferences.WorkAuthorization[0])
		},
		"pay order":           func(p *Profile) { v := int64(1); p.Search.Preferences.Compensation[0].Target = &v },
		"pay currency":        func(p *Profile) { p.Search.Preferences.Compensation[0].Currency = "rupees" },
		"pay basis":           func(p *Profile) { p.Search.Preferences.Compensation[0].Basis = "equity" },
		"missing pay amount":  func(p *Profile) { p.Search.Preferences.Compensation[0].Minimum = nil },
		"negative experience": func(p *Profile) { p.Search.MaxRequiredExperienceYears = integer(-1) },
		"negative budget":     func(p *Profile) { p.Automation.DailyBudget = integer(-1) },
		"negative cycle":      func(p *Profile) { p.Automation.MaxPerCycle = integer(-1) },
		"negative size":       func(p *Profile) { p.Outreach.MaxEmployees = integer(-1) },
		"fit range":           func(p *Profile) { p.Automation.MinFitToNotify = integer(101) },
		"unknown fit policy":  func(p *Profile) { p.Automation.UnknownFitPolicy = "guess" },
		"template path":       func(p *Profile) { p.Resume.Template = "../../template" },
		"unsupported pages":   func(p *Profile) { p.Resume.MaxPages = integer(2) },
	} {
		t.Run(name, func(t *testing.T) {
			p := example(t).Profile
			mutate(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
