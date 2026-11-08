package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execTrustFixture is one `codex exec` trust scenario: a codex home holding the
// user config plus a checkout whose project config is what the trust decision
// gates.
type execTrustFixture struct {
	codexHome string
	repo      string
}

func newExecTrustFixture(t *testing.T, userConfig string) execTrustFixture {
	t.Helper()
	codexHome := t.TempDir()
	if userConfig != "" {
		if err := os.WriteFile(ConfigPath(codexHome), []byte(userConfig), 0o644); err != nil {
			t.Fatalf("WriteFile(user config) error = %v", err)
		}
	}
	repo := initTrustRepo(t, t.TempDir())
	if err := os.MkdirAll(filepath.Join(repo, ".gcode"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.gcode) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gcode", "config.toml"), []byte("model = \"project-model\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(project config) error = %v", err)
	}
	return execTrustFixture{codexHome: codexHome, repo: repo}
}

func (f execTrustFixture) options() ExecProjectTrustOptions {
	return ExecProjectTrustOptions{CodexHome: f.codexHome, CWD: f.repo}
}

func (f execTrustFixture) userConfig(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(ConfigPath(f.codexHome))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("ReadFile(user config) error = %v", err)
	}
	return string(data)
}

// Case 1: a trusted project runs with its project configuration and no warning.
func TestExecProjectTrustTrustedProjectHasNoWarning(t *testing.T) {
	repo := initTrustRepo(t, t.TempDir())
	fixture := newExecTrustFixture(t, "")
	trusted := "[projects." + tomlBasicString(canonicalProjectPath(repo)) + "]\ntrust_level = \"trusted\"\n"
	fixture.repo = repo
	if err := os.WriteFile(ConfigPath(fixture.codexHome), []byte(trusted), 0o644); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	service := NewConfigService(fixture.codexHome)
	if got := service.ExecProjectTrustWarningForCWD(repo); got != "" {
		t.Fatalf("warning = %q, want none for a trusted project", got)
	}
	if got := service.ProjectConfigWarningForCWD(repo); got != "" {
		t.Fatalf("project config warning = %q, want none", got)
	}
	userValues, err := loadConfigFile(ConfigPath(fixture.codexHome))
	if err != nil {
		t.Fatalf("loadConfigFile() error = %v", err)
	}
	if !ProjectConfigEnabled(userValues, repo) {
		t.Fatal("ProjectConfigEnabled() = false for a trusted project")
	}
	// Running without --trust must not touch the user config.
	before := fixture.userConfig(t)
	result, err := ResolveExecProjectTrust(fixture.options())
	if err != nil {
		t.Fatalf("ResolveExecProjectTrust() error = %v", err)
	}
	if result.Granted || result.ConfigPath != "" {
		t.Fatalf("result = %#v, want no work without --trust", result)
	}
	if after := fixture.userConfig(t); after != before {
		t.Fatalf("user config changed without --trust: %q -> %q", before, after)
	}
}

// Case 2: an untrusted project keeps its project config disabled, and the
// disabled reason names the folder and the key the decision applies to.
func TestExecProjectTrustUntrustedProjectDisablesConfigAndNamesTheFolders(t *testing.T) {
	fixture := newExecTrustFixture(t, "")
	service := NewConfigService(fixture.codexHome)
	warning := service.ExecProjectTrustWarningForCWD(fixture.repo)
	if warning != ExecProjectTrustWarning {
		t.Fatalf("warning = %q, want the exec trust warning", warning)
	}
	layers, err := service.projectLayersForTrustWarning(fixture.repo)
	if err != nil {
		t.Fatalf("projectLayersForTrustWarning() error = %v", err)
	}
	disabled := ProjectConfigDisabledFolders(layers)
	if len(disabled) != 1 {
		t.Fatalf("disabled folders = %#v, want exactly the project folder", disabled)
	}
	if want := filepath.Join(fixture.repo, ".gcode"); !strings.EqualFold(disabled[0].Folder, want) {
		t.Fatalf("disabled folder = %q, want %q", disabled[0].Folder, want)
	}
	if !strings.Contains(disabled[0].Reason, canonicalProjectPath(fixture.repo)) {
		t.Fatalf("reason = %q, want it to locate %q", disabled[0].Reason, canonicalProjectPath(fixture.repo))
	}
}

