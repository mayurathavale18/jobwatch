package poller

import (
	"regexp"
	"strings"

	"jobwatch/internal/config"
	"jobwatch/internal/providers"
)

// Passes reports whether a job matches the configured filters:
// title must contain at least one include keyword (if any are configured),
// must not contain any exclude keyword, and location must match at least
// one entry in locations_include (if any are configured). Title keyword
// matching is case-insensitive and whole-word/whole-phrase (a keyword like
// "intern" must not match inside an unrelated longer word like
// "international" -- location matching stays plain substring since city/
// region names don't have that failure mode).
func Passes(job providers.Job, f config.Filters) bool {
	title := strings.ToLower(job.Title)

	if len(f.ExcludeKeywords) > 0 && matchesAny(title, f.ExcludeKeywords) {
		return false
	}

	if len(f.IncludeKeywords) > 0 && !matchesAny(title, f.IncludeKeywords) {
		return false
	}

	if len(f.LocationsInclude) > 0 {
		location := strings.ToLower(job.Location)
		if !containsAny(location, f.LocationsInclude) {
			return false
		}
	}

	return true
}

// matchesAny reports whether haystack contains any needle as a whole word
// or phrase, \b-bounded so a short needle doesn't match inside an
// unrelated longer word. Needles are literal text -- regex metacharacters
// (e.g. the "+" in "10+ years") are escaped, not treated as patterns.
func matchesAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		pattern := `\b` + regexp.QuoteMeta(strings.ToLower(n)) + `\b`
		if regexp.MustCompile(pattern).MatchString(haystack) {
			return true
		}
	}
	return false
}

// containsAny reports whether haystack contains any needle as a plain
// substring. Used for location matching only.
func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(haystack, strings.ToLower(n)) {
			return true
		}
	}
	return false
}
