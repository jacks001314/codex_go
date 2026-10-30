package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func policyStringPointer(value string) *string {
	return &value
}

func policySource(t *testing.T, source string, refName *string) *ParsedMarketplaceSource {
	t.Helper()
	parsed, err := ParseMarketplaceSource(source, refName)
	if err != nil {
		t.Fatalf("ParseMarketplaceSource(%q) error = %v", source, err)
	}
	return parsed
}

func restrictedPolicy(allowed ...MarketplaceAllowedSource) *MarketplacePolicy {
	restricted := true
	sources := make(map[string]MarketplaceAllowedSource, len(allowed))
	for index, source := range allowed {
		sources[string(rune('a'+index))] = source
	}
	return NewMarketplacePolicy(&MarketplaceRequirements{
		RestrictToAllowedSources: &restricted,
		AllowedSources:           sources,
		RequirementSource:        "managed requirements",
	})
}

func TestMarketplacePolicyExactGitRuleMatchesURLAndRefLikeRust(t *testing.T) {
	policy := NewMarketplacePolicy(&MarketplaceRequirements{
		RestrictToAllowedSources: boolPointer(true),
		AllowedSources: map[string]MarketplaceAllowedSource{
			"company": {
				Source:  sourceKindPointer(MarketplaceAllowedSourceGit),
				URL:     policyStringPointer("https://github.com/example/plugins"),
				RefName: policyStringPointer("main"),
			},
		},
	})

	if err := policy.ValidateSource(policySource(t, "https://github.com/example/plugins.git", policyStringPointer("main"))); err != nil {
		t.Fatalf("allowlisted source rejected: %v", err)
	}
	for _, denied := range []*ParsedMarketplaceSource{
		policySource(t, "https://github.com/example/plugins.git", policyStringPointer("release")),
		policySource(t, "https://github.com/other/plugins.git", policyStringPointer("main")),
	} {
		if err := policy.ValidateSource(denied); err == nil {
			t.Fatalf("source %s unexpectedly allowed", denied.Display())
		}
	}

	normalized, err := policy.ValidateGitSource("example/plugins", policyStringPointer("main"))
	if err != nil {
		t.Fatalf("ValidateGitSource() error = %v", err)
	}
	if normalized == nil || normalized.URL != "https://github.com/example/plugins.git" || stringPtrValue(normalized.RefName) != "main" {
		t.Fatalf("normalized source = %#v", normalized)
	}
}

func TestMarketplacePolicyGitRuleWithoutRefAllowsAnyRefLikeRust(t *testing.T) {
	policy := restrictedPolicy(MarketplaceAllowedSource{
		Source: sourceKindPointer(MarketplaceAllowedSourceGit),
		URL:    policyStringPointer("https://github.com/example/plugins"),
	})
	if err := policy.ValidateSource(policySource(t, "https://github.com/example/plugins.git", policyStringPointer("release"))); err != nil {
		t.Fatalf("ref-agnostic rule rejected source: %v", err)
	}
}

func TestMarketplacePolicyHostPatternMatchesHTTPSAndSSHLikeRust(t *testing.T) {
	policy := restrictedPolicy(MarketplaceAllowedSource{
		Source:      sourceKindPointer(MarketplaceAllowedSourceHostPattern),
		HostPattern: policyStringPointer(`^git\.example\.com$`),
	})
	for _, source := range []string{
		"http://git.example.com/team/plugins.git",
		"https://git.example.com/team/plugins.git",
		"https://user@git.example.com:8443/team/plugins.git",
		"https://user%5Cname@git.example.com/team/plugins.git",
		`http://git.example.com/team\plugins.git`,
		`https://git.example.com/team\@plugins.git`,
		`https://git.example.com?repository=team\@plugins.git`,
		"ssh://git@git.example.com/team/plugins.git",
		"git@git.example.com:team/plugins.git",
	} {
		if err := policy.ValidateSource(policySource(t, source, nil)); err != nil {
			t.Errorf("host pattern rejected %q: %v", source, err)
		}
	}
	if err := policy.ValidateSource(policySource(t, "https://github.com/example/plugins.git", nil)); err == nil {
		t.Fatal("host pattern allowed a different GitHub host")
	}
}

