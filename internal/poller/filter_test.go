package poller

import (
	"testing"

	"jobwatch/internal/config"
	"jobwatch/internal/providers"
)

func TestPasses(t *testing.T) {
	filters := config.Filters{
		IncludeKeywords:  []string{"backend", "software engineer", "sde", "platform", "golang", "python"},
		ExcludeKeywords:  []string{"staff", "principal", "director", "manager", "intern", "10+ years", "lead"},
		LocationsInclude: []string{"india", "hyderabad", "bengaluru", "bangalore", "remote"},
	}

	tests := []struct {
		name  string
		title string
		loc   string
		want  bool
	}{
		{"matches backend + bengaluru", "Backend Engineer", "Bengaluru, India", true},
		{"matches golang + remote", "Golang Developer", "Remote - India", true},
		{"excluded by staff", "Staff Backend Engineer", "Bengaluru", false},
		{"excluded by manager", "Engineering Manager, Platform", "Remote", false},
		{"no include keyword match", "Product Designer", "Bengaluru", false},
		{"location not allowed", "Backend Engineer", "London, UK", false},
		{"case insensitive include", "BACKEND ENGINEER", "INDIA", true},
		{"case insensitive exclude", "STAFF Engineer", "India", false},
		{"excluded by lead as whole word", "Team Lead, Backend Engineer", "Remote", false},
		{"not excluded: intern substring inside international", "International Backend Engineer", "Remote", true},
		{"not excluded: lead substring inside leadership", "Leadership Program Backend Engineer", "Remote", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := providers.Job{Title: tt.title, Location: tt.loc}
			got := Passes(job, filters)
			if got != tt.want {
				t.Errorf("Passes(title=%q, loc=%q) = %v, want %v", tt.title, tt.loc, got, tt.want)
			}
		})
	}
}

func TestPassesRejectsCountryLockedRemote(t *testing.T) {
	filters := config.Filters{
		IncludeKeywords:  []string{"backend"},
		LocationsInclude: []string{"india", "bangalore", "remote", "global", "worldwide"},
		LocationsExclude: []string{"us", "united states", "canada", "united kingdom", "poland", "emea"},
	}
	tests := []struct {
		loc  string
		want bool
	}{
		{"Remote, Global", true},
		{"Remote, Bangalore", true},
		{"Worldwide", true},
		{"Bangalore, India", true},
		{"US - Remote", false},
		{"United States (Remote)", false},
		{"Remote, Canada; Remote, United States", false},
		{"Remote, Poland", false},
		{"Remote, EMEA; Bangalore, India", true},
		{"Remote, Australia", true}, // not in this test's exclude list
	}
	for _, tt := range tests {
		job := providers.Job{Title: "Backend Engineer", Location: tt.loc}
		if got := Passes(job, filters); got != tt.want {
			t.Errorf("location %q: got %v, want %v", tt.loc, got, tt.want)
		}
	}
}

func TestPassesEmptyLocationFilterAllowsAll(t *testing.T) {
	filters := config.Filters{
		IncludeKeywords:  []string{"backend"},
		LocationsInclude: []string{},
	}
	job := providers.Job{Title: "Backend Engineer", Location: "Antarctica"}
	if !Passes(job, filters) {
		t.Error("expected job to pass when locations_include is empty")
	}
}

func TestPassesEmptyIncludeAllowsAllTitles(t *testing.T) {
	filters := config.Filters{
		IncludeKeywords: []string{},
		ExcludeKeywords: []string{"manager"},
	}
	job := providers.Job{Title: "Random Title With No Keywords", Location: ""}
	if !Passes(job, filters) {
		t.Error("expected job to pass when include_keywords is empty and no exclude match")
	}
}
