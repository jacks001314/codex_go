package execserver

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"
)

// RegistrationBearerMaxBytes mirrors Rust's `take(16 * 1024)` bound on the
// stdin registration bearer.
const RegistrationBearerMaxBytes = 16 * 1024

var (
	// ErrRegistrationBearerNotTerminated is returned when stdin does not supply
	// one newline-terminated bearer within the size bound.
	ErrRegistrationBearerNotTerminated = errors.New("expected a newline-terminated registration bearer of at most 16 KiB")
	// ErrInvalidRegistrationBearer is returned for an empty or non-printable
	// bearer. The rejected value is never echoed.
	ErrInvalidRegistrationBearer = errors.New("invalid registration bearer")
)

// RegistrationBearerFromReader reads one newline-terminated registration
// bearer from stdin and returns the single Authorization header remote
// registration should present (Rust #59254 `registration_auth_from_stdin`).
// On Linux the process becomes non-dumpable before the credential is read.
func RegistrationBearerFromReader(reader io.Reader) (http.Header, error) {
	if reader == nil {
		return nil, ErrRegistrationBearerNotTerminated
	}
	if err := hardenRegistrationBearerRead(); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(io.LimitReader(reader, RegistrationBearerMaxBytes)).ReadString('\n')
	if err != nil && line == "" {
		return nil, ErrRegistrationBearerNotTerminated
	}
	if !strings.HasSuffix(line, "\n") {
		return nil, ErrRegistrationBearerNotTerminated
	}
	token := strings.TrimSuffix(line, "\n")
	token = strings.TrimSuffix(token, "\r")
	if token == "" || !isASCIIGraphicToken(token) {
		return nil, ErrInvalidRegistrationBearer
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	return headers, nil
}
