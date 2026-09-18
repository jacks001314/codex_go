package config

import (
	"encoding/json"
	"strings"
	"testing"
)

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

// TestConfigAllowedLoginMethodsFollowsForcedWorkspacesLikeRust mirrors Rust's
// config_manager_service_tests::allowed_login_methods_follow_current_forced_workspaces.
func TestConfigAllowedLoginMethodsFollowsForcedWorkspacesLikeRust(t *testing.T) {
	requirements := &ConfigRequirements{AllowedChatGPTWorkspaces: []string{"managed"}}
	for _, testCase := range []struct {
		name       string
		workspaces []string
		want       []ForcedLoginMethod
	}{
		{
			name:       "forced workspace is allowed by the allowlist",
			workspaces: []string{"managed"},
			want:       []ForcedLoginMethod{ForcedLoginMethodAPI, ForcedLoginMethodChatGPT},
		},
		{
			name:       "forced workspace outside the allowlist leaves API only",
			workspaces: []string{"other"},
			want:       []ForcedLoginMethod{ForcedLoginMethodAPI},
		},
		{
			name:       "no forced workspaces is unrestricted",
			workspaces: nil,
			want:       []ForcedLoginMethod{ForcedLoginMethodAPI, ForcedLoginMethodChatGPT},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{
				Values:       map[string]any{"forced_chatgpt_workspace_id": testCase.workspaces},
				Requirements: requirements,
			}
			if got := cfg.AllowedLoginMethods(); !equalForcedLoginMethods(got, testCase.want) {
				t.Fatalf("AllowedLoginMethods() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestConfigAllowedLoginMethodsCombinesForcedMethodAndAllowlistLikeRust covers
// the forced-method and allowlist halves of the effective policy.
func TestConfigAllowedLoginMethodsCombinesForcedMethodAndAllowlistLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		forcedMethod   string
		allowedMethods []ForcedLoginMethod
		want           []ForcedLoginMethod
	}{
		{
			name: "unrestricted",
			want: []ForcedLoginMethod{ForcedLoginMethodAPI, ForcedLoginMethodChatGPT},
		},
		{
			name:         "forced api",
			forcedMethod: "api",
			want:         []ForcedLoginMethod{ForcedLoginMethodAPI},
		},
		{
			name:           "allowlist chatgpt only",
			allowedMethods: []ForcedLoginMethod{ForcedLoginMethodChatGPT},
			want:           []ForcedLoginMethod{ForcedLoginMethodChatGPT},
		},
		{
			name:           "empty allowlist permits nothing",
			allowedMethods: []ForcedLoginMethod{},
			want:           []ForcedLoginMethod{},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			values := map[string]any{}
			if testCase.forcedMethod != "" {
				values["forced_login_method"] = testCase.forcedMethod
			}
			cfg := &Config{Values: values, Requirements: &ConfigRequirements{AllowedLoginMethods: testCase.allowedMethods}}
			got := cfg.AllowedLoginMethods()
			if !equalForcedLoginMethods(got, testCase.want) || (len(testCase.want) == 0 && got == nil) {
				t.Fatalf("AllowedLoginMethods() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestProjectRequirementsWithAllowedLoginMethodsMatchesRust mirrors Rust's
// map_requirements_to_api: requirements are absent only for the unrestricted
// default, and the effective login methods are always reported otherwise.
func TestProjectRequirementsWithAllowedLoginMethodsMatchesRust(t *testing.T) {
	unrestricted := []ForcedLoginMethod{ForcedLoginMethodAPI, ForcedLoginMethodChatGPT}
	if got := ProjectRequirementsWithAllowedLoginMethods(nil, unrestricted); got != nil {
		t.Fatalf("unrestricted projection = %#v, want nil", got)
	}
	// A restricted policy without managed requirements still returns an object.
	restricted := ProjectRequirementsWithAllowedLoginMethods(nil, []ForcedLoginMethod{ForcedLoginMethodAPI})
	if restricted == nil || !equalForcedLoginMethods(restricted.AllowedLoginMethods, []ForcedLoginMethod{ForcedLoginMethodAPI}) {
		t.Fatalf("restricted projection = %#v", restricted)
	}
	// An empty list permits no login method and stays a non-nil slice, so it
	// serializes as [] rather than null.
	none := ProjectRequirementsWithAllowedLoginMethods(&ConfigRequirements{}, []ForcedLoginMethod{})
	if none == nil || none.AllowedLoginMethods == nil || len(none.AllowedLoginMethods) != 0 {
		t.Fatalf("empty projection = %#v", none)
	}
	encoded, err := json.Marshal(none)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"allowedLoginMethods":[]`) {
		t.Fatalf("encoded requirements = %s, want an empty allowedLoginMethods array", encoded)
	}
	// Managed requirements are preserved and the effective list replaces the
	// file's own allowlist.
	projected := ProjectRequirementsWithAllowedLoginMethods(
		&ConfigRequirements{AllowedLoginMethods: []ForcedLoginMethod{ForcedLoginMethodChatGPT}, ChatgptBaseURL: stringPtr("https://example.test")},
		[]ForcedLoginMethod{ForcedLoginMethodAPI, ForcedLoginMethodChatGPT},
	)
	if projected == nil || projected.ChatgptBaseURL == nil || *projected.ChatgptBaseURL != "https://example.test" ||
		!equalForcedLoginMethods(projected.AllowedLoginMethods, unrestricted) {
		t.Fatalf("projected requirements = %#v", projected)
	}
}

func equalForcedLoginMethods(got []ForcedLoginMethod, want []ForcedLoginMethod) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
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
