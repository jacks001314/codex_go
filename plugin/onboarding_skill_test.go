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
