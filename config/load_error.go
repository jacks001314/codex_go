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
	// text is the document the parser saw, so the reported message can use the
	// wording and span the Rust build prints for the same syntax failure.
	text string
}

// Error renders the failure the way Rust's ConfigLoadError Display does: the
// file position followed by the parser's message. A failure that carries no TOML
// position keeps the generic load wording.
func (e *ConfigLoadError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		if line, column, message, ok := TOMLDiagnosticPosition(e.text, e.cause); ok {
			return fmt.Sprintf("%s:%d:%d: %s", e.Path, line, column, message)
		}
		return fmt.Sprintf("failed to load configuration from %s: %v", e.Path, e.cause)
	}
	return fmt.Sprintf("failed to load configuration from %s", e.Path)
}

// TOMLSyntaxSnippet reports the excerpt Rust prints under
// `Error loading config.toml:` when the load failed on a TOML syntax error.
func (e *ConfigLoadError) TOMLSyntaxSnippet() (string, bool) {
	if e == nil || e.cause == nil {
		return "", false
	}
	return TOMLConfigSnippet(e.Path, e.text, e.cause)
}

// TOMLSyntaxSnippetOf reports the excerpt Rust prints under
// `Error loading config.toml:` when a load failed on a TOML syntax error.
func TOMLSyntaxSnippetOf(err error) (string, bool) {
	var loadErr *ConfigLoadError
	if !errors.As(err, &loadErr) {
		return "", false
	}
	return loadErr.TOMLSyntaxSnippet()
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
func newConfigLoadError(path, text string, cause error) error {
	if cause == nil {
		return nil
	}
	err := &ConfigLoadError{Path: path, cause: cause, text: text}
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
