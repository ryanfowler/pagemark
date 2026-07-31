// Package urlutil contains URL validation shared by extraction and fetching.
package urlutil

import (
	"net/url"
	"strings"
)

// IsHierarchicalHTTP reports whether u is a hierarchical HTTP or HTTPS URL
// with a non-empty hostname.
func IsHierarchicalHTTP(u *url.URL) bool {
	if u == nil || u.Opaque != "" || u.Hostname() == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}