// Case 3: `--trust` saves trust_level = "trusted" in the user config.
func TestExecProjectTrustWritesTrustedDecisionToUserConfig(t *testing.T) {
	fixture := newExecTrustFixture(t, "model = \"user-model\"\n")
	options := fixture.options()
	options.Trust = true
	result, err := ResolveExecProjectTrust(options)
	if err != nil {
		t.Fatalf("ResolveExecProjectTrust() error = %v", err)
	}
	if !result.Granted {
		t.Fatalf("result = %#v, want a grant", result)
	}
	wantKey := canonicalProjectPath(fixture.repo)
	if result.Decision.TrustKey != wantKey || result.ConfigPath != ConfigPath(fixture.codexHome) {
		t.Fatalf("result = %#v, want key %q in %q", result, wantKey, ConfigPath(fixture.codexHome))
	}
	text := fixture.userConfig(t)
	if !strings.Contains(text, "[projects."+tomlBasicString(wantKey)+"]") {
		t.Fatalf("user config = %q, want the project table for %q", text, wantKey)
	}
	if !strings.Contains(text, "trust_level = \"trusted\"") {
		t.Fatalf("user config = %q, want trust_level = trusted", text)
	}
	if !strings.Contains(text, "model = \"user-model\"") {
		t.Fatalf("user config = %q, want unrelated settings preserved", text)
	}
	// The grant must satisfy the very check that gates project config.
	userValues, err := loadConfigFile(ConfigPath(fixture.codexHome))
	if err != nil {
		t.Fatalf("loadConfigFile() error = %v", err)
	}
	if !ProjectConfigEnabled(userValues, fixture.repo) {
		t.Fatal("ProjectConfigEnabled() = false after the grant")
	}
	if got := NewConfigService(fixture.codexHome).ExecProjectTrustWarningForCWD(fixture.repo); got != "" {
		t.Fatalf("warning = %q, want none after the grant", got)
	}
}

// Case 4: --trust with --ignore-user-config is refused before any write.
func TestExecProjectTrustRejectsIgnoreUserConfigWithoutWriting(t *testing.T) {
	fixture := newExecTrustFixture(t, "model = \"user-model\"\n")
	before := fixture.userConfig(t)
	options := fixture.options()
	options.Trust = true
	options.IgnoreUserConfig = true
	_, err := ResolveExecProjectTrust(options)
	if err == nil {
		t.Fatal("ResolveExecProjectTrust() succeeded, want the conflict diagnostic")
	}
	if got, want := err.Error(), "--trust cannot be combined with --ignore-user-config: saved project trust decisions must be checked"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if after := fixture.userConfig(t); after != before {
		t.Fatalf("user config changed on a refused run: %q -> %q", before, after)
	}
}

// Case 5: --trust refuses an explicitly untrusted project and does not
// overwrite the saved decision.
func TestExecProjectTrustRefusesExplicitlyUntrustedProject(t *testing.T) {
	fixture := newExecTrustFixture(t, "")
	key := canonicalProjectPath(fixture.repo)
	untrusted := "[projects." + tomlBasicString(key) + "]\ntrust_level = \"untrusted\"\n"
	if err := os.WriteFile(ConfigPath(fixture.codexHome), []byte(untrusted), 0o644); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	options := fixture.options()
	options.Trust = true
	_, err := ResolveExecProjectTrust(options)
	if err == nil {
		t.Fatal("ResolveExecProjectTrust() succeeded, want the untrusted diagnostic")
	}
	if want := key + " is marked as untrusted. Update its trust setting first."; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	if after := fixture.userConfig(t); after != untrusted {
		t.Fatalf("user config = %q, want it unchanged", after)
	}
}

// Case 6: the interactive path is unchanged -- the gate is inert without
// --trust, and the trust reader keeps answering exactly as before.
func TestExecProjectTrustLeavesInteractivePathUnchanged(t *testing.T) {
	fixture := newExecTrustFixture(t, "")
	key := canonicalProjectPath(fixture.repo)
	untrusted := "[projects." + tomlBasicString(key) + "]\ntrust_level = \"untrusted\"\n"
	if err := os.WriteFile(ConfigPath(fixture.codexHome), []byte(untrusted), 0o644); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	service := NewConfigService(fixture.codexHome)
	warningBefore := service.ExecProjectTrustWarningForCWD(fixture.repo)
	userValues, err := loadConfigFile(ConfigPath(fixture.codexHome))
	if err != nil {
		t.Fatalf("loadConfigFile() error = %v", err)
	}
	enabledBefore := ProjectConfigEnabled(userValues, fixture.repo)
	decisionBefore, okBefore := ProjectTrustLevelForTarget(userValues, fixture.repo)
	if decisionBefore != ProjectTrustLevelUntrusted || !okBefore {
		t.Fatalf("trust before = %q, %v; want the untrusted decision", decisionBefore, okBefore)
	}

	// A non-interactive run without --trust must not refuse, and must not write.
	result, err := ResolveExecProjectTrust(fixture.options())
	if err != nil {
		t.Fatalf("ResolveExecProjectTrust() error = %v; a run without --trust must not refuse", err)
	}
	if result.Granted || result.Decision.Saved() {
		t.Fatalf("result = %#v, want no decision work without --trust", result)
	}
	if after := fixture.userConfig(t); after != untrusted {
		t.Fatalf("user config = %q, want it unchanged", after)
	}
	userValuesAfter, err := loadConfigFile(ConfigPath(fixture.codexHome))
	if err != nil {
		t.Fatalf("loadConfigFile() error = %v", err)
	}
	if got := ProjectConfigEnabled(userValuesAfter, fixture.repo); got != enabledBefore {
		t.Fatalf("ProjectConfigEnabled() = %v, want %v", got, enabledBefore)
	}
	if got, ok := ProjectTrustLevelForTarget(userValuesAfter, fixture.repo); got != decisionBefore || ok != okBefore {
		t.Fatalf("trust after = %q, %v; want %q, %v", got, ok, decisionBefore, okBefore)
	}
	if got := service.ExecProjectTrustWarningForCWD(fixture.repo); got != warningBefore {
		t.Fatalf("warning = %q, want %q", got, warningBefore)
	}
}

