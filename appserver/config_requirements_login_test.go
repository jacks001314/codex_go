package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// TestRuntimeRouterConfigRequirementsReadReportsAllowedLoginMethodsLikeRust
// mirrors Rust #45495: configRequirements/read reports the login methods the
// running policy permits after managed requirements, the forced login method,
// and workspace restrictions, and it returns a requirements object even without
// managed requirements when that policy restricts them (while the unrestricted
// default stays null).
func TestRuntimeRouterConfigRequirementsReadReportsAllowedLoginMethodsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		configTOML       string
		requirementsTOML string
		wantNil          bool
		want             []config.ForcedLoginMethod
	}{
		{
			name:    "unrestricted default stays null",
			wantNil: true,
		},
		{
			name:       "forced api without managed requirements",
			configTOML: "forced_login_method = \"api\"\n",
			want:       []config.ForcedLoginMethod{config.ForcedLoginMethodAPI},
		},
		{
			name:             "managed allowlist",
			requirementsTOML: "allowed_login_methods = [\"chatgpt\"]\n",
			want:             []config.ForcedLoginMethod{config.ForcedLoginMethodChatGPT},
		},
		{
			name:             "forced workspace outside the allowlist",
			configTOML:       "forced_chatgpt_workspace_id = \"managed\"\n",
			requirementsTOML: "allowed_chatgpt_workspaces = [\"other\"]\n",
			want:             []config.ForcedLoginMethod{config.ForcedLoginMethodAPI},
		},
		{
			name:             "forced workspace inside the allowlist",
			configTOML:       "forced_chatgpt_workspace_id = \"managed\"\n",
			requirementsTOML: "allowed_chatgpt_workspaces = [\"managed\"]\n",
			want:             []config.ForcedLoginMethod{config.ForcedLoginMethodAPI, config.ForcedLoginMethodChatGPT},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if testCase.configTOML != "" {
				if err := os.WriteFile(config.ConfigPath(home), []byte(testCase.configTOML), 0o600); err != nil {
					t.Fatalf("WriteFile config error = %v", err)
				}
			}
			if testCase.requirementsTOML != "" {
				if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(testCase.requirementsTOML), 0o600); err != nil {
					t.Fatalf("WriteFile requirements error = %v", err)
				}
			}
			router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
			response := router.Handle(requestWithParams(t, IntID(1), MethodConfigRequirementsRead, map[string]any{}))
			if response.Error != nil {
				t.Fatalf("configRequirements/read error: %+v", response.Error)
			}
			read, ok := response.Result.(*config.ConfigRequirementsReadResponse)
			if !ok || read == nil {
				t.Fatalf("configRequirements/read result = %#v", response.Result)
			}
			if testCase.wantNil {
				if read.Requirements != nil {
					t.Fatalf("requirements = %#v, want null", read.Requirements)
				}
				return
			}
			if read.Requirements == nil {
				t.Fatalf("requirements = nil, want the effective login policy %#v", testCase.want)
			}
			got := read.Requirements.AllowedLoginMethods
			if len(got) != len(testCase.want) {
				t.Fatalf("allowedLoginMethods = %#v, want %#v", got, testCase.want)
			}
			for index := range testCase.want {
				if got[index] != testCase.want[index] {
					t.Fatalf("allowedLoginMethods = %#v, want %#v", got, testCase.want)
				}
			}
		})
	}
}

