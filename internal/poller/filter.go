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

	if len(f.LocationsInclude) > 0 && !locationPasses(strings.ToLower(job.Location), f) {
		return false
	}

	return true
}

// locationPasses splits a multi-location string ("Remote, Canada; Remote,
// United States") into segments and passes if any single segment matches
// an include term without matching an exclude term -- so "Remote, Global"
// passes but a remote role locked to a foreign country doesn't.
// ponytail: a segment naming both India and a foreign region ("Remote - US
// or India") is rejected; split on " or " too if that ever costs a real job.
func locationPasses(location string, f config.Filters) bool {
	for _, seg := range strings.FieldsFunc(location, func(r rune) bool { return r == ';' || r == '|' }) {
		if containsAny(seg, f.LocationsInclude) && !matchesAny(seg, f.LocationsExclude) {
			return true
		}
	}
	return false
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
