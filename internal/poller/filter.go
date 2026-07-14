package poller

import (
	"strings"

	"jobwatch/internal/config"
	"jobwatch/internal/providers"
)

// Passes reports whether a job matches the configured filters:
// title must contain at least one include keyword (if any are configured),
// must not contain any exclude keyword, and location must match at least
// one entry in locations_include (if any are configured). All matching is
// case-insensitive substring matching.
func Passes(job providers.Job, f config.Filters) bool {
	title := strings.ToLower(job.Title)

	if len(f.ExcludeKeywords) > 0 && containsAny(title, f.ExcludeKeywords) {
		return false
	}

	if len(f.IncludeKeywords) > 0 && !containsAny(title, f.IncludeKeywords) {
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