// TestRuntimeRouterConfigRequirementsReadMatchesRustLoginMatrix mirrors
// codex-rs/app-server/tests/suite/v2/config_requirements_login.rs: every
// combination of managed allowlists, forced login methods, and workspace
// restrictions reports the canonical effective set, and a host with no managed
// requirements stays null when nothing is restricted.
func TestRuntimeRouterConfigRequirementsReadMatchesRustLoginMatrix(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		configTOML string
		reqsTOML   string
		wantNil    bool
		want       []config.ForcedLoginMethod
	}{
		{name: "empty forced workspaces are unrestricted", configTOML: "forced_chatgpt_workspace_id = []\n", wantNil: true},
		{name: "forced workspace does not exclude api", configTOML: "forced_chatgpt_workspace_id = [\"managed\"]\n", wantNil: true},
		{name: "unrestricted with other requirements", reqsTOML: "allow_remote_control = false\n", want: []config.ForcedLoginMethod{config.ForcedLoginMethodAPI, config.ForcedLoginMethodChatGPT}},
		{name: "both normalized", reqsTOML: "allowed_login_methods = [\"chatgpt\", \"api\", \"api\"]\n", want: []config.ForcedLoginMethod{config.ForcedLoginMethodAPI, config.ForcedLoginMethodChatGPT}},
		{name: "forced chatgpt without requirements", configTOML: "forced_login_method = \"chatgpt\"\n", want: []config.ForcedLoginMethod{config.ForcedLoginMethodChatGPT}},
		{name: "forced narrows managed", reqsTOML: "allowed_login_methods = [\"chatgpt\", \"api\"]\n", configTOML: "forced_login_method = \"api\"\n", want: []config.ForcedLoginMethod{config.ForcedLoginMethodAPI}},
		{name: "empty managed workspaces", reqsTOML: "allowed_chatgpt_workspaces = []\n", want: []config.ForcedLoginMethod{config.ForcedLoginMethodAPI}},
		{name: "disjoint workspaces", reqsTOML: "allowed_chatgpt_workspaces = [\"managed\"]\n", configTOML: "forced_chatgpt_workspace_id = [\"other\"]\n", want: []config.ForcedLoginMethod{config.ForcedLoginMethodAPI}},
		{name: "overlapping workspaces", reqsTOML: "allowed_chatgpt_workspaces = [\"managed\"]\n", configTOML: "forced_chatgpt_workspace_id = [\"other\", \"managed\"]\n", want: []config.ForcedLoginMethod{config.ForcedLoginMethodAPI, config.ForcedLoginMethodChatGPT}},
		{name: "api only ignores workspace mismatch", reqsTOML: "allowed_login_methods = [\"api\"]\nallowed_chatgpt_workspaces = [\"managed\"]\n", configTOML: "forced_chatgpt_workspace_id = [\"other\"]\n", want: []config.ForcedLoginMethod{config.ForcedLoginMethodAPI}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if testCase.configTOML != "" {
				if err := os.WriteFile(config.ConfigPath(home), []byte(testCase.configTOML), 0o600); err != nil {
					t.Fatalf("WriteFile config error = %v", err)
				}
			}
			if testCase.reqsTOML != "" {
				if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(testCase.reqsTOML), 0o600); err != nil {
					t.Fatalf("WriteFile requirements error = %v", err)
				}
			}
			router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
			response := router.Handle(requestWithParams(t, IntID(1), MethodConfigRequirementsRead, map[string]any{}))
			if response.Error != nil {
				t.Fatalf("configRequirements/read error: %+v", response.Error)
			}
			read, ok := response.Result.(*config.ConfigRequirementsReadResponse)
			if !ok || read == nil {
				t.Fatalf("configRequirements/read result = %#v", response.Result)
			}
			if testCase.wantNil {
				if read.Requirements != nil {
					t.Fatalf("requirements = %#v, want null", read.Requirements)
				}
				return
			}
			if read.Requirements == nil {
				t.Fatalf("requirements = nil, want allowed methods %#v", testCase.want)
			}
			got := read.Requirements.AllowedLoginMethods
			if !configEqualForcedLoginMethods(got, testCase.want) {
				t.Fatalf("allowedLoginMethods = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestRuntimeRouterConfigRequirementsReadRejectsInvalidLoginMethodLikeRust
// mirrors config_requirements_read_rejects_invalid_login_method: a managed
// allowlist pointing at an unsupported method is an error, not a silently
// narrowed policy.
func TestRuntimeRouterConfigRequirementsReadRejectsInvalidLoginMethodLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allowed_login_methods = [\"saml\"]\n"), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	response := router.Handle(requestWithParams(t, IntID(1), MethodConfigRequirementsRead, map[string]any{}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "allowed_login_methods") {
		t.Fatalf("configRequirements/read error = %+v, want one naming allowed_login_methods", response.Error)
	}
}

// TestRuntimeRouterConfigRequirementsReadUsesRunningLoginPolicyLikeRust mirrors
// config_requirements_read_uses_running_auth_policy: requirements written after
// startup are re-read, but the effective login methods stay those of the
// running policy.
func TestRuntimeRouterConfigRequirementsReadUsesRunningLoginPolicyLikeRust(t *testing.T) {
	for _, refreshed := range []string{"", "allowed_login_methods = [\"chatgpt\"]\n"} {
		t.Run(refreshed, func(t *testing.T) {
			home := t.TempDir()
			requirementsPath := filepath.Join(home, "requirements.toml")
			if err := os.WriteFile(requirementsPath, []byte("allowed_login_methods = [\"api\"]\n"), 0o600); err != nil {
				t.Fatalf("WriteFile requirements error = %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
			if err := os.WriteFile(requirementsPath, []byte(refreshed), 0o600); err != nil {
				t.Fatalf("rewrite requirements error = %v", err)
			}
			response := router.Handle(requestWithParams(t, IntID(1), MethodConfigRequirementsRead, map[string]any{}))
			if response.Error != nil {
				t.Fatalf("configRequirements/read error: %+v", response.Error)
			}
			read, ok := response.Result.(*config.ConfigRequirementsReadResponse)
			if !ok || read == nil || read.Requirements == nil {
				t.Fatalf("configRequirements/read result = %#v", response.Result)
			}
			if !configEqualForcedLoginMethods(read.Requirements.AllowedLoginMethods, []config.ForcedLoginMethod{config.ForcedLoginMethodAPI}) {
				t.Fatalf("allowedLoginMethods = %#v, want the running policy [api]", read.Requirements.AllowedLoginMethods)
			}
		})
	}
}

func configEqualForcedLoginMethods(got, want []config.ForcedLoginMethod) bool {
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
