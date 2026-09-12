package plugin

import (
	"strings"
	"testing"

	"codex_go/envutil"
)

func TestIsolatedPluginGitEnvStripsScopedConfigLikeRust(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "url.https://evil.invalid/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'url.https://evil.invalid/.insteadOf'")
	t.Setenv("GIT_CONFIG_GLOBAL", "/attacker/gitconfig")
	t.Setenv("GIT_EXEC_PATH", "/workspace/.git/hooks")
	t.Setenv("DEVELOPER_DIR", "/workspace/toolchain")
	env, ok := isolatedPluginGitEnv()
	if !ok {
		t.Skip("no trusted executable directories on this host")
	}
	for _, pair := range env {
		name := pair
		if idx := strings.IndexByte(pair, '='); idx >= 0 {
			name = pair[:idx]
		}
		if name == "GIT_CONFIG_COUNT" || name == "GIT_CONFIG_PARAMETERS" ||
			name == "GIT_EXEC_PATH" || name == "GIT_TEMPLATE_DIR" || name == "DEVELOPER_DIR" ||
			strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			t.Fatalf("scoped git config variable survived isolation: %s", name)
		}
		if pair == "GIT_CONFIG_GLOBAL=/attacker/gitconfig" {
			t.Fatal("attacker GIT_CONFIG_GLOBAL survived isolation")
		}
	}
	joined := strings.Join(env, "\x00")
	if !strings.Contains(joined, "GIT_CONFIG_GLOBAL="+isolatedGitConfigPathValue) {
		t.Fatal("isolated GIT_CONFIG_GLOBAL was not set to the empty trusted config")
	}
	trustedPath, _ := envutil.TrustedSystemPath()
	if !strings.Contains(joined, "PATH="+trustedPath) {
		t.Fatalf("isolated git PATH is not the trusted system path: %q", env)
	}
	if !strings.Contains(joined, "NoDefaultCurrentDirectoryInExePath=1") {
		t.Fatal("isolated git env is missing the Windows cwd-execution guard")
	}
}
