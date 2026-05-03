package feature

import "net/url"

// sanitizeURL returns only the hostname from a URL string.
// For malformed or empty inputs, returns the input unchanged.
func sanitizeURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return rawURL
	}

	return parsed.Hostname()
}
