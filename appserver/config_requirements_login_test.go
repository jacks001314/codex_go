package appserver

import (
	"os"
	"path/filepath"
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
