package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"jobwatch/internal/config"
)

const MaxImportBytes = 1 << 20

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,99}$`)
var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)
var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// Decode rejects ambiguous or unsupported imports before any database is opened.
func Decode(r io.Reader) (Snapshot, error) {
	var snapshot Snapshot
	data, err := io.ReadAll(io.LimitReader(r, MaxImportBytes+1))
	if err != nil {
		return snapshot, err
	}
	if len(data) > MaxImportBytes {
		return snapshot, fmt.Errorf("profile import exceeds 1 MiB")
	}
	if !utf8.Valid(data) {
		return snapshot, fmt.Errorf("profile import must be UTF-8")
	}
	if err := checkKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return snapshot, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&snapshot); err != nil {
		return snapshot, fmt.Errorf("decoding profile: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return snapshot, fmt.Errorf("profile import must contain one JSON object")
	}
	return snapshot, snapshot.Validate()
}

func (s Snapshot) Validate() error {
	if s.Revision < 0 || s.Revision == math.MaxInt64 {
		return fmt.Errorf("revision must be nonnegative and leave room for an increment")
	}
	if err := s.Profile.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(s)
	if err != nil || len(encoded) > MaxImportBytes {
		return fmt.Errorf("encoded profile snapshot exceeds 1 MiB or cannot be encoded")
	}
	return nil
}

// Reject duplicate keys, including case variants encoding/json would merge.
func checkKeys(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		if delim == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[strings.ToLower(name)] {
				return fmt.Errorf("profile object keys must be unique (case-insensitive)")
			}
			seen[strings.ToLower(name)] = true
		}
		if err := checkKeys(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func (p Profile) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("profile.schemaVersion: supported version is %d", SchemaVersion)
	}
	if p.Identity.Email != "" {
		address, err := mail.ParseAddress(p.Identity.Email)
		if err != nil || address.Address != p.Identity.Email {
			return fmt.Errorf("profile.identity.email: expected an email address without a display name")
		}
	}
	for i, link := range p.Identity.Links {
		if strings.TrimSpace(link.Label) == "" || !webURL(link.URL) {
			return fmt.Errorf("profile.identity.links[%d]: label and http(s) URL required", i)
		}
	}
	seen := map[string]bool{}
	evidence := map[string]bool{}
	addID := func(id, path string) error {
		if !identifier.MatchString(id) || seen[id] {
			return fmt.Errorf("%s.id: expected a unique identifier (letters, digits, hyphens, underscores)", path)
		}
		seen[id] = true
		return nil
	}
	for i, item := range p.Background.Evidence {
		path := fmt.Sprintf("profile.background.evidence[%d]", i)
		if err := addID(item.ID, path); err != nil {
			return err
		}
		if strings.TrimSpace(item.Text) == "" || strings.TrimSpace(item.Source) == "" || !claimStatus(item.Status) {
			return fmt.Errorf("%s: text, source and status (confirmed, needs_review, planned) required", path)
		}
		evidence[item.ID] = true
	}
	checkRefs := func(refs []string, path string) error {
		used := map[string]bool{}
		for _, id := range refs {
			if !evidence[id] || used[id] {
				return fmt.Errorf("%s.evidenceIds: references must be unique and point to existing evidence", path)
			}
			used[id] = true
		}
		return nil
	}
	for i, skill := range p.Background.Skills {
		path := fmt.Sprintf("profile.background.skills[%d]", i)
		if err := addID(skill.ID, path); err != nil {
			return err
		}
		if strings.TrimSpace(skill.Name) == "" || !slices.Contains([]string{"used", "familiar", "adjacent"}, skill.Level) {
			return fmt.Errorf("%s: name and level (used, familiar, adjacent) required", path)
		}
		if err := checkRefs(skill.EvidenceIDs, path); err != nil {
			return err
		}
	}
	for i, job := range p.Background.Experience {
		path := fmt.Sprintf("profile.background.experience[%d]", i)
		if err := addID(job.ID, path); err != nil {
			return err
		}
		if strings.TrimSpace(job.Company) == "" || strings.TrimSpace(job.Title) == "" || !dates(job.StartDate, job.EndDate) {
			return fmt.Errorf("%s: company, title and ordered YYYY-MM dates required", path)
		}
		if err := checkRefs(job.EvidenceIDs, path); err != nil {
			return err
		}
	}
	for i, school := range p.Background.Education {
		path := fmt.Sprintf("profile.background.education[%d]", i)
		if err := addID(school.ID, path); err != nil {
			return err
		}
		if strings.TrimSpace(school.Institution) == "" || strings.TrimSpace(school.Qualification) == "" || !dates(school.StartDate, school.EndDate) {
			return fmt.Errorf("%s: institution, qualification and ordered YYYY-MM dates required", path)
		}
	}
	for i, project := range p.Background.Projects {
		path := fmt.Sprintf("profile.background.projects[%d]", i)
		if err := addID(project.ID, path); err != nil {
			return err
		}
		if strings.TrimSpace(project.Name) == "" || !claimStatus(project.Status) || (project.URL != "" && !webURL(project.URL)) {
			return fmt.Errorf("%s: name, valid status and optional http(s) URL required", path)
		}
		if err := checkRefs(project.EvidenceIDs, path); err != nil {
			return err
		}
	}
	if len(p.Search.Sources) > 0 {
		if err := config.ValidateCompanies(p.Search.Sources); err != nil {
			return fmt.Errorf("profile.search.sources: %w", err)
		}
		for _, source := range p.Search.Sources {
			if source.Provider != strings.ToLower(source.Provider) {
				return fmt.Errorf("profile.search.sources: provider names must be lowercase")
			}
		}
	}
	modes := map[string]bool{}
	for _, mode := range p.Search.Preferences.WorkModes {
		if !slices.Contains([]string{"remote", "hybrid", "onsite"}, mode) || modes[mode] {
			return fmt.Errorf("profile.search.preferences.workModes: expected unique remote, hybrid or onsite entries")
		}
		modes[mode] = true
	}
	countries := map[string]bool{}
	for _, auth := range p.Search.Preferences.WorkAuthorization {
		if !countryCode.MatchString(auth.Country) || countries[auth.Country] {
			return fmt.Errorf("profile.search.preferences.workAuthorization: expected unique uppercase two-letter country codes")
		}
		countries[auth.Country] = true
	}
	for i, pay := range p.Search.Preferences.Compensation {
		if strings.TrimSpace(pay.Market) == "" || !currencyCode.MatchString(pay.Currency) ||
			!slices.Contains([]string{"annual", "monthly", "hourly"}, pay.Period) ||
			!slices.Contains([]string{"fixed", "total"}, pay.Basis) ||
			(pay.Minimum == nil && pay.Target == nil) || (pay.Minimum != nil && *pay.Minimum < 0) ||
			(pay.Target != nil && (*pay.Target < 0 || (pay.Minimum != nil && *pay.Target < *pay.Minimum))) {
			return fmt.Errorf("profile.search.preferences.compensation[%d]: market, currency, period, basis and nonnegative ordered amounts required", i)
		}
	}
	if p.Resume.Template != "" && !identifier.MatchString(p.Resume.Template) {
		return fmt.Errorf("profile.resume.template: expected a template identifier, not a path")
	}
	if p.Resume.MaxPages != nil && *p.Resume.MaxPages != 1 {
		return fmt.Errorf("profile.resume.maxPages: only one-page resumes are currently supported")
	}
	for _, field := range []struct {
		path  string
		value *int
	}{
		{"search.maxRequiredExperienceYears", p.Search.MaxRequiredExperienceYears},
		{"automation.maxPerCycle", p.Automation.MaxPerCycle},
		{"automation.dailyBudget", p.Automation.DailyBudget},
		{"automation.minFitToNotify", p.Automation.MinFitToNotify},
		{"outreach.maxEmployees", p.Outreach.MaxEmployees},
	} {
		if field.value != nil && *field.value < 0 {
			return fmt.Errorf("profile.%s: expected a nonnegative value", field.path)
		}
	}
	if p.Automation.MinFitToNotify != nil && *p.Automation.MinFitToNotify > 100 {
		return fmt.Errorf("profile.automation.minFitToNotify: expected a value between 0 and 100")
	}
	if policy := p.Automation.UnknownFitPolicy; policy != "" && !slices.Contains([]string{"notify", "review", "skip"}, policy) {
		return fmt.Errorf("profile.automation.unknownFitPolicy: expected notify, review or skip")
	}
	return nil
}

func claimStatus(value string) bool {
	return slices.Contains([]string{"confirmed", "needs_review", "planned"}, value)
}

func webURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}

func dates(start, end string) bool {
	_, err := time.Parse("2006-01", start)
	if err != nil {
		return false
	}
	if end == "" {
		return true
	}
	_, err = time.Parse("2006-01", end)
	return err == nil && end >= start
}
