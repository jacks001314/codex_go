package app

import (
	"os"
	"slices"
	"testing"

	"codex_go/cli"
	"codex_go/config"
)

// TestWebSearchOverridesForwardIndexedDefaultLikeRust pins Rust #62894's TUI
// forwarding: a launch-origin indexed default travels as a feature, and launch
// overrides that disable the legacy search flags keep the default implicit so a
// full-access turn can still promote it to live.
func TestWebSearchOverridesForwardIndexedDefaultLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	root := &cli.RootOptions{ConfigOverrides: []string{
		"features.web_search_indexed_default=true",
		"features.web_search_cached=false",
	}}
	values, err := remoteConfigValues(root, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues: %v", err)
	}
	if _, present := values["web_search"]; present {
		t.Fatalf("client pinned web_search = %#v, want the implicit default", values["web_search"])
	}
	features, _ := values["features"].(map[string]any)
	if features["web_search_indexed_default"] != true {
		t.Fatalf("forwarded indexed default = %#v", features["web_search_indexed_default"])
	}
	if features["web_search_cached"] != false || features["web_search_request"] != false {
		t.Fatalf("forwarded legacy flags = %#v, want both disabled", features)
	}

	// Without the indexed default the legacy cached fallback is still pinned.
	legacyRoot := &cli.RootOptions{ConfigOverrides: []string{"features.web_search_cached=false"}}
	values, err = remoteConfigValues(legacyRoot, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues(legacy): %v", err)
	}
	if got, _ := values["web_search"].(string); got != "cached" {
		t.Fatalf("legacy fallback web_search = %q, want cached", got)
	}
}

// TestWebSearchOverridesKeepImplicitIndexedDefaultEmbeddedLikeRust covers the
// embedded half: the requirement-merged feature state decides, so the TUI does
// not forward a canonical web_search value that would defeat the promotion.
func TestWebSearchOverridesKeepImplicitIndexedDefaultEmbeddedLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nweb_search_indexed_default = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	root := &cli.RootOptions{ConfigOverrides: []string{"features.web_search_cached=false"}}
	if got := interactiveLaunchWebSearchOverrides(root); len(got) != 0 {
		t.Fatalf("embedded overrides = %#v, want the implicit default", got)
	}

	// A non-launch configuration value alone never travels with the request.
	values, err := remoteConfigValues(&cli.RootOptions{}, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues: %v", err)
	}
	if values != nil {
		t.Fatalf("forwarded values = %#v, want none", values)
	}

	if err := os.Remove(config.ConfigPath(home)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove config: %v", err)
	}
	root = &cli.RootOptions{ConfigOverrides: []string{"features.web_search_cached=false"}}
	if got := interactiveLaunchWebSearchOverrides(root); !slices.Equal(got, []string{"web_search=cached"}) {
		t.Fatalf("embedded legacy fallback = %#v, want web_search=cached", got)
	}
}
