package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/plugin"
)

func mustParseMarketplaceRequirements(t *testing.T, body string) *ConfigRequirements {
	t.Helper()
	requirements, err := ParseRequirementsTOML([]byte(body))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || requirements.Marketplaces == nil {
		t.Fatalf("marketplace requirements = %#v", requirements)
	}
	return requirements
}

func marketplaceRule(t *testing.T, requirements *ConfigRequirements, name string) plugin.MarketplaceAllowedSource {
	t.Helper()
	rule, ok := requirements.Marketplaces.AllowedSources[name]
	if !ok {
		t.Fatalf("allowed source %q missing from %#v", name, requirements.Marketplaces.AllowedSources)
	}
	return rule
}

func requiredString(t *testing.T, value *string) string {
	t.Helper()
	if value == nil {
		t.Fatal("string pointer is nil")
	}
	return *value
}

func TestMarketplaceRequirementsUseDefaultTOMLMergeLikeRust(t *testing.T) {
	low := mustParseMarketplaceRequirements(t, `
[marketplaces]
restrict_to_allowed_sources = true

[marketplaces.allowed_sources.shared]
source = "git"
url = "https://github.com/example/old.git"
ref = "main"

[marketplaces.allowed_sources.other]
source = "git"
url = "https://github.com/example/other.git"
`)
	high := mustParseMarketplaceRequirements(t, `
[marketplaces.allowed_sources.shared]
ref = "release"
`)
	merged := mergeConfigRequirements(low, high)
	if merged == nil || merged.Marketplaces == nil || merged.Marketplaces.RestrictToAllowedSources == nil || !*merged.Marketplaces.RestrictToAllowedSources {
		t.Fatalf("merged requirements = %#v", merged)
	}
	shared := marketplaceRule(t, merged, "shared")
	if shared.Source == nil || *shared.Source != plugin.MarketplaceAllowedSourceGit {
		t.Fatalf("shared source = %#v", shared.Source)
	}
	if got := requiredString(t, shared.URL); got != "https://github.com/example/old.git" {
		t.Fatalf("shared url = %q", got)
	}
	if got := requiredString(t, shared.RefName); got != "release" {
		t.Fatalf("shared ref = %q", got)
	}
	if got := requiredString(t, marketplaceRule(t, merged, "other").URL); got != "https://github.com/example/other.git" {
		t.Fatalf("other url = %q", got)
	}
	if low.Marketplaces.AllowedSources["shared"].RefName == nil || *low.Marketplaces.AllowedSources["shared"].RefName != "main" {
		t.Fatal("merge mutated the lower requirements layer")
	}
}

func TestMarketplaceRequirementsSourceSwitchUsesDefaultTOMLMergeLikeRust(t *testing.T) {
	low := mustParseMarketplaceRequirements(t, `
[marketplaces.allowed_sources.company]
source = "git"
url = "https://github.com/example/plugins.git"
ref = "main"
`)
	high := mustParseMarketplaceRequirements(t, `
[marketplaces.allowed_sources.company]
source = "host_pattern"
host_pattern = '^github\.example\.com$'
`)
	merged := mergeConfigRequirements(low, high)
	company := marketplaceRule(t, merged, "company")
	if company.Source == nil || *company.Source != plugin.MarketplaceAllowedSourceHostPattern {
		t.Fatalf("source = %#v", company.Source)
	}
	if got := requiredString(t, company.URL); got != "https://github.com/example/plugins.git" {
		t.Fatalf("retained url = %q", got)
	}
	if got := requiredString(t, company.RefName); got != "main" {
		t.Fatalf("retained ref = %q", got)
	}
	if got := requiredString(t, company.HostPattern); got != `^github\.example\.com$` {
		t.Fatalf("host pattern = %q", got)
	}
}

func TestMarketplaceRequirementsRejectUnknownFieldsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		match string
	}{
		{
			name: "allowed source field",
			body: `
[marketplaces]
restrict_to_allowed_sources = true
[marketplaces.allowed_sources.invalid]
source = "git"
url = "https://github.com/example/plugins.git"
reff = "main"
`,
			match: "unknown field `reff`",
		},
		{
			name: "marketplace table field",
			body: `
[marketplaces]
restrict_to_allowed_sources = true
allow_everything = true
`,
			match: "unknown field `allow_everything`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRequirementsTOML([]byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("ParseRequirementsTOML() error = %v, want %q", err, tc.match)
			}
		})
	}
}

func TestMarketplaceRequirementsLocalPathIsNotResolvedDuringMergeLikeRust(t *testing.T) {
	requirements := mustParseMarketplaceRequirements(t, `
[marketplaces]
restrict_to_allowed_sources = true
[marketplaces.allowed_sources.local]
source = "local"
path = "../plugins"
`)
	local := marketplaceRule(t, requirements, "local")
	if got := requiredString(t, local.Path); got != "../plugins" {
		t.Fatalf("local path = %q, want unresolved ../plugins", got)
	}
	merged := mergeConfigRequirements(nil, requirements)
	if got := requiredString(t, marketplaceRule(t, merged, "local").Path); got != "../plugins" {
		t.Fatalf("merged local path = %q", got)
	}
}

func TestConfigLoadCarriesMarketplaceRequirementsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(`
[marketplaces]
restrict_to_allowed_sources = true
[marketplaces.allowed_sources.company]
source = "git"
url = "https://github.com/example/plugins.git"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`
[marketplaces.company]
source_type = "git"
source = "https://github.com/example/plugins.git"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(home)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg == nil || cfg.Requirements == nil || cfg.Requirements.Marketplaces == nil {
		t.Fatalf("loaded requirements = %#v", cfg)
	}
	if cfg.Requirements.Marketplaces.RestrictToAllowedSources == nil || !*cfg.Requirements.Marketplaces.RestrictToAllowedSources {
		t.Fatalf("restriction = %#v", cfg.Requirements.Marketplaces.RestrictToAllowedSources)
	}
	if got := requiredString(t, marketplaceRule(t, cfg.Requirements, "company").URL); got != "https://github.com/example/plugins.git" {
		t.Fatalf("company url = %q", got)
	}
	marketplaces, ok := cfg.Values["marketplaces"].(map[string]any)
	if !ok || marketplaces["company"] == nil {
		t.Fatalf("effective marketplaces = %#v", cfg.Values["marketplaces"])
	}
}

func TestMarketplaceRequirementsAreNotReportedAsIgnored(t *testing.T) {
	warning := IgnoredConfigWarning(nil, map[string]any{"marketplaces": map[string]any{
		"restrict_to_allowed_sources": true,
	}})
	if strings.Contains(warning, "marketplaces") {
		t.Fatalf("ignored config warning = %q", warning)
	}
}
