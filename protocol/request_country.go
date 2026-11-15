package protocol

import (
	"encoding/json"
	"errors"
	"strings"
)

// ErrInvalidRequestCountry reports a country code that is not exactly two
// ASCII letters.
var ErrInvalidRequestCountry = errors.New("request country must contain exactly two ASCII letters")

type requestCountryKind uint8

const (
	requestCountryOmitted requestCountryKind = iota
	requestCountryUnknown
	requestCountryKnown
)

// RequestCountry is a trusted host-supplied country for Responses requests.
// The zero value omits the field so the server falls back to request
// provenance. The explicit unknown country serializes as JSON null, which
// prevents that fallback (Rust RequestCountry, #61535).
type RequestCountry struct {
	kind requestCountryKind
	code string
}

// UnknownRequestCountry is the explicit unknown country. It serializes as
// JSON null.
var UnknownRequestCountry = RequestCountry{kind: requestCountryUnknown}

// ParseRequestCountry normalizes a two-letter ASCII country code. The special
// code XX means explicitly unknown.
func ParseRequestCountry(code string) (RequestCountry, error) {
	code = strings.TrimSpace(code)
	if len(code) != 2 {
		return RequestCountry{}, ErrInvalidRequestCountry
	}
	for i := 0; i < len(code); i++ {
		character := code[i]
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') {
			return RequestCountry{}, ErrInvalidRequestCountry
		}
	}
	code = strings.ToUpper(code)
	if code == "XX" {
		return UnknownRequestCountry, nil
	}
	return RequestCountry{kind: requestCountryKnown, code: code}, nil
}

// IsOmitted reports whether the country should be left off the request.
func (c RequestCountry) IsOmitted() bool {
	return c.kind == requestCountryOmitted
}

// KnownCountry returns the normalized country code, if one was supplied.
func (c RequestCountry) KnownCountry() (string, bool) {
	if c.kind != requestCountryKnown {
		return "", false
	}
	return c.code, true
}

// MarshalJSON emits null for an explicitly unknown country and the uppercase
// code for a known country.
func (c RequestCountry) MarshalJSON() ([]byte, error) {
	code, ok := c.KnownCountry()
	if !ok {
		return []byte("null"), nil
	}
	return json.Marshal(code)
}

// UnmarshalJSON accepts a country code or null. Null becomes the explicit
// unknown country, matching the distinction between an absent field and a
// host-supplied unknown value.
func (c *RequestCountry) UnmarshalJSON(data []byte) error {
	if c == nil {
		return ErrInvalidRequestCountry
	}
	if strings.TrimSpace(string(data)) == "null" {
		*c = UnknownRequestCountry
		return nil
	}
	var code string
	if err := json.Unmarshal(data, &code); err != nil {
		return ErrInvalidRequestCountry
	}
	parsed, err := ParseRequestCountry(code)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}
