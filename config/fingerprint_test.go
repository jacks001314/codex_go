// Protects fingerprint bytes when nested configuration keys are reordered.
//
// Ports codex-rs/config/src/fingerprint_tests.rs, added by upstream cf12c86dc5
// "Simplify configuration fingerprint canonicalization" (#49295), and pins the same
// canonical fingerprint on the Go production path.
package config

import (
	"errors"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestFingerprintPreservesNestedKeyOrderIndependenceAndExistingHash mirrors the Rust
// regression test: reordered keys in nested tables and in tables inside arrays must
// produce the same fingerprint, and that fingerprint is the pre-existing one asserted
// by fingerprint_tests.rs.
func TestFingerprintPreservesNestedKeyOrderIndependenceAndExistingHash(t *testing.T) {
	inputs := []string{
		"rows = [{ z = 2, a = 1 }, { z = 4, a = 3 }]\n[nested]\nz = \"text\"\na = true\n",
		"rows = [{ a = 1, z = 2 }, { a = 3, z = 4 }]\n[nested]\na = true\nz = \"text\"\n",
	}
	const want = "sha256:a5e4c8fbdbc75d185b0f2b5291bfb2de10e8c523d6c89248876fd78a97c83176"
	for _, input := range inputs {
		var value map[string]any
		if err := toml.Unmarshal([]byte(input), &value); err != nil {
			t.Fatalf("parse %q: %v", input, err)
		}
		if got := VersionForTOML(value); got != want {
			t.Fatalf("version_for_toml(%q) = %s, want %s", input, got, want)
		}
	}
}

// TestFingerprintCanonicalJSONMatchesRust pins the canonical form itself: object keys
// sorted recursively (tables inside arrays included), array order preserved, and no
// HTML escaping -- serde_json leaves `<`, `>` and `&` verbatim.
func TestFingerprintCanonicalJSONMatchesRust(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{
			body: "rows = [{ z = 2, a = 1 }, { z = 4, a = 3 }]\n[nested]\nz = \"text\"\na = true\n",
			want: `{"nested":{"a":true,"z":"text"},"rows":[{"a":1,"z":2},{"a":3,"z":4}]}`,
		},
		{
			body: "note = \"a<b>&c\"\n[nested]\nz = \"text\"\na = true\n",
			want: `{"nested":{"a":true,"z":"text"},"note":"a<b>&c"}`,
		},
	}
	for _, test := range cases {
		var value map[string]any
		if err := toml.Unmarshal([]byte(test.body), &value); err != nil {
			t.Fatalf("parse %q: %v", test.body, err)
		}
		if got := string(canonicalConfigJSON(value)); got != test.want {
			t.Fatalf("canonical JSON = %s, want %s", got, test.want)
		}
	}
}

// TestConfigServiceVersionsUseCanonicalFingerprintLikeRust proves the fingerprint is
// wired into the config service: config/read reports the canonical fingerprint of the
// user config as the user layer version, and config/write checks that same fingerprint
// before applying edits.
func TestConfigServiceVersionsUseCanonicalFingerprintLikeRust(t *testing.T) {
	// Two textual orders of the same values (top-level key order swapped, and the
	// nested table reordered). The canonical form is
	// `{"approval_policy":"on-request","features":{"web_search":true},"model":"gpt-5"}`.
	const orderA = "model = \"gpt-5\"\napproval_policy = \"on-request\"\n\n[features]\nweb_search = true\n"
	const orderB = "approval_policy = \"on-request\"\nmodel = \"gpt-5\"\n\n[features]\nweb_search = true\n"
	const wantVersion = "sha256:7784dd9b748f58808f60f335f2fd8509eceae3b1021b8fe26e7f1b89cc47d98d"

	for _, body := range []string{orderA, orderB} {
		home := t.TempDir()
		writeConfig(t, home, body)
		service := NewConfigService(home)
		read, err := service.Read(&ConfigReadParams{IncludeLayers: true})
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		var userLayer *Layer
		for i := range read.Layers {
			if read.Layers[i].Name.Type == LayerSourceUser {
				userLayer = &read.Layers[i]
			}
		}
		if userLayer == nil {
			t.Fatalf("layers = %+v, want a user layer", read.Layers)
		}
		if userLayer.Version != wantVersion {
			t.Fatalf("user layer version = %s, want %s", userLayer.Version, wantVersion)
		}
	}

	home := t.TempDir()
	writeConfig(t, home, orderA)
	service := NewConfigService(home)
	expected := wantVersion
	response, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:         "model",
		Value:           "gpt-5-mini",
		ExpectedVersion: &expected,
	})
	if err != nil {
		t.Fatalf("WriteValue(canonical expectedVersion) error = %v", err)
	}
	// sha256 of `{"approval_policy":"on-request","features":{"web_search":true},"model":"gpt-5-mini"}`.
	const wantWrittenVersion = "sha256:de5e8dd8fc69c321ed9c60e3d935d35ba1ae49be42f45cbe59c31e3ce5829506"
	if response.Version != wantWrittenVersion {
		t.Fatalf("write version = %s, want %s", response.Version, wantWrittenVersion)
	}

	stale := "sha256:stale"
	_, err = service.WriteValue(&ConfigValueWriteParams{
		KeyPath:         "model",
		Value:           "gpt-5",
		ExpectedVersion: &stale,
	})
	if !errors.Is(err, ErrInvalidConfigRequest) || configWriteErrorCode(err) != ConfigWriteVersionConflict {
		t.Fatalf("WriteValue(stale version) error = %v, code = %s, want %s", err, configWriteErrorCode(err), ConfigWriteVersionConflict)
	}
}
