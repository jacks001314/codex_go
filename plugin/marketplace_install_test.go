package plugin

import (
	"strings"
	"testing"
)

func TestIsolatedPluginGitEnvStripsScopedConfigLikeRust(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "url.https://evil.invalid/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'url.https://evil.invalid/.insteadOf'")
	t.Setenv("GIT_CONFIG_GLOBAL", "/attacker/gitconfig")
	env := isolatedPluginGitEnv()
	for _, pair := range env {
		name := pair
		if idx := strings.IndexByte(pair, '='); idx >= 0 {
			name = pair[:idx]
		}
		if name == "GIT_CONFIG_COUNT" || name == "GIT_CONFIG_PARAMETERS" ||
			strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			t.Fatalf("scoped git config variable survived isolation: %s", name)
		}
		if pair == "GIT_CONFIG_GLOBAL=/attacker/gitconfig" {
			t.Fatal("attacker GIT_CONFIG_GLOBAL survived isolation")
		}
	}
	if !strings.Contains(strings.Join(env, "\x00"), "GIT_CONFIG_GLOBAL="+isolatedGitConfigPathValue) {
		t.Fatal("isolated GIT_CONFIG_GLOBAL was not set to the empty trusted config")
	}
}
