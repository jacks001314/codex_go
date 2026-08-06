package config

import "testing"

func TestManagedAuthPolicyAllowsLoginMethodComposesRestrictionsLikeRust(t *testing.T) {
	policy := &ManagedAuthPolicy{AllowedLoginMethods: []ForcedLoginMethod{ForcedLoginMethodChatGPT}}
	// API key is disallowed by the allowlist even without a forced method.
	if policy.AllowsLoginMethod(ForcedLoginMethodAPI, "", nil) {
		t.Fatal("API key login allowed despite chatgpt-only allowlist")
	}
	if !policy.AllowsLoginMethod(ForcedLoginMethodChatGPT, "", nil) {
		t.Fatal("ChatGPT login denied despite allowlist")
	}
	// nil policy is permissive.
	if !(&ManagedAuthPolicy{}).AllowsLoginMethod(ForcedLoginMethodAPI, "", nil) {
		t.Fatal("empty policy denied API login")
	}
}

func TestManagedAuthPolicyForcedMethodCombinesWithAllowlistLikeRust(t *testing.T) {
	policy := &ManagedAuthPolicy{AllowedLoginMethods: []ForcedLoginMethod{ForcedLoginMethodAPI, ForcedLoginMethodChatGPT}}
	// The forced method must match AND the allowlist must contain the method.
	if policy.AllowsLoginMethod(ForcedLoginMethodChatGPT, ForcedLoginMethodAPI, nil) {
		t.Fatal("ChatGPT login allowed while API is forced")
	}
	if !policy.AllowsLoginMethod(ForcedLoginMethodAPI, ForcedLoginMethodAPI, nil) {
		t.Fatal("API login denied while API is forced and allowlisted")
	}
	// A narrow allowlist still rejects an otherwise-forced method.
	narrow := &ManagedAuthPolicy{AllowedLoginMethods: []ForcedLoginMethod{ForcedLoginMethodChatGPT}}
	if narrow.AllowsLoginMethod(ForcedLoginMethodAPI, ForcedLoginMethodAPI, nil) {
		t.Fatal("API login allowed while API is forced but absent from allowlist")
	}
}

func TestManagedAuthPolicyWorkspaceIntersectionLikeRust(t *testing.T) {
	policy := &ManagedAuthPolicy{AllowedChatGPTWorkspaces: []string{"w1", "w3"}}
	forced := []string{"w1", "w2"}
	workspaces, restricted := policy.EffectiveChatGPTWorkspaces(forced)
	if !restricted || len(workspaces) != 1 || workspaces[0] != "w1" {
		t.Fatalf("intersection = %v, %v; want [w1], true", workspaces, restricted)
	}
	// Empty intersection rejects ChatGPT logins.
	emptyPolicy := &ManagedAuthPolicy{AllowedChatGPTWorkspaces: []string{"other"}}
	if emptyPolicy.AllowsLoginMethod(ForcedLoginMethodChatGPT, "", forced) {
		t.Fatal("ChatGPT login allowed with empty workspace intersection")
	}
	// No forced workspaces and no allowlist is unrestricted.
	workspaces, restricted = (&ManagedAuthPolicy{}).EffectiveChatGPTWorkspaces(nil)
	if restricted || workspaces != nil {
		t.Fatalf("unrestricted = %v, %v; want nil, false", workspaces, restricted)
	}
}

func TestConfigIsLoginMethodAllowedUsesRequirementsLikeRust(t *testing.T) {
	cfg := &Config{Requirements: &ConfigRequirements{
		AllowedLoginMethods: []ForcedLoginMethod{ForcedLoginMethodAPI},
	}}
	if !cfg.IsLoginMethodAllowed(ForcedLoginMethodAPI) {
		t.Fatal("API login denied with api allowlist")
	}
	if cfg.IsLoginMethodAllowed(ForcedLoginMethodChatGPT) {
		t.Fatal("ChatGPT login allowed with api-only allowlist")
	}
	// Config values override via forced method.
	cfg.Values = map[string]any{"forced_login_method": "api"}
	if cfg.IsLoginMethodAllowed(ForcedLoginMethodChatGPT) {
		t.Fatal("ChatGPT login allowed while api is forced")
	}
}

func TestParseRequirementsTOMLAuthAllowlists(t *testing.T) {
	data := []byte(`
allowed_login_methods = ["api", "chatgpt"]
allowed_chatgpt_workspaces = ["ws-1", "ws-2"]
`)
	requirements, err := ParseRequirementsTOML(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(requirements.AllowedLoginMethods) != 2 || requirements.AllowedLoginMethods[0] != ForcedLoginMethodAPI || requirements.AllowedLoginMethods[1] != ForcedLoginMethodChatGPT {
		t.Fatalf("AllowedLoginMethods = %#v", requirements.AllowedLoginMethods)
	}
	if len(requirements.AllowedChatGPTWorkspaces) != 2 || requirements.AllowedChatGPTWorkspaces[0] != "ws-1" {
		t.Fatalf("AllowedChatGPTWorkspaces = %#v", requirements.AllowedChatGPTWorkspaces)
	}
}
