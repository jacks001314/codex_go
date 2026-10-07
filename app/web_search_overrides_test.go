package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/cli"
	"codex_go/config"
	codexexec "codex_go/exec"
	codextea "codex_go/tui/tea"
)

// TestWebSearchOverridesRespectLaunchOriginsLikeRust covers Rust #49799
// (606b139565) web_search_tests.rs::legacy_search_overrides_respect_launch_origins:
// a canonical config-file setting is not a launch choice, so neither it nor the
// losing legacy flags travel with the request.
func TestWebSearchOverridesRespectLaunchOriginsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("web_search = \"disabled\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	root := &cli.RootOptions{ConfigOverrides: []string{
		"features.multi_agent=true",
		"features.web_search_request=true",
		"features.web_search_cached=true",
		"features.web_search=true",
	}}
	values, err := remoteConfigValues(root, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues: %v", err)
	}
	if got, present := values["web_search"]; present {
		t.Fatalf("client search setting forwarded = %#v, want the destination's own setting", got)
	}
	forwarded, _ := values["features"].(map[string]any)
	if len(forwarded) != 1 || forwarded["multi_agent"] != true {
		t.Fatalf("forwarded features = %#v, want only multi_agent", forwarded)
	}
}

// TestWebSearchOverridesPreserveExplicitLegacyChoicesLikeRust covers Rust #49799
// web_search_tests.rs::web_search_overrides_preserve_explicit_legacy_choices: a
// launch-supplied legacy flag is translated into the canonical `web_search`
// override and then dropped from the forwarded feature table.
func TestWebSearchOverridesPreserveExplicitLegacyChoicesLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	for _, testCase := range []struct {
		name      string
		overrides []string
		want      string
	}{
		{"request flag", []string{"features.web_search_request=true"}, "live"},
		{"cached beats live", []string{"features.web_search_cached=true", "features.web_search_request=true"}, "cached"},
		{"legacy alias", []string{"features.web_search=true"}, "live"},
		{"disabled request falls back to cached", []string{"features.web_search=true", "features.web_search_request=false"}, "cached"},
		{"canonical wins", []string{"web_search=\"disabled\"", "features.web_search_request=true"}, "disabled"},
	} {
		root := &cli.RootOptions{ConfigOverrides: append([]string(nil), testCase.overrides...)}
		values, err := remoteConfigValues(root, cli.SharedOptions{})
		if err != nil {
			t.Fatalf("%s: remoteConfigValues: %v", testCase.name, err)
		}
		if len(values) != 1 {
			t.Fatalf("%s: forwarded values = %#v, want only the canonical web_search", testCase.name, values)
		}
		if got, _ := values["web_search"].(string); got != testCase.want {
			t.Fatalf("%s: web_search = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

// TestWebSearchOverridesHonorSharedFeatureRequirementsLikeRust covers Rust #49799
// web_search_tests.rs::embedded_legacy_search_honors_shared_feature_requirements:
// an embedded session keeps the requirement-merged feature state, while a remote
// server enforces its own requirements on the raw launch choice.
func TestWebSearchOverridesHonorSharedFeatureRequirementsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		requirements string
		overrides    []string
		wantEmbedded string
		wantRemote   string
	}{
		{"requirement disables the request flag", "web_search_request = false", []string{"features.web_search_request=true"}, "", "live"},
		{"requirement pins cached", "web_search_cached = true", []string{"features.web_search_request=true"}, "", "live"},
		{"requirement enables the request flag", "web_search_request = true", []string{"features.web_search_request=false"}, "", "cached"},
		{"requirement unpins cached", "web_search_cached = false", []string{"features.web_search_cached=true", "features.web_search_request=true"}, "live", "cached"},
	} {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("[features]\n"+testCase.requirements+"\n"), 0o600); err != nil {
			t.Fatalf("%s: write requirements: %v", testCase.name, err)
		}
		root := &cli.RootOptions{ConfigOverrides: append([]string(nil), testCase.overrides...)}
		values, err := remoteConfigValues(root, cli.SharedOptions{})
		if err != nil {
			t.Fatalf("%s: remoteConfigValues: %v", testCase.name, err)
		}
		if got, _ := values["web_search"].(string); got != testCase.wantRemote {
			t.Fatalf("%s: remote web_search = %q, want %q (%#v)", testCase.name, got, testCase.wantRemote, values)
		}
		wantEmbedded := []string{}
		if testCase.wantEmbedded != "" {
			wantEmbedded = []string{"web_search=" + testCase.wantEmbedded}
		}
		if got := interactiveLaunchWebSearchOverrides(root); !slices.Equal(got, wantEmbedded) {
			t.Fatalf("%s: embedded overrides = %#v, want %#v", testCase.name, got, wantEmbedded)
		}
	}
}

