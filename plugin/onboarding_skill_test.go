package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestOnboardingSkillForDetailLikeRust mirrors Rust #46544's selection rules: the
// declared skill is returned only while the plugin and that skill are enabled,
// and an absent or unmatched declaration yields null.
func TestOnboardingSkillForDetailLikeRust(t *testing.T) {
	root := t.TempDir()
	onboarding := filepath.Join(root, "skills", "setup", "SKILL.md")
	other := filepath.Join(root, "skills", "other", "SKILL.md")
	setup := PluginSkill{Name: "demo:setup", Path: &onboarding, Enabled: true}
	disabledSetup := PluginSkill{Name: "demo:setup", Path: &onboarding, Enabled: false}
	base := PluginDetail{
		Summary:             PluginSummary{Enabled: true},
		Skills:              []PluginSkill{setup, {Name: "demo:other", Path: &other, Enabled: true}},
		onboardingSkillPath: onboarding,
	}

	if got := onboardingSkillForDetail(base); got == nil || got.Name != "demo:setup" {
		t.Fatalf("enabled matching skill = %#v", got)
	}

	disabledPlugin := base
	disabledPlugin.Summary.Enabled = false
	if got := onboardingSkillForDetail(disabledPlugin); got != nil {
		t.Fatalf("disabled plugin returned %#v", got)
	}

	disabledSkill := base
	disabledSkill.Skills = []PluginSkill{disabledSetup, base.Skills[1]}
	if got := onboardingSkillForDetail(disabledSkill); got != nil {
		t.Fatalf("disabled skill returned %#v", got)
	}

	missing := base
	missing.onboardingSkillPath = ""
	if got := onboardingSkillForDetail(missing); got != nil {
		t.Fatalf("missing declaration returned %#v", got)
	}

	unmatched := base
	unmatched.onboardingSkillPath = filepath.Join(root, "skills", "missing", "SKILL.md")
	if got := onboardingSkillForDetail(unmatched); got != nil {
		t.Fatalf("unmatched declaration returned %#v", got)
	}
}

// TestResolveOnboardingSkillPathLikeRust mirrors Rust's
// resolve_openai_onboarding_skill: relative declarations are accepted with or
// without the legacy `./` prefix, and a declaration outside the plugin root is
// rejected.
func TestResolveOnboardingSkillPathLikeRust(t *testing.T) {
	root := t.TempDir()
	cases := map[string]string{
		"skills/setup/SKILL.md":   filepath.Join(root, "skills", "setup", "SKILL.md"),
		"./skills/setup/SKILL.md": filepath.Join(root, "skills", "setup", "SKILL.md"),
		"":                        "",
		"../outside/SKILL.md":     "",
	}
	for declared, want := range cases {
		if got := resolveOnboardingSkillPath(root, &pluginManifestFile{OnboardingSkill: declared}); got != want {
			t.Fatalf("resolveOnboardingSkillPath(%q) = %q, want %q", declared, got, want)
		}
	}
	if got := resolveOnboardingSkillPath(root, nil); got != "" {
		t.Fatalf("nil manifest = %q, want empty", got)
	}
}

