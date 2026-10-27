package auth

import "strings"

// API-key routing hints. Prefixes never validate credentials or grant access
// (Rust login::auth::is_fedramp_api_key, #57178).

// IsFedrampAPIKey reports whether the key carries a FedRAMP issuance marker.
//
// Unmarked keys may predate the marker, so false does not establish that a key
// is commercial. Authentication must still use the complete, unchanged key.
func IsFedrampAPIKey(apiKey string) bool {
	return strings.HasPrefix(apiKey, "sk-proj-fedramp-") || strings.HasPrefix(apiKey, "sk-svcacct-fedramp-")
}