// TestWebSearchOverridesForwardTheSearchFlagLikeRust covers the Rust CLI's
// session-flags `--search` origin: the flag is a winning launch choice, so it
// travels as the canonical `web_search = "live"` on both paths, and it outranks
// a configured value.
func TestWebSearchOverridesForwardTheSearchFlagLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("web_search = \"disabled\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	root := &cli.RootOptions{}
	root.Shared.Search = true
	values, err := remoteConfigValues(root, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues: %v", err)
	}
	if got, _ := values["web_search"].(string); got != "live" {
		t.Fatalf("remote web_search = %q, want live (%#v)", got, values)
	}
	if got := interactiveLaunchWebSearchOverrides(root); !slices.Equal(got, []string{"web_search=live"}) {
		t.Fatalf("embedded overrides = %#v, want web_search=live", got)
	}
}

// TestWebSearchOverridesLeaveImplicitSettingsToTheDestinationLikeRust pins the
// no-launch case for both paths: nothing is forwarded, so the destination server
// (or the embedded runner) keeps its own default or the saved thread setting.
func TestWebSearchOverridesLeaveImplicitSettingsToTheDestinationLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nweb_search_request = true\nweb_search_cached = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	values, err := remoteConfigValues(&cli.RootOptions{}, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues: %v", err)
	}
	if values != nil {
		t.Fatalf("forwarded values = %#v, want none", values)
	}
	if got := interactiveLaunchWebSearchOverrides(&cli.RootOptions{}); len(got) != 0 {
		t.Fatalf("embedded overrides = %#v, want none", got)
	}
}

// interactiveTurnCaptureRunner records the config overrides the embedded turn
// hands to its runner, so the launch-override wiring is exercised end to end
// instead of through the helper alone.
type interactiveTurnCaptureRunner struct {
	captured chan []string
}

func (r interactiveTurnCaptureRunner) RunContext(_ context.Context, req *codexexec.Request, _ io.Reader, _, _ io.Writer) (*codexexec.Result, error) {
	if r.captured != nil {
		r.captured <- append([]string(nil), req.Root.ConfigOverrides...)
	}
	return nil, nil
}

// TestInteractiveTurnForwardsLaunchSearchOverrideLikeRust drives the real
// embedded turn path (runInteractiveTurn) and asserts the launch search choice
// reaches the runner: Rust #49799 forwards `--search` (session flags) and a
// launch-origin legacy flag as the canonical `web_search` override, while an
// implicit client setting is left to the embedded runner.
func TestInteractiveTurnForwardsLaunchSearchOverrideLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	for _, testCase := range []struct {
		name      string
		config    string
		search    bool
		overrides []string
		want      string
	}{
		{"search flag outranks the config", "web_search = \"disabled\"\n", true, nil, "web_search=live"},
		{"launch legacy flag", "", false, []string{"features.web_search_cached=true"}, "web_search=cached"},
		{"launch request flag", "", false, []string{"features.web_search_request=true"}, "web_search=live"},
		{"implicit config setting", "[features]\nweb_search_request = true\n", false, nil, ""},
	} {
		path := config.ConfigPath(home)
		if testCase.config == "" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatalf("%s: remove config: %v", testCase.name, err)
			}
		} else if err := os.WriteFile(path, []byte(testCase.config), 0o600); err != nil {
			t.Fatalf("%s: write config: %v", testCase.name, err)
		}
		root := &cli.RootOptions{ConfigOverrides: append([]string(nil), testCase.overrides...)}
		root.Shared.Search = testCase.search
		captured := make(chan []string, 1)
		messages := make(chan bubbletea.Msg, 8)
		runInteractiveTurn(context.Background(), root, interactiveTurnCaptureRunner{captured: captured}, nil, codextea.SubmitRequest{Prompt: "hi"}, "thread-1", messages, nil, nil, nil)
		var overrides []string
		select {
		case overrides = <-captured:
		default:
			t.Fatalf("%s: runner did not receive the turn request", testCase.name)
		}
		if testCase.want == "" {
			for _, key := range []string{"web_search=live", "web_search=cached", "web_search=disabled"} {
				if slices.Contains(overrides, key) {
					t.Fatalf("%s: implicit setting forwarded = %#v", testCase.name, overrides)
				}
			}
			continue
		}
		if !slices.Contains(overrides, testCase.want) {
			t.Fatalf("%s: turn overrides = %#v, want %q", testCase.name, overrides, testCase.want)
		}
	}
}