// --ephemeral suppresses session files, not the trust grant: Rust writes the
// decision to the user config, so it must survive an ephemeral run. This case is
// kept separate from the default path on purpose -- it locks one rule.
func TestExecProjectTrustPersistsUnderEphemeral(t *testing.T) {
	fixture := newExecTrustFixture(t, "")
	options := fixture.options()
	options.Trust = true
	options.Ephemeral = true
	result, err := ResolveExecProjectTrust(options)
	if err != nil {
		t.Fatalf("ResolveExecProjectTrust() error = %v", err)
	}
	if !result.Granted {
		t.Fatalf("result = %#v, want a persisted grant under --ephemeral", result)
	}
	wantKey := canonicalProjectPath(fixture.repo)
	if !strings.Contains(fixture.userConfig(t), "[projects."+tomlBasicString(wantKey)+"]") {
		t.Fatalf("user config = %q, want the grant to persist under --ephemeral", fixture.userConfig(t))
	}
	// A second ephemeral run must find the decision and stay idempotent.
	second, err := ResolveExecProjectTrust(options)
	if err != nil {
		t.Fatalf("second ResolveExecProjectTrust() error = %v", err)
	}
	if !second.Decision.Trusted() || second.Granted {
		t.Fatalf("second result = %#v, want a trusted decision with no extra write", second)
	}
}

// A `-c` override must not be able to grant itself trust: Rust strips every
// `projects` entry from the CLI overrides before checking the saved decision.
func TestExecProjectTrustIgnoresProjectsOverrides(t *testing.T) {
	fixture := newExecTrustFixture(t, "")
	options := fixture.options()
	options.Trust = true
	options.RawOverrides = []string{
		"projects." + `"` + canonicalProjectPath(fixture.repo) + `"` + ".trust_level=untrusted",
	}
	result, err := ResolveExecProjectTrust(options)
	if err != nil {
		t.Fatalf("ResolveExecProjectTrust() error = %v", err)
	}
	if result.Decision.Saved() || !result.Granted {
		t.Fatalf("result = %#v, want the override ignored and a grant written", result)
	}
}

// An untrusted project never refuses a run by itself: upstream #60041 only
// disables the project layers and lets the run proceed with the warning. Only
// an explicit `--trust` on an explicitly untrusted project bails. This case
// exists to keep that distinction from drifting into a self-invented refusal.
func TestExecProjectTrustDoesNotRefuseUntrustedProjectWithoutFlag(t *testing.T) {
	fixture := newExecTrustFixture(t, "")
	key := canonicalProjectPath(fixture.repo)
	untrusted := "[projects." + tomlBasicString(key) + "]\ntrust_level = \"untrusted\"\n"
	if err := os.WriteFile(ConfigPath(fixture.codexHome), []byte(untrusted), 0o644); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	for _, ephemeral := range []bool{false, true} {
		options := fixture.options()
		options.Ephemeral = ephemeral
		result, err := ResolveExecProjectTrust(options)
		if err != nil {
			t.Fatalf("ResolveExecProjectTrust(ephemeral=%v) error = %v; an untrusted project must not refuse", ephemeral, err)
		}
		if result.Granted || result.Decision.Saved() || result.ConfigPath != "" {
			t.Fatalf("result(ephemeral=%v) = %#v, want no work without --trust", ephemeral, result)
		}
	}
	if after := fixture.userConfig(t); after != untrusted {
		t.Fatalf("user config = %q, want it unchanged", after)
	}
	// The trust decision still gates the project layer, and the reason names
	// the key -- that is the whole observable effect of being untrusted.
	if got := NewConfigService(fixture.codexHome).ExecProjectTrustWarningForCWD(fixture.repo); got != ExecProjectTrustWarning {
		t.Fatalf("warning = %q, want the exec trust warning", got)
	}
}
