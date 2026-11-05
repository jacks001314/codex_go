package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProjectAgentOptInFixture(t *testing.T, userConfig, projectConfig, requirements string) (string, string) {
	t.Helper()
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(project, ".gcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	user := userConfig + trustedProjectConfig(project)
	if err := os.WriteFile(ConfigPath(home), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectConfigPath(project), []byte(projectConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if requirements != "" {
		if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, project
}

// TestProjectAgentOptInPrecedenceLikeRust ports #59476/#59496: a project's
// [agents].enabled=true replaces only a strictly lower-priority legacy collab
// opt-out; same-layer or higher-priority opt-outs and managed requirements stay
// authoritative.
func TestProjectAgentOptInPrecedenceLikeRust(t *testing.T) {
	tests := []struct {
		name         string
		userConfig   string
		project      string
		requirements string
		overrides    []string
		want         bool
	}{
		{name: "lower-priority user opt-out", userConfig: "[features]\nmulti_agent = false\n", project: "[agents]\nenabled = true\n", want: true},
		{name: "legacy collab alias opt-out", userConfig: "[features]\ncollab = false\n", project: "[agents]\nenabled = true\n", want: true},
		{name: "project feature opt-in", userConfig: "[features]\nmulti_agent = false\n", project: "[features]\nmulti_agent = true\n", want: true},
		{name: "user opt-out remains without project opt-in", userConfig: "[features]\nmulti_agent = false\n", project: "[features]\ncollab = true\n[agents]\nenabled = true\n", want: true},
		{name: "same-layer opt-out", userConfig: "[features]\nmulti_agent = false\n", project: "[agents]\nenabled = true\n[features]\nmulti_agent = false\n", want: false},
		{name: "same-layer collab opt-out", userConfig: "[features]\nmulti_agent = false\n", project: "[agents]\nenabled = true\n[features]\ncollab = false\n", want: false},
		{name: "session opt-out", userConfig: "[features]\nmulti_agent = false\n", project: "[agents]\nenabled = true\n", overrides: []string{"features.multi_agent=false"}, want: false},
		{name: "session collab alias opt-out", userConfig: "[features]\nmulti_agent = false\n", project: "[agents]\nenabled = true\n", overrides: []string{"features.collab=false"}, want: false},
		{name: "enabled session flag does not stop scan", userConfig: "[features]\nmulti_agent = false\n", project: "[agents]\nenabled = true\n", overrides: []string{"features.collab=true"}, want: true},
		{name: "managed requirement opt-out wins", userConfig: "[features]\nmulti_agent = false\n", project: "[agents]\nenabled = true\n", requirements: "[features]\nmulti_agent = false\n", want: false},
		{name: "managed collab requirement alias wins", userConfig: "[features]\nmulti_agent = false\n", project: "[agents]\nenabled = true\n", requirements: "[features]\ncollab = false\n", want: false},
		{name: "project agents disabled does not opt in", userConfig: "[features]\nmulti_agent = false\n", project: "[agents]\nenabled = false\n", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home, project := writeProjectAgentOptInFixture(t, tc.userConfig, tc.project, tc.requirements)
			cfg, err := LoadEffectiveWithOptions(home, &EffectiveOptions{
				CWD:          project,
				RawOverrides: tc.overrides,
			})
			if err != nil {
				t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
			}
			if got := cfg.FeatureSettings()["multi_agent"]; got != tc.want {
				t.Fatalf("multi_agent = %v, want %v; values=%#v", got, tc.want, cfg.Values)
			}
		})
	}
}

func TestLegacyMultiAgentDisabledSourcesSurviveSessionOverrideLikeRust(t *testing.T) {
	tests := []struct {
		name         string
		userConfig   string
		project      string
		requirements string
		wantEnabled  bool
		wantSources  string
	}{
		{
			name:        "user opt-out survives session enable",
			userConfig:  "[features]\nmulti_agent = false\n",
			project:     "[features]\ncollab = false\n",
			wantEnabled: true,
			wantSources: "user",
		},
		{
			name:         "managed requirement recorded",
			userConfig:   "[features]\nmulti_agent = false\n",
			project:      "[agents]\nenabled = true\n",
			requirements: "[features]\nmulti_agent = false\n",
			wantEnabled:  false,
			wantSources:  "user,managed_requirement",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home, project := writeProjectAgentOptInFixture(t, tc.userConfig, tc.project, tc.requirements)
			cfg, err := LoadEffectiveWithOptions(home, &EffectiveOptions{
				CWD:          project,
				RawOverrides: []string{"features.multi_agent=true"},
			})
			if err != nil {
				t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
			}
			if got := cfg.FeatureSettings()["multi_agent"]; got != tc.wantEnabled {
				t.Fatalf("multi_agent = %v, want %v", got, tc.wantEnabled)
			}
			if got := strings.Join(cfg.LegacyMultiAgentDisabledSources(), ","); got != tc.wantSources {
				t.Fatalf("LegacyMultiAgentDisabledSources() = %q, want %q", got, tc.wantSources)
			}
		})
	}
}

func TestConfigReadPreservesProjectAgentOptInLikeRust(t *testing.T) {
	home, project := writeProjectAgentOptInFixture(t, "[features]\nmulti_agent = false\n", "[agents]\nenabled = true\n", "")
	read, err := NewConfigService(home).Read(&ConfigReadParams{CWD: &project})
	if err != nil {
		t.Fatalf("ConfigService.Read() error = %v", err)
	}
	cfg := NewConfigFromRead(read)
	if got := cfg.FeatureSettings()["multi_agent"]; !got {
		t.Fatalf("multi_agent = false after ConfigService.Read, want the project opt-in; values=%#v", cfg.Values)
	}
	cfg.ApplyOverrides([]Override{{Path: "features.collab", Value: false}})
	if got := cfg.FeatureSettings()["multi_agent"]; got {
		t.Fatalf("multi_agent = true after session opt-out, want false")
	}
}
