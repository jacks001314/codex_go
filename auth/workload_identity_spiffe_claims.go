package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const workloadErrAmbiguousSPIFFE workloadIdentityError = "the SPIFFE Workload API returned multiple JWT-SVIDs; configure an explicit SPIFFE ID"

// selectSPIFFEAssertion checks selection claims only. The token endpoint owns
// JOSE signature and lifetime verification, including JWTs without a kid.
func selectSPIFFEAssertion(svids []string, audience, expectedID string) (string, error) {
	if len(svids) > 1 {
		return "", workloadErrAmbiguousSPIFFE
	}
	if len(svids) != 1 {
		return "", workloadErrInvalidAssertion
	}
	raw := svids[0]
	if len(raw) > workloadMaxAssertionBytes {
		return "", workloadErrAssertionTooLarge
	}
	assertion := strings.TrimSpace(raw)
	if assertion == "" || strings.ContainsRune(assertion, 0) {
		return "", workloadErrInvalidAssertion
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		return "", workloadErrInvalidAssertion
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", workloadErrInvalidAssertion
	}
	var claims struct {
		Sub string          `json:"sub"`
		Aud json.RawMessage `json:"aud"`
	}
	if json.Unmarshal(payload, &claims) != nil || !validSPIFFESelectionID(claims.Sub) {
		return "", workloadErrInvalidAssertion
	}
	var single string
	var multiple []string
	matched := false
	if json.Unmarshal(claims.Aud, &single) == nil {
		matched = single == audience
	} else if json.Unmarshal(claims.Aud, &multiple) == nil {
		for _, value := range multiple {
			if value == audience {
				matched = true
			}
		}
	}
	subject, _ := canonicalSPIFFESelectionID(claims.Sub)
	expected, validExpected := canonicalSPIFFESelectionID(expectedID)
	if !matched || expectedID != "" && (!validExpected || expected != subject) {
		return "", workloadErrInvalidAssertion
	}
	return assertion, nil
}

func validSPIFFESelectionID(value string) bool {
	_, ok := canonicalSPIFFESelectionID(value)
	return ok
}

// Matches spiffe 0.18.0 SpiffeId::new: ASCII scheme/domain are case-insensitive,
// path case is retained, and empty/dot path segments are invalid.
func canonicalSPIFFESelectionID(value string) (string, bool) {
	scheme, rest, ok := strings.Cut(value, "://")
	if !ok || !strings.EqualFold(scheme, "spiffe") {
		return "", false
	}
	domain, path, hasPath := strings.Cut(rest, "/")
	if len(domain) == 0 || len(domain) > 255 {
		return "", false
	}
	validChars := func(text string) bool {
		for i := 0; i < len(text); i++ {
			b := text[i]
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '.' || b == '_') {
				return false
			}
		}
		return true
	}
	if !validChars(domain) {
		return "", false
	}
	canonical := "spiffe://" + strings.ToLower(domain)
	if hasPath {
		for _, segment := range strings.Split(path, "/") {
			if segment == "" || segment == "." || segment == ".." || !validChars(segment) {
				return "", false
			}
		}
		canonical += "/" + path
	}
	return canonical, true
}
