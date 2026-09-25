package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/config"
)

// Rust parity: codex-rs/cli/tests/doctor_path_safety.rs
// doctor_reports_only_safe_config_error_metadata (#46962): a configuration load
// failure reports typed metadata only — the file/line/column of a parse failure,
// an I/O error kind, or a generic message — and never the offending values.
func TestConfigCheckReportsOnlySafeConfigErrorMetadataLikeRust(t *testing.T) {
	const credential = "doctor-test-credential"
	for _, testCase := range []struct {
		name        string
		configBody  string
		wantDetails []string
	}{
		{
			name:       "parse error reports its location",
			configBody: "custom_header = \"" + credential + "\" trailing\n",
			wantDetails: []string{
				"error: invalid configuration",
				"file: ",
				"line: 1",
				"column: ",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(config.ConfigPath(home), []byte(testCase.configBody), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			check := configCheck(home, &Options{Root: cli.RootOptions{}})
			if check.Status != CheckStatusFail {
				t.Fatalf("check status = %q, want fail", check.Status)
			}
			joined := strings.Join(check.Details, "\n")
			if strings.Contains(joined, credential) {
				t.Fatalf("config check leaked a configuration value:\n%s", joined)
			}
			if !containsDetail(check, "config.toml: "+config.ConfigPath(home)) {
				t.Fatalf("config check lost the config path detail: %#v", check.Details)
			}
			for _, want := range testCase.wantDetails {
				if !strings.Contains(joined, want) {
					t.Fatalf("config check details missing %q:\n%s", want, joined)
				}
			}
			if !strings.Contains(joined, "file: "+config.ConfigPath(home)) {
				t.Fatalf("config check did not report the failing file:\n%s", joined)
			}
			// The JSON report keeps the typed details and no raw values.
			data, err := json.Marshal(check)
			if err != nil {
				t.Fatalf("marshal check: %v", err)
			}
			if strings.Contains(string(data), credential) {
				t.Fatalf("JSON report leaked a configuration value: %s", data)
			}
		})
	}
}

// TestConfigCheckReportsMissingCredentialWithoutValuesLikeRust pins the
// "entity not found" arm: a load failure caused by a missing file reports the
// I/O kind instead of the raw path error text.
func TestConfigCheckReportsMissingCredentialWithoutValuesLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	// A directory where the config file is expected makes os.ReadFile fail with
	// a non-not-exist I/O error, which must not be echoed verbatim.
	if err := os.Mkdir(config.ConfigPath(home), 0o700); err != nil {
		t.Fatalf("mkdir config path: %v", err)
	}
	check := configCheck(home, &Options{Root: cli.RootOptions{}})
	if check.Status != CheckStatusFail {
		t.Fatalf("check status = %q, want fail", check.Status)
	}
	joined := strings.Join(check.Details, "\n")
	if strings.Contains(joined, config.ConfigPath(home)+": ") {
		t.Fatalf("config check echoed the raw load error:\n%s", joined)
	}
	if !strings.Contains(joined, "error: ") {
		t.Fatalf("config check lost its error detail:\n%s", joined)
	}
}

// TestConfigLoadErrorLocationLikeRust pins the typed location extraction,
// including through an io wrapper.
func TestConfigLoadErrorLocationLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	loadErr := &config.ConfigLoadError{Path: path, Line: 3, Column: 7}
	gotPath, line, column, ok := config.ConfigLoadErrorLocation(loadErr)
	if !ok || gotPath != path || line != 3 || column != 7 {
		t.Fatalf("location = %q %d %d %v", gotPath, line, column, ok)
	}
	wrapped := &wrapError{inner: loadErr}
	if _, _, _, ok := config.ConfigLoadErrorLocation(wrapped); !ok {
		t.Fatal("location lookup did not look through the wrapper")
	}
	// A location-less load failure (for example a validation error) reports no
	// position, matching Rust's fallback to the I/O kind.
	if _, _, _, ok := config.ConfigLoadErrorLocation(&config.ConfigLoadError{Path: path}); ok {
		t.Fatal("a load failure without a position reported a location")
	}
	if _, _, _, ok := config.ConfigLoadErrorLocation(os.ErrNotExist); ok {
		t.Fatal("a plain I/O error must not report a config location")
	}
}

type wrapError struct{ inner error }

func (e *wrapError) Error() string { return "wrapped: " + e.inner.Error() }
func (e *wrapError) Unwrap() error { return e.inner }
