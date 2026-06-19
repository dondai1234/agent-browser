package browser

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateURL checks a navigation URL is safe. By default only http/https are
// allowed; file/javascript/data/about/blob require allowInsecure (opt-in) so
// an agent can't be steered to read local files or execute javascript: URIs.
// Relative URLs (no scheme) are rejected.
func ValidateURL(raw string, allowInsecure bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		return u.String(), nil
	case "file", "javascript", "data", "about", "blob":
		if !allowInsecure {
			return "", fmt.Errorf("scheme %q blocked by default (allow-insecure to enable)", scheme)
		}
		return u.String(), nil
	default:
		return "", fmt.Errorf("scheme %q not allowed", scheme)
	}
}
