package config

import (
	"errors"
	"strings"
	"testing"
)

// Mirrors Rust ConfigServer::validated_base_url: HTTPS without userinfo,
// fragments, or a query, and OAuth endpoints with a client ID and nonzero
// redirect port when specified.
func TestConfigServerValidationLikeRust(t *testing.T) {
	oauthRedirect := uint16(0)
	valid := &ConfigServer{
		BaseURL: "https://config.example.com/settings",
		OAuth: &ConfigServerOAuth{
			AuthorizationURL: "https://config.example.com/authorize",
			TokenURL:         "https://config.example.com/token",
			ClientID:         "company-client",
		},
	}
	base, err := valid.ValidatedBaseURL()
	if err != nil || base != "https://config.example.com/settings" {
		t.Fatalf("ValidatedBaseURL() = %q, %v", base, err)
	}
	bundleURL, err := valid.BundleURL()
	if err != nil || bundleURL != "https://config.example.com/settings/codex/config/bundle" {
		t.Fatalf("BundleURL() = %q, %v", bundleURL, err)
	}
	trailing := &ConfigServer{BaseURL: "https://config.example.com/settings/"}
	if bundleURL, err := trailing.BundleURL(); err != nil || bundleURL != "https://config.example.com/settings/codex/config/bundle" {
		t.Fatalf("trailing BundleURL() = %q, %v", bundleURL, err)
	}

	cases := map[string]*ConfigServer{
		"plain http":              {BaseURL: "http://config.example.com"},
		"userinfo":                {BaseURL: "https://user:pass@config.example.com"},
		"fragment":                {BaseURL: "https://config.example.com#fragment"},
		"query":                   {BaseURL: "https://config.example.com?token=secret"},
		"oauth plain http":        {BaseURL: "https://config.example.com", OAuth: &ConfigServerOAuth{AuthorizationURL: "http://config.example.com/a", TokenURL: "https://config.example.com/token", ClientID: "c"}},
		"oauth missing client id": {BaseURL: "https://config.example.com", OAuth: &ConfigServerOAuth{AuthorizationURL: "https://config.example.com/a", TokenURL: "https://config.example.com/token"}},
		"oauth zero port":         {BaseURL: "https://config.example.com", OAuth: &ConfigServerOAuth{AuthorizationURL: "https://config.example.com/a", TokenURL: "https://config.example.com/token", ClientID: "c", RedirectPort: &oauthRedirect}},
	}
	for name, server := range cases {
		if _, err := server.ValidatedBaseURL(); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

// Mirrors Rust ConfigRequirementsToml.config_server parsing.
func TestConfigServerRequirementsParsingLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[config_server]
base_url = "https://config.example.com/settings"

[config_server.oauth]
authorization_url = "https://config.example.com/authorize"
token_url = "https://config.example.com/token"
client_id = "company-client"
scopes = ["managed-config"]
redirect_port = 1455
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || requirements.ConfigServer == nil {
		t.Fatalf("config server = %#v", requirements)
	}
	if !requirements.RequiresRemoteConfig() {
		t.Fatal("RequiresRemoteConfig() = false")
	}
	server := requirements.ConfigServer
	if server.BaseURL != "https://config.example.com/settings" || server.OAuth == nil {
		t.Fatalf("server = %#v", server)
	}
	if server.OAuth.ClientID != "company-client" || len(server.OAuth.Scopes) != 1 || server.OAuth.RedirectPort == nil || *server.OAuth.RedirectPort != 1455 {
		t.Fatalf("oauth = %#v", server.OAuth)
	}

	for name, document := range map[string]string{
		"plain http":    "[config_server]\nbase_url = \"http://config.example.com\"\n",
		"unknown field": "[config_server]\nbase_url = \"https://config.example.com\"\nunexpected = true\n",
		"missing base":  "[config_server]\noauth = { authorization_url = \"https://x/a\", token_url = \"https://x/t\", client_id = \"c\" }\n",
	} {
		if _, err := ParseRequirementsTOML([]byte(document)); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

// Mirrors Rust #56055: cloud-managed requirement layers may never select the
// managed configuration source. The anchor rejects the fragment instead of
// silently dropping it, because an endpoint and its credentials must come from
// the same local requirements layer.
func TestCloudManagedConfigServerRejectedLikeRust(t *testing.T) {
	local := &ConfigRequirements{ConfigServer: &ConfigServer{BaseURL: "https://local.example.com"}}
	bundle := CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
			ID:       "remote",
			Name:     "remote",
			Contents: "[config_server]\nbase_url = \"https://attacker.example.com\"\n",
		}}},
	}
	merged, err := applyCloudConfigBundle(map[string]any{}, local, bundle, t.TempDir())
	if !errors.Is(err, ErrInvalidCloudConfig) || !strings.Contains(err.Error(), "config_server") {
		t.Fatalf("applyCloudConfigBundle() error = %v, want an invalid cloud config error naming config_server", err)
	}
	if merged != nil {
		t.Fatalf("applyCloudConfigBundle() requirements = %#v, want nil", merged)
	}
}

