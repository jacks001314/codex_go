package config

// Typed configuration load failures.
//
// Rust parity: codex-config's `ConfigLoadError` (#46962). Diagnostic surfaces
// (notably `codex doctor`) must be able to report *where* a configuration file
// failed without echoing the offending value, because load errors can carry
// credentials from `config.toml`.

import (
	"errors"
	"fmt"

	toml "github.com/pelletier/go-toml/v2"
)

// ConfigLoadError is a configuration file failure with its source location.
// The wrapped cause keeps the original text for interactive callers; diagnostic
// reports use the typed fields only.
type ConfigLoadError struct {
	Path   string
	Line   int
	Column int

	cause error
}

func (e *ConfigLoadError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return fmt.Sprintf("failed to load configuration from %s: %v", e.Path, e.cause)
	}
	return fmt.Sprintf("failed to load configuration from %s", e.Path)
}

func (e *ConfigLoadError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Location reports the file and 1-based position a configuration load failed
// at, when the underlying failure carried one.
func (e *ConfigLoadError) Location() (string, int, int, bool) {
	if e == nil || e.Path == "" || e.Line <= 0 {
		return "", 0, 0, false
	}
	return e.Path, e.Line, e.Column, true
}

// newConfigLoadError wraps a TOML failure with the file it came from and the
// position the parser reported (Rust ConfigLoadError keeps the same pair).
func newConfigLoadError(path string, cause error) error {
	if cause == nil {
		return nil
	}
	err := &ConfigLoadError{Path: path, cause: cause}
	var decodeErr *toml.DecodeError
	if errors.As(cause, &decodeErr) {
		err.Line, err.Column = decodeErr.Position()
	}
	var missingErr *toml.StrictMissingError
	if errors.As(cause, &missingErr) && len(missingErr.Errors) > 0 {
		err.Line, err.Column = missingErr.Errors[0].Position()
	}
	return err
}

// ConfigLoadErrorLocation extracts the typed source location of a
// configuration load failure, looking through wrappers (including path errors).
// The bool result reports whether such a location is available.
func ConfigLoadErrorLocation(err error) (string, int, int, bool) {
	var loadErr *ConfigLoadError
	if !errors.As(err, &loadErr) {
		return "", 0, 0, false
	}
	return loadErr.Location()
}