func TestMarketplacePolicyExactLocalRuleRejectsOtherDirectoriesLikeRust(t *testing.T) {
	allowedRoot := t.TempDir()
	deniedRoot := t.TempDir()
	policy := restrictedPolicy(MarketplaceAllowedSource{
		Source: sourceKindPointer(MarketplaceAllowedSourceLocal),
		Path:   policyStringPointer(allowedRoot),
	})
	if err := policy.ValidateSource(policySource(t, allowedRoot, nil)); err != nil {
		t.Fatalf("allowed local source rejected: %v", err)
	}
	if err := policy.ValidateSource(policySource(t, deniedRoot, nil)); err == nil {
		t.Fatal("local source outside the allowlist was allowed")
	}
}

func TestMarketplacePolicyRestrictionFlagControlsEmptyAllowlistLikeRust(t *testing.T) {
	source := policySource(t, "https://github.com/example/plugins.git", nil)
	for _, tc := range []struct {
		restricted bool
		wantOK     bool
	}{{true, false}, {false, true}} {
		policy := NewMarketplacePolicy(&MarketplaceRequirements{RestrictToAllowedSources: boolPointer(tc.restricted)})
		if got := policy.ValidateSource(source) == nil; got != tc.wantOK {
			t.Fatalf("restricted=%v allowed=%v, want %v", tc.restricted, got, tc.wantOK)
		}
	}
}

