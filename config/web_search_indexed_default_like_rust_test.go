package config

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/codexapi"
)

// TestWebSearchModePreferenceIndexedDefaultLikeRust mirrors Rust #62894
// resolve_web_search_mode: the explicit setting wins, the deprecated flags
// follow, and `features.web_search_indexed_default` upgrades an implicit cached
// default only when the managed requirements allow indexed search.
func TestWebSearchModePreferenceIndexedDefaultLikeRust(t *testing.T) {
	indexedDefault := map[string]bool{"web_search_indexed_default": true}
	for _, test := range []struct {
		name     string
		values   map[string]any
		features map[string]bool
		allowed  []WebSearchMode
		want     codexapi.WebSearchMode
	}{
		{name: "default cached", want: codexapi.WebSearchModeCached},
		{name: "opts into indexed", features: indexedDefault, want: codexapi.WebSearchModeIndexed},
		{
			name:     "requirements admit indexed",
			features: indexedDefault,
			allowed:  []WebSearchMode{WebSearchMode("cached"), WebSearchMode("indexed")},
			want:     codexapi.WebSearchModeIndexed,
		},
		{
			name:     "requirements reject indexed",
			features: indexedDefault,
			allowed:  []WebSearchMode{WebSearchMode("cached")},
			want:     codexapi.WebSearchModeCached,
		},
		{
			name:     "requirement default live stays live",
			features: indexedDefault,
			allowed:  []WebSearchMode{WebSearchMode("live")},
			want:     codexapi.WebSearchModeLive,
		},
		{
			name:     "explicit cached wins over indexed default",
			values:   map[string]any{"web_search": "cached"},
			features: indexedDefault,
			want:     codexapi.WebSearchModeCached,
		},
		{
			name:     "legacy cached wins over indexed default",
			features: map[string]bool{"web_search_cached": true, "web_search_indexed_default": true},
			want:     codexapi.WebSearchModeCached,
		},
		{
			name:     "legacy request stays live",
			features: map[string]bool{"web_search_request": true, "web_search_indexed_default": true},
			want:     codexapi.WebSearchModeLive,
		},
		{
			name:     "explicit indexed",
			values:   map[string]any{"web_search": "indexed"},
			features: map[string]bool{"web_search_request": true},
			want:     codexapi.WebSearchModeIndexed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := WebSearchModePreference(test.values, test.features, test.allowed); got != test.want {
				t.Fatalf("WebSearchModePreference() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestWebSearchModeForTurnLikeRust mirrors Rust
// resolve_web_search_mode_for_turn: full access promotes to live, an implicit
// indexed default does not block that promotion, an explicit indexed selection
// keeps its precedence, and a provider without external web access falls back to
// cached.
func TestWebSearchModeForTurnLikeRust(t *testing.T) {
	allowedDefault := []WebSearchMode{WebSearchMode("cached"), WebSearchMode("indexed")}
	for _, test := range []struct {
		name              string
		preferred         codexapi.WebSearchMode
		explicit          bool
		fullAccess        bool
		allowed           []WebSearchMode
		externalWebAccess bool
		want              codexapi.WebSearchMode
	}{
		{name: "restricted cached", preferred: codexapi.WebSearchModeCached, externalWebAccess: true, want: codexapi.WebSearchModeCached},
		{name: "restricted keeps indexed", preferred: codexapi.WebSearchModeIndexed, externalWebAccess: true, want: codexapi.WebSearchModeIndexed},
		{name: "full access promotes cached", preferred: codexapi.WebSearchModeCached, fullAccess: true, externalWebAccess: true, want: codexapi.WebSearchModeLive},
		{name: "full access keeps live", preferred: codexapi.WebSearchModeLive, fullAccess: true, externalWebAccess: true, want: codexapi.WebSearchModeLive},
		{name: "full access promotes implicit indexed", preferred: codexapi.WebSearchModeIndexed, fullAccess: true, externalWebAccess: true, want: codexapi.WebSearchModeLive},
		{name: "full access keeps explicit indexed", preferred: codexapi.WebSearchModeIndexed, explicit: true, fullAccess: true, externalWebAccess: true, want: codexapi.WebSearchModeIndexed},
		{
			name:              "full access keeps implicit indexed without live",
			preferred:         codexapi.WebSearchModeIndexed,
			fullAccess:        true,
			allowed:           allowedDefault,
			externalWebAccess: true,
			want:              codexapi.WebSearchModeIndexed,
		},
		{name: "full access keeps disabled", preferred: codexapi.WebSearchModeDisabled, fullAccess: true, externalWebAccess: true, want: codexapi.WebSearchModeDisabled},
		{name: "live without external access falls back to cached", preferred: codexapi.WebSearchModeLive, externalWebAccess: false, want: codexapi.WebSearchModeCached},
		{name: "indexed without external access falls back to cached", preferred: codexapi.WebSearchModeIndexed, externalWebAccess: false, want: codexapi.WebSearchModeCached},
		{name: "full access without external access falls back to cached", preferred: codexapi.WebSearchModeCached, fullAccess: true, externalWebAccess: false, want: codexapi.WebSearchModeCached},
		{name: "all modes but disabled rejected", preferred: codexapi.WebSearchModeCached, fullAccess: true, allowed: []WebSearchMode{}, externalWebAccess: true, want: codexapi.WebSearchModeDisabled},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveWebSearchModeForTurn(test.preferred, test.explicit, test.fullAccess, test.allowed, test.externalWebAccess)
			if got != test.want {
				t.Fatalf("ResolveWebSearchModeForTurn() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestWebSearchModeExplicitnessThroughLoaderLikeRust pins Rust #62894's
// `web_search_mode_was_explicit = cfg.web_search == Some(constrained value)`:
// a raw value the managed requirements replaced counts as implicit.
func TestWebSearchModeExplicitnessThroughLoaderLikeRust(t *testing.T) {
	writeHome := func(t *testing.T, configTOML, requirementsTOML string) *Config {
		t.Helper()
		home := t.TempDir()
		if configTOML != "" {
			if err := os.WriteFile(ConfigPath(home), []byte(configTOML), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
		}
		if requirementsTOML != "" {
			if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirementsTOML), 0o600); err != nil {
				t.Fatalf("write requirements: %v", err)
			}
		}
		cfg, err := LoadWithOptions(home, nil)
		if err != nil {
			t.Fatalf("LoadWithOptions() error = %v", err)
		}
		return cfg
	}

	implicit := writeHome(t, "", "")
	if implicit.WebSearchModeWasExplicit() {
		t.Fatal("an unset web_search must be implicit")
	}
	if got := implicit.WebSearchModePreference(); got != codexapi.WebSearchModeCached {
		t.Fatalf("implicit preference = %q, want cached", got)
	}

	explicit := writeHome(t, "web_search = \"indexed\"\n", "")
	if !explicit.WebSearchModeWasExplicit() {
		t.Fatal("a configured web_search must be explicit")
	}
	if got := explicit.WebSearchModePreference(); got != codexapi.WebSearchModeIndexed {
		t.Fatalf("explicit preference = %q, want indexed", got)
	}

	kept := writeHome(t, "web_search = \"indexed\"\n", "allowed_web_search_modes = [\"cached\", \"indexed\"]\n")
	if !kept.WebSearchModeWasExplicit() {
		t.Fatal("an allowed configured web_search must stay explicit")
	}

	replaced := writeHome(t, "web_search = \"indexed\"\n", "allowed_web_search_modes = [\"cached\"]\n")
	if replaced.WebSearchModeWasExplicit() {
		t.Fatal("a disallowed configured web_search must count as implicit")
	}
	if got := replaced.WebSearchModePreference(); got != codexapi.WebSearchModeCached {
		t.Fatalf("constrained preference = %q, want cached", got)
	}
}