// TestPluginReadExposesDeclaredOnboardingSkillLikeRust is the end-to-end check:
// a manifest declaring `extensions["com.openai"].onboardingSkill` exposes the
// matching skill on plugin/read, and the value round-trips over JSON.
func TestPluginReadExposesDeclaredOnboardingSkillLikeRust(t *testing.T) {
	root := t.TempDir()
	marketplaceDir := filepath.Join(root, ".agents", "plugins")
	if err := os.MkdirAll(marketplaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(marketplace) error = %v", err)
	}
	marketplace := `{"name":"debug","plugins":[{"name":"agent-tools","source":{"source":"local","path":"./plugins/agent-tools"}}]}`
	if err := os.WriteFile(filepath.Join(marketplaceDir, "marketplace.json"), []byte(marketplace), 0o600); err != nil {
		t.Fatalf("WriteFile(marketplace) error = %v", err)
	}
	pluginRoot := filepath.Join(root, "plugins", "agent-tools")
	for _, name := range []string{"setup", "other"} {
		if err := os.MkdirAll(filepath.Join(pluginRoot, "skills", name), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", name, err)
		}
		body := "---\nname: " + name + "\ndescription: Run " + name + "\n---\n"
		if err := os.WriteFile(filepath.Join(pluginRoot, "skills", name, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	manifest := `{"$schema":"` + AgentPluginSchemaURI + `","name":"agent-tools","extensions":{"com.openai":{"onboardingSkill":"./skills/setup/SKILL.md"}}}`
	if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(plugin) error = %v", err)
	}

	service := NewPluginService()
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: root}); err != nil {
		t.Fatalf("AddMarketplace() error = %v", err)
	}
	if _, err := service.Install(&PluginInstallParams{PluginName: "agent-tools", MarketplaceName: "debug"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	read, err := service.Read(&PluginReadParams{MarketplaceName: "debug", PluginName: "agent-tools"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Plugin.OnboardingSkill == nil {
		t.Fatalf("onboarding skill missing: %#v", read.Plugin.Skills)
	}
	if read.Plugin.OnboardingSkill.Name != "setup" {
		t.Fatalf("onboarding skill = %#v, want setup", read.Plugin.OnboardingSkill)
	}
	encoded, err := json.Marshal(read.Plugin)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	onboarding, ok := payload["onboardingSkill"].(map[string]any)
	if !ok || onboarding["name"] != "setup" {
		t.Fatalf("serialized onboardingSkill = %#v", payload["onboardingSkill"])
	}
}

// TestOnboardingSkillForDetailMatchesWindowsPathIdentityLikeRust mirrors Rust
// #51482 (c9870d0157): the onboarding-skill lookup compares PathUri identity
// rather than raw text, so equivalent Windows spellings (case and separators)
// match. Rust tests: plugin_read_selects_local_onboarding_skill and
// load_plugins_resolves_disabled_skill_names_against_loaded_plugin_skills.
func TestOnboardingSkillForDetailMatchesWindowsPathIdentityLikeRust(t *testing.T) {
	declared := `C:\skills\setup\SKILL.md`
	skillPath := `C:/SKILLS/SETUP/SKILL.md`
	other := `C:/skills/other/SKILL.md`
	detail := PluginDetail{
		Summary:             PluginSummary{Enabled: true},
		Skills:              []PluginSkill{{Name: "demo:setup", Path: &skillPath, Enabled: true}, {Name: "demo:other", Path: &other, Enabled: true}},
		onboardingSkillPath: declared,
	}
	if got := onboardingSkillForDetail(detail); got == nil || got.Name != "demo:setup" {
		t.Fatalf("windows identity match = %#v", got)
	}

	unmatched := detail
	unmatched.onboardingSkillPath = `C:\skills\missing\SKILL.md`
	if got := onboardingSkillForDetail(unmatched); got != nil {
		t.Fatalf("unmatched windows path returned %#v", got)
	}
}

// TestPluginSkillPathMatchesLikeRust mirrors Rust #51482's PathUri identity
// rules: Windows spellings fold case and separators, and a genuinely different
// path never matches. Spellings without a URI representation (relative text)
// fall back to the cleaned-text compare so pre-#51482 behavior is preserved.
func TestPluginSkillPathMatchesLikeRust(t *testing.T) {
	upper := `C:/SKILLS/SETUP/skill.md`
	lower := `C:\\skills\\setup\\SKILL.md`
	upperKey, okUpper := pluginSkillPathIdentity(upper)
	lowerKey, okLower := pluginSkillPathIdentity(lower)
	if !okUpper || !okLower {
		t.Fatalf("identity keys missing: %v %v", okUpper, okLower)
	}
	if upperKey != lowerKey {
		t.Fatalf("case/separator folding failed: %q vs %q", upperKey, lowerKey)
	}
	if pluginSkillPathMatches(filepath.Clean(lower), lowerKey, okLower, upper) != true {
		t.Fatal("equivalent Windows spellings must match")
	}
	if pluginSkillPathMatches(filepath.Clean(lower), lowerKey, okLower, `C:/skills/other/SKILL.md`) {
		t.Fatal("a different path must not match")
	}

	// Relative text has no URI representation, so the fallback compares cleaned
	// text (the pre-#51482 behavior for non-absolute inputs).
	relative := filepath.Join("skills", "setup", "SKILL.md")
	relativeKey, relativeOK := pluginSkillPathIdentity(relative)
	if pluginSkillPathMatches(filepath.Clean(relative), relativeKey, relativeOK, "./"+filepath.ToSlash(relative)) != true {
		t.Fatal("relative spellings must still match through the text fallback")
	}
}