// Mirrors Rust #56314 validation.rs: generic errors never expose delivered
// contents.
func TestCloudConfigBundleValidationErrorsOmitContentsLikeRust(t *testing.T) {
	secret := "super-secret-requirement-value"
	bundle := CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
			ID:       "secret-fragment",
			Name:     "secret-fragment",
			Contents: "allowed_approval_policies = [\"" + secret + "\"\n",
		}}},
	}
	err := validateCloudConfigBundle(bundle, t.TempDir())
	if err == nil {
		t.Fatal("invalid bundle must be rejected")
	}
	if err.Error() != "invalid cloud config bundle: invalid requirements" {
		t.Fatalf("error = %q", err.Error())
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "secret-fragment") {
		t.Fatalf("validation error leaked bundle contents: %q", err.Error())
	}
}

// Mirrors Rust CloudConfigBundleLoader::get_snapshot carrying the source.
func TestCloudConfigLoaderSnapshotCarriesConfigServerLikeRust(t *testing.T) {
	server := &ConfigServer{BaseURL: "https://config.example.com"}
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) {
		return &CloudConfigBundle{}, nil
	}).WithConfigServer(server)
	snapshot := loader.GetSnapshot()
	if snapshot.Err != nil || snapshot.ConfigServer == nil || snapshot.ConfigServer.BaseURL != server.BaseURL {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if loader.ConfigServer() == nil {
		t.Fatal("ConfigServer() = nil")
	}
}

// Mirrors Rust #56055's applied-server check: a required customer-hosted
// configuration server must be the one whose bundle was applied, while a
// trailing slash on the base URL is not a mismatch.
func TestValidateAppliedConfigServerLikeRust(t *testing.T) {
	required := &ConfigServer{BaseURL: "https://required.example.com/settings"}
	if err := ValidateAppliedConfigServer(nil, nil); err != nil {
		t.Fatalf("ValidateAppliedConfigServer(nil, nil) = %v", err)
	}
	if err := ValidateAppliedConfigServer(required, required); err != nil {
		t.Fatalf("matching server rejected: %v", err)
	}
	if err := ValidateAppliedConfigServer(&ConfigServer{BaseURL: "https://required.example.com/settings/"}, required); err != nil {
		t.Fatalf("trailing slash rejected: %v", err)
	}
	if err := ValidateAppliedConfigServer(&ConfigServer{BaseURL: "https://other.example.com/settings"}, required); !errors.Is(err, ErrConfigServerMismatch) {
		t.Fatalf("ValidateAppliedConfigServer(mismatch) = %v, want ErrConfigServerMismatch", err)
	}
	if err := ValidateAppliedConfigServer(nil, required); !errors.Is(err, ErrConfigServerMismatch) {
		t.Fatalf("ValidateAppliedConfigServer(nil, required) = %v, want ErrConfigServerMismatch", err)
	}
}

// Mirrors Rust #56055: a fetched policy bundle may never select the
// customer-hosted configuration server, because that setting is a local
// administration requirement.
func TestFetchedPolicyRejectsConfigServerLikeRust(t *testing.T) {
	_, _, err := applyCloudConfigBundleWithFilesystemSource(map[string]any{}, nil, CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "req", Name: "Requirements", Contents: "[config_server]\nbase_url = \"https://config.example.com/settings\"\n"},
		}},
	}, t.TempDir())
	if !errors.Is(err, ErrInvalidCloudConfig) || !strings.Contains(err.Error(), "config_server") {
		t.Fatalf("applyCloudConfigBundleWithFilesystemSource() error = %v, want an invalid cloud config error naming config_server", err)
	}
	// The same fragment without config_server still applies.
	requirements, _, err := applyCloudConfigBundleWithFilesystemSource(map[string]any{}, nil, CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "req", Name: "Requirements", Contents: "[features]\nfast_mode = true\n"},
		}},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("applyCloudConfigBundleWithFilesystemSource() error = %v", err)
	}
	if requirements == nil || !requirements.FeatureRequirements["fast_mode"] {
		t.Fatalf("requirements = %#v, want fast_mode enabled", requirements)
	}
}
