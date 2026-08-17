package envutil

import (
	"os/exec"
	"strings"
	"testing"

	"codex_go/applypatch"
)

func TestIsNonInheritableEnvVarCaseInsensitiveLikeRust(t *testing.T) {
	for _, name := range []string{
		"OPENAI_FEDERATION_RULE_ID",
		"openai_federation_rule_id",
		"OpenAI_Federation_Rule_Id",
		"OPENAI_IDENTITY_TOKEN_FILE",
		"openai_identity_token_file",
		"OPENAI_WORKLOAD_IDENTITY_CONTEXT",
		"openai_workload_identity_context",
		CodexExecServerNoiseAuthTokenEnvVar,
		"codex_exec_server_noise_auth_token",
		"Codex_Exec_Server_Noise_Auth_Token",
	} {
		if !IsNonInheritableEnvVar(name) {
			t.Fatalf("IsNonInheritableEnvVar(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"PATH", "OPENAI_API_KEY", "OPENAI_FEDERATION_RULE", "CODEX_EXEC_SERVER_NOISE_REGISTRY_URL"} {
		if IsNonInheritableEnvVar(name) {
			t.Fatalf("IsNonInheritableEnvVar(%q) = true, want false", name)
		}
	}
}

func TestScrubSliceAndCommandEnv(t *testing.T) {
	scrubbed := ScrubSlice([]string{
		"PATH=C:\\bin",
		"OPENAI_FEDERATION_RULE_ID=rule-1",
		"openai_identity_token_file=C:\\token",
		"OPENAI_WORKLOAD_IDENTITY_CONTEXT=ctx-1",
		CodexExecServerNoiseAuthTokenEnvVar + "=configured-noise-token",
		"codex_exec_server_noise_auth_token=case-variant-noise-token",
		"HOME=C:\\home",
	})
	if len(scrubbed) != 2 || scrubbed[0] != "PATH=C:\\bin" || scrubbed[1] != "HOME=C:\\home" {
		t.Fatalf("ScrubSlice() = %#v", scrubbed)
	}

	cmd := exec.Command("echo", "hi")
	cmd.Env = []string{"OPENAI_FEDERATION_RULE_ID=inherited", "Codex_Exec_Server_Noise_Auth_Token=inherited"}
	ScrubCommandEnv(cmd)
	if len(cmd.Env) != 0 {
		t.Fatalf("ScrubCommandEnv() = %#v, want empty", cmd.Env)
	}
}

// TestScrubCommandEnvRemovesNoiseAuthTokenAfterPolicyOverrides mirrors Rust
// command_hook_does_not_expose_configured_noise_auth_token (#38941): a shell
// environment policy that explicitly sets the Noise auth token (or a case
// variant) must still be scrubbed before the command or hook runs.
func TestScrubCommandEnvRemovesNoiseAuthTokenAfterPolicyOverrides(t *testing.T) {
	for _, name := range []string{
		CodexExecServerNoiseAuthTokenEnvVar,
		strings.ToLower(CodexExecServerNoiseAuthTokenEnvVar),
		strings.ToUpper(CodexExecServerNoiseAuthTokenEnvVar),
	} {
		cmd := exec.Command("echo", "hi")
		cmd.Env = []string{
			name + "=configured-noise-token",
			"CODEX_HOOK_SAFE_ENV=visible",
		}
		ScrubCommandEnv(cmd)
		for _, pair := range cmd.Env {
			pairName := pair
			for i := 0; i < len(pair); i++ {
				if pair[i] == '=' {
					pairName = pair[:i]
					break
				}
			}
			if IsNonInheritableEnvVar(pairName) {
				t.Fatalf("policy override leaked %q through the scrubber", pair)
			}
		}
		found := false
		for _, pair := range cmd.Env {
			if pair == "CODEX_HOOK_SAFE_ENV=visible" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("safe hook env was dropped: %#v", cmd.Env)
		}
	}
}

func TestScrubCommandEnvInheritsFilteredEnvironment(t *testing.T) {
	t.Setenv("OPENAI_FEDERATION_RULE_ID", "rule-1")
	t.Setenv("OPENAI_IDENTITY_TOKEN_FILE", "token")
	cmd := exec.Command("echo", "hi")
	ScrubCommandEnv(cmd)
	for _, pair := range cmd.Env {
		name := pair
		for i := 0; i < len(pair); i++ {
			if pair[i] == '=' {
				name = pair[:i]
				break
			}
		}
		if IsNonInheritableEnvVar(name) {
			t.Fatalf("inherited env leaked %q", pair)
		}
	}
}

func TestInjectApplyPatchEnvFollowsPreserveLineEndingsFeature(t *testing.T) {
	env := map[string]string{
		"KEEP":                               "value",
		applypatch.PreserveLineEndingsEnvVar: "stale",
	}
	out := InjectApplyPatchEnv(env, false)
	if got := out[applypatch.PreserveLineEndingsEnvVar]; got != "" {
		t.Fatalf("disabled mode left stale var = %q", got)
	}
	if out["KEEP"] != "value" {
		t.Fatalf("KEEP = %q, want value", out["KEEP"])
	}

	out = InjectApplyPatchEnv(out, true)
	if got := out[applypatch.PreserveLineEndingsEnvVar]; got != "1" {
		t.Fatalf("enabled mode var = %q, want 1", got)
	}
	if out["KEEP"] != "value" {
		t.Fatalf("KEEP = %q, want value", out["KEEP"])
	}
}

func TestInjectApplyPatchEnvRemovesDifferentlyCasedStaleKey(t *testing.T) {
	env := InjectApplyPatchEnv(map[string]string{
		"codex_apply_patch_preserve_line_endings": "0",
	}, true)
	if len(env) != 1 || env[applypatch.PreserveLineEndingsEnvVar] != "1" {
		t.Fatalf("InjectApplyPatchEnv() = %#v", env)
	}
}

func TestInjectApplyPatchEnvHandlesNilMap(t *testing.T) {
	out := InjectApplyPatchEnv(nil, false)
	if out == nil || len(out) != 0 {
		t.Fatalf("InjectApplyPatchEnv(nil, false) = %#v, want empty map", out)
	}
	out = InjectApplyPatchEnv(nil, true)
	if out == nil || out[applypatch.PreserveLineEndingsEnvVar] != "1" {
		t.Fatalf("InjectApplyPatchEnv(nil, true) = %#v", out)
	}
}