func TestMarketplacePolicyValidatesConfiguredSourceAndRootLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	configuredRoot := t.TempDir()
	otherRoot := t.TempDir()
	configuredManifest := filepath.Join(configuredRoot, ".agents", "plugins", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(configuredManifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configuredManifest, []byte(`{"name":"company","plugins":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	otherManifest := filepath.Join(otherRoot, ".agents", "plugins", "marketplace.json")
	policy := restrictedPolicy(MarketplaceAllowedSource{
		Source: sourceKindPointer(MarketplaceAllowedSourceLocal),
		Path:   policyStringPointer(configuredRoot),
	})
	values := map[string]any{"marketplaces": map[string]any{
		"company": map[string]any{"source_type": "local", "source": configuredRoot},
	}}

	if err := policy.ValidateInstall(codexHome, configuredManifest, "company", values); err != nil {
		t.Fatalf("configured marketplace rejected: %v", err)
	}
	if err := policy.ValidateInstall(codexHome, configuredManifest, "other", values); err == nil || !strings.Contains(err.Error(), "must be added to config") {
		t.Fatalf("unconfigured marketplace error = %v", err)
	}
	if err := policy.ValidateInstall(codexHome, otherManifest, "company", values); err == nil || !strings.Contains(err.Error(), "does not match configured marketplace") {
		t.Fatalf("mismatched root error = %v", err)
	}
}

func TestMarketplacePolicyBlockedConfiguredSourceIsNotInstallableLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	policy := restrictedPolicy(MarketplaceAllowedSource{
		Source: sourceKindPointer(MarketplaceAllowedSourceGit),
		URL:    policyStringPointer("https://github.com/example/allowed.git"),
	})
	values := map[string]any{"marketplaces": map[string]any{
		"debug": map[string]any{"source_type": "git", "source": "https://github.com/example/blocked.git"},
	}}
	manifest := filepath.Join(codexHome, InstalledMarketplacesDir, "debug", ".agents", "plugins", "marketplace.json")
	err := policy.ValidateInstall(codexHome, manifest, "debug", values)
	if err == nil || !strings.Contains(err.Error(), "is not allowed by requirements") {
		t.Fatalf("blocked marketplace error = %v", err)
	}
}

func TestConfiguredLocalMarketplaceSourceIsNotParsedAsGitShorthandLikeRust(t *testing.T) {
	source, err := configuredMarketplaceSource("company", map[string]any{
		"source_type": "local",
		"source":      "marketplaces/company",
	})
	if err != nil {
		t.Fatalf("configuredMarketplaceSource() error = %v", err)
	}
	if source.Kind != MarketplaceSourceLocal || source.Path != filepath.Clean("marketplaces/company") {
		t.Fatalf("source = %#v", source)
	}
}

func TestMarketplacePolicyCuratedNamespaceLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	policy := restrictedPolicy(MarketplaceAllowedSource{
		Source: sourceKindPointer(MarketplaceAllowedSourceGit),
		URL:    policyStringPointer("https://github.com/openai/plugins.git"),
	})
	curatedRoot := filepath.Join(codexHome, ".tmp", "plugins")
	curatedManifest := filepath.Join(curatedRoot, ".agents", "plugins", "marketplace.json")
	apiManifest := filepath.Join(curatedRoot, ".agents", "plugins", "api_marketplace.json")
	if err := policy.ValidateInstall(codexHome, curatedManifest, OpenAICuratedMarketplaceName, nil); err != nil {
		t.Fatalf("curated install rejected: %v", err)
	}
	if err := policy.ValidateInstall(codexHome, curatedManifest, OpenAIAPICuratedMarketplaceName, nil); err == nil {
		t.Fatal("repository marketplace borrowed the API-curated name")
	}
	if err := policy.ValidateInstall(codexHome, apiManifest, OpenAIAPICuratedMarketplaceName, nil); err != nil {
		t.Fatalf("API curated install rejected: %v", err)
	}

	repositoryManifest := filepath.Join(codexHome, "repository", ".agents", "plugins", "marketplace.json")
	for _, policy := range []*MarketplacePolicy{policy, NewMarketplacePolicy(nil)} {
		for _, name := range []string{
			OpenAICuratedMarketplaceName,
			OpenAIAPICuratedMarketplaceName,
			OpenAIBundledMarketplaceName,
			"openai-bundled-alpha",
			"openai-primary-runtime",
			OpenAIRemoteCuratedMarketplaceName,
			RemoteCreatedByMeMarketplaceName,
			RemoteWorkspaceMarketplaceName,
			RemoteWorkspaceSharedWithMeMarketplace,
			RemoteWorkspaceSharedWithMePrivate,
			RemoteWorkspaceSharedWithMeUnlisted,
		} {
			if err := policy.ValidateInstall(codexHome, repositoryManifest, name, nil); err == nil {
				t.Errorf("reserved marketplace %q was accepted from a repository", name)
			}
		}
	}
}

func TestMarketplacePolicyLocalAllowlistCannotAuthorizeCuratedLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	curatedRoot := filepath.Join(codexHome, ".tmp", "plugins")
	policy := restrictedPolicy(MarketplaceAllowedSource{
		Source: sourceKindPointer(MarketplaceAllowedSourceLocal),
		Path:   policyStringPointer(curatedRoot),
	})
	if _, err := policy.ValidateGitSource(OpenAIPluginsGitURL, nil); err == nil {
		t.Fatal("local allowlist authorized the curated Git source")
	}
	for _, name := range []string{OpenAIBundledMarketplaceName, "openai-bundled-alpha"} {
		root := filepath.Join(codexHome, ".tmp", "bundled-marketplaces", name)
		manifest := filepath.Join(root, ".agents", "plugins", "marketplace.json")
		if err := policy.ValidateInstall(codexHome, manifest, name, nil); err != nil {
			t.Errorf("managed marketplace %q rejected: %v", name, err)
		}
	}
}

func TestMarketplacePolicyManagedBundledSourceIsBoundToExpectedNameLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	root := filepath.Join(codexHome, ".tmp", "bundled-marketplaces", OpenAIBundledMarketplaceName)
	policy := restrictedPolicy()
	source := &ParsedMarketplaceSource{Kind: MarketplaceSourceLocal, Path: root}
	expected, err := policy.ValidateMarketplaceSourceForAdd(codexHome, source)
	if err != nil {
		t.Fatalf("managed source rejected: %v", err)
	}
	if expected != OpenAIBundledMarketplaceName {
		t.Fatalf("expected managed name = %q", expected)
	}
	if err := ValidateMarketplaceNameForAdd(expected, OpenAIBundledMarketplaceName); err != nil {
		t.Fatalf("expected name rejected: %v", err)
	}
	if err := ValidateMarketplaceNameForAdd(expected, "other"); err == nil {
		t.Fatal("managed source accepted a different marketplace name")
	}
}

func TestMarketplacePolicyFiltersProjectedConfigLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	policy := restrictedPolicy(MarketplaceAllowedSource{
		Source: sourceKindPointer(MarketplaceAllowedSourceGit),
		URL:    policyStringPointer("https://github.com/example/allowed.git"),
	})
	values := map[string]any{
		"marketplaces": map[string]any{
			"allowed": map[string]any{"source_type": "git", "source": "https://github.com/example/allowed.git"},
			"blocked": map[string]any{"source_type": "git", "source": "https://github.com/example/blocked.git"},
		},
		"plugins": map[string]any{
			"_default":       map[string]any{"enabled": true},
			"sample@allowed": map[string]any{"enabled": true},
			"sample@blocked": map[string]any{"enabled": true},
		},
	}
	filtered, ok := policy.FilterPluginConfig(codexHome, values)
	if !ok {
		t.Fatal("FilterPluginConfig() ignored plugin config")
	}
	marketplaces := filtered["marketplaces"].(map[string]any)
	if len(marketplaces) != 1 || marketplaces["allowed"] == nil {
		t.Fatalf("filtered marketplaces = %#v", marketplaces)
	}
	plugins := filtered["plugins"].(map[string]any)
	if len(plugins) != 2 || plugins["_default"] == nil || plugins["sample@allowed"] == nil {
		t.Fatalf("filtered plugins = %#v", plugins)
	}
	if values["marketplaces"].(map[string]any)["blocked"] == nil || values["plugins"].(map[string]any)["sample@blocked"] == nil {
		t.Fatal("FilterPluginConfig mutated the input config")
	}
}

func TestMarketplacePolicyRetainsManagedBundledConfigOnlyAtOwnedPathLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	bundledRoot := filepath.Join(codexHome, ".tmp", "bundled-marketplaces", OpenAIBundledMarketplaceName)
	notManaged := filepath.Join(t.TempDir(), "not-managed")
	values := map[string]any{
		"marketplaces": map[string]any{
			OpenAIBundledMarketplaceName: map[string]any{"source_type": "local", "source": bundledRoot},
			"openai-bundled-alpha":       map[string]any{"source_type": "local", "source": notManaged},
			"evil":                       map[string]any{"source_type": "local", "source": bundledRoot},
		},
		"plugins": map[string]any{
			"sample@openai-bundled":       map[string]any{"enabled": true},
			"sample@openai-bundled-alpha": map[string]any{"enabled": true},
			"sample@evil":                 map[string]any{"enabled": true},
		},
	}
	filtered, ok := restrictedPolicy().FilterPluginConfig(codexHome, values)
	if !ok {
		t.Fatal("FilterPluginConfig() ignored plugin config")
	}
	marketplaces := filtered["marketplaces"].(map[string]any)
	if len(marketplaces) != 1 || marketplaces[OpenAIBundledMarketplaceName] == nil {
		t.Fatalf("filtered marketplaces = %#v", marketplaces)
	}
	plugins := filtered["plugins"].(map[string]any)
	if len(plugins) != 1 || plugins["sample@openai-bundled"] == nil {
		t.Fatalf("filtered plugins = %#v", plugins)
	}
}

func TestMarketplacePolicyAllowlistedSourcesCannotClaimReservedNamesLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	sourceRoot := t.TempDir()
	policy := restrictedPolicy(MarketplaceAllowedSource{
		Source: sourceKindPointer(MarketplaceAllowedSourceLocal),
		Path:   policyStringPointer(sourceRoot),
	})
	values := map[string]any{
		"marketplaces": map[string]any{
			OpenAIBundledMarketplaceName: map[string]any{"source_type": "local", "source": sourceRoot},
			OpenAICuratedMarketplaceName: map[string]any{"source_type": "local", "source": sourceRoot},
		},
		"plugins": map[string]any{
			"sample@openai-bundled": map[string]any{"enabled": true},
			"sample@openai-curated": map[string]any{"enabled": true},
		},
	}
	filtered, ok := policy.FilterPluginConfig(codexHome, values)
	if !ok {
		t.Fatal("FilterPluginConfig() ignored plugin config")
	}
	if len(filtered["marketplaces"].(map[string]any)) != 0 || len(filtered["plugins"].(map[string]any)) != 0 {
		t.Fatalf("reserved names survived filtering: %#v", filtered)
	}
}

func TestMarketplacePolicyInvalidRulesFailClosedLikeRust(t *testing.T) {
	valid := MarketplaceAllowedSource{
		Source: sourceKindPointer(MarketplaceAllowedSourceGit),
		URL:    policyStringPointer("https://github.com/example/plugins.git"),
	}
	invalid := MarketplaceAllowedSource{
		Source:      sourceKindPointer(MarketplaceAllowedSourceHostPattern),
		HostPattern: policyStringPointer("("),
	}
	policy := NewMarketplacePolicy(&MarketplaceRequirements{
		RestrictToAllowedSources: boolPointer(true),
		AllowedSources: map[string]MarketplaceAllowedSource{
			"allowed": valid,
			"invalid": invalid,
		},
	})
	err := policy.ValidateSource(policySource(t, "https://github.com/example/plugins.git", nil))
	if err == nil || !strings.Contains(err.Error(), "invalid marketplace allowed source `invalid`") {
		t.Fatalf("invalid active rule error = %v", err)
	}

	for _, tc := range []struct {
		name  string
		rule  MarketplaceAllowedSource
		match string
	}{
		{"missing source", MarketplaceAllowedSource{URL: policyStringPointer("https://github.com/example/plugins.git")}, "missing source"},
		{"missing url", MarketplaceAllowedSource{Source: sourceKindPointer(MarketplaceAllowedSourceGit)}, "missing url"},
		{"empty ref", MarketplaceAllowedSource{Source: sourceKindPointer(MarketplaceAllowedSourceGit), URL: policyStringPointer("https://github.com/example/plugins.git"), RefName: policyStringPointer(" ")}, "ref must not be empty"},
		{"relative local", MarketplaceAllowedSource{Source: sourceKindPointer(MarketplaceAllowedSourceLocal), Path: policyStringPointer("../plugins")}, "local path must be absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := restrictedPolicy(tc.rule).ValidateSource(policySource(t, "https://github.com/example/plugins.git", nil))
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("error = %v, want %q", err, tc.match)
			}
		})
	}
}

func TestMarketplacePolicyBlockedOrReservedUpgradeIsRejectedBeforeInstallationLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(codexHome)
	service.SetMarketplacePolicy(&MarketplaceRequirements{RestrictToAllowedSources: boolPointer(true)}, nil)
	materializer := &recordingMarketplaceMaterializer{}
	service.SetMarketplaceMaterializer(materializer)
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(*ParsedMarketplaceSource) (string, error) {
		t.Fatal("blocked marketplace reached the revision resolver")
		return "", nil
	}))
	reload := func() (map[string]any, error) {
		t.Fatal("blocked marketplace reloaded config")
		return nil, nil
	}
	values := map[string]any{"marketplaces": map[string]any{
		"debug": map[string]any{"source_type": "git", "source": "https://github.com/example/blocked.git"},
	}}
	outcome, err := service.UpgradeConfiguredMarketplaces(values, "debug", reload)
	if err != nil {
		t.Fatalf("UpgradeConfiguredMarketplaces() error = %v", err)
	}
	if len(outcome.SelectedMarketplaces) != 1 || outcome.SelectedMarketplaces[0] != "debug" {
		t.Fatalf("selected = %#v", outcome.SelectedMarketplaces)
	}
	if len(outcome.UpgradedRoots) != 0 || len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0].Message, "is not allowed by requirements") {
		t.Fatalf("outcome = %#v", outcome)
	}
	if len(materializer.destinations) != 0 {
		t.Fatalf("blocked marketplace materialized into %#v", materializer.destinations)
	}
	if _, err := os.Stat(filepath.Join(codexHome, InstalledMarketplacesDir)); !os.IsNotExist(err) {
		t.Fatalf("marketplace install root exists after rejection: %v", err)
	}

	service.SetMarketplacePolicy(nil, nil)
	reservedValues := map[string]any{"marketplaces": map[string]any{
		OpenAIBundledMarketplaceName: map[string]any{"source_type": "git", "source": "https://github.com/example/blocked.git"},
	}}
	outcome, err = service.UpgradeConfiguredMarketplaces(reservedValues, OpenAIBundledMarketplaceName, reload)
	if err != nil {
		t.Fatalf("UpgradeConfiguredMarketplaces(reserved) error = %v", err)
	}
	if len(outcome.UpgradedRoots) != 0 || len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0].Message, "is reserved") {
		t.Fatalf("reserved outcome = %#v", outcome)
	}
	if _, err := os.Stat(filepath.Join(codexHome, InstalledMarketplacesDir)); !os.IsNotExist(err) {
		t.Fatalf("marketplace install root exists after reserved rejection: %v", err)
	}
}

func TestMarketplacePolicyBlocksCuratedSyncLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplacePolicy(&MarketplaceRequirements{RestrictToAllowedSources: boolPointer(true)}, nil)
	called := false
	service.SetMarketplaceMaterializer(MarketplaceMaterializerFunc(func(*ParsedMarketplaceSource, []string, string) error {
		called = true
		return nil
	}))
	if service.StartCuratedRepoSync(nil) {
		t.Fatal("curated sync started without an allowlisted OpenAI Git source")
	}
	if called {
		t.Fatal("curated sync called the materializer after policy rejection")
	}
}

func TestMarketplacePolicyWindowsManagedPathNormalizationLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows device-path normalization")
	}
	if !isExpectedManagedPath(`\\?\c:\users\USER\.cache`, `C:\Users/user\.cache`) {
		t.Fatal("Windows device path did not match its owned root")
	}
}

func TestPluginInstallRejectsBlockedAndReservedMarketplacesLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	blockedRoot := t.TempDir()
	manifest := filepath.Join(blockedRoot, ".agents", "plugins", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"debug","plugins":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service.SetMarketplacePolicy(&MarketplaceRequirements{
		RestrictToAllowedSources: boolPointer(true),
		AllowedSources: map[string]MarketplaceAllowedSource{
			"allowed": {
				Source: sourceKindPointer(MarketplaceAllowedSourceGit),
				URL:    policyStringPointer("https://github.com/example/allowed.git"),
			},
		},
	}, map[string]any{"marketplaces": map[string]any{
		"debug": map[string]any{"source_type": "git", "source": "https://github.com/example/blocked.git"},
	}})
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			ID:              "sample@debug",
			Name:            "sample",
			MarketplaceName: "debug",
		},
		MarketplaceRoot: blockedRoot,
	})
	if _, err := service.Install(&PluginInstallParams{PluginID: "sample@debug"}); err == nil || !strings.Contains(err.Error(), "is not allowed by requirements") {
		t.Fatalf("blocked Install() error = %v", err)
	}

	service.SetMarketplacePolicy(nil, nil)
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			ID:              "sample@openai-bundled",
			Name:            "sample",
			MarketplaceName: OpenAIBundledMarketplaceName,
		},
		MarketplaceRoot: blockedRoot,
	})
	if _, err := service.Install(&PluginInstallParams{PluginID: "sample@openai-bundled"}); err == nil || !strings.Contains(err.Error(), "is reserved") {
		t.Fatalf("reserved Install() error = %v", err)
	}
}

func TestPluginInstallKeepsRemoteMarketplaceSourcesOutsidePolicyLikeRust(t *testing.T) {
	service := NewPluginService()
	service.SetCodexHome(t.TempDir())
	service.SetMarketplacePolicy(&MarketplaceRequirements{RestrictToAllowedSources: boolPointer(true)}, nil)
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			ID:              "sample@created-by-me-remote",
			Name:            "sample",
			MarketplaceName: RemoteCreatedByMeMarketplaceName,
			Source:          PluginSource{Type: "local", Path: t.TempDir()},
		},
		MarketplaceRoot: t.TempDir(),
	})
	response, err := service.Install(&PluginInstallParams{PluginID: "sample@created-by-me-remote"})
	if err != nil {
		t.Fatalf("remote Install() error = %v", err)
	}
	if response == nil || response.PluginID != "sample@created-by-me-remote" {
		t.Fatalf("remote Install() response = %#v", response)
	}
}

func sourceKindPointer(kind MarketplaceAllowedSourceKind) *MarketplaceAllowedSourceKind {
	return &kind
}

func boolPointer(value bool) *bool {
	return &value
}
