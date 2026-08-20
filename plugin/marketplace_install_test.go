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
			strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") ||
			name == "GIT_CONFIG_GLOBAL" || name == "GIT_CONFIG_SYSTEM" {
			t.Fatalf("scoped git config variable survived isolation: %s", name)
		}
	}
}
