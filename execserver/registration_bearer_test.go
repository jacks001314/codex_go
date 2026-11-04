package execserver

import (
	"errors"
	"strings"
	"testing"
)

// Rust #59254 reads exactly one newline-terminated, printable-ASCII bearer of
// at most 16 KiB and turns it into one Authorization header.
func TestRegistrationBearerFromReaderLikeRust(t *testing.T) {
	read := func(input string) (string, error) {
		headers, err := RegistrationBearerFromReader(strings.NewReader(input))
		if err != nil {
			return "", err
		}
		if len(headers) != 1 {
			return "", errors.New("expected one header")
		}
		return headers.Get("Authorization"), nil
	}

	if got, err := read("registration-placeholder\n"); err != nil || got != "Bearer registration-placeholder" {
		t.Fatalf("plain bearer = %q, %v", got, err)
	}
	if got, err := read("registration-placeholder\r\n"); err != nil || got != "Bearer registration-placeholder" {
		t.Fatalf("CRLF bearer = %q, %v", got, err)
	}
	atLimit := strings.Repeat("a", RegistrationBearerMaxBytes-1) + "\n"
	if got, err := read(atLimit); err != nil || got != "Bearer "+strings.Repeat("a", RegistrationBearerMaxBytes-1) {
		t.Fatalf("limit bearer error = %v", err)
	}
	overLimit := strings.Repeat("a", RegistrationBearerMaxBytes) + "\n"
	if _, err := read(overLimit); !errors.Is(err, ErrRegistrationBearerNotTerminated) {
		t.Fatalf("over-limit error = %v", err)
	}
	if _, err := read("missing-newline"); !errors.Is(err, ErrRegistrationBearerNotTerminated) {
		t.Fatalf("missing newline error = %v", err)
	}
	for _, invalid := range []string{"\n", "bad token\n", "tab\tvalue\n", "\x7f\n"} {
		if _, err := read(invalid); !errors.Is(err, ErrInvalidRegistrationBearer) {
			t.Fatalf("invalid bearer %q error = %v", invalid, err)
		}
	}
	if _, err := RegistrationBearerFromReader(nil); !errors.Is(err, ErrRegistrationBearerNotTerminated) {
		t.Fatalf("nil reader error = %v", err)
	}
}
