package execpolicy

import (
	"runtime"
	"testing"
)

// TestInjectSessionEnvAddsHarnessVersionLikeRust mirrors Rust #42395:
// CODEX_VERSION is set from the running harness version after the shell policy,
// replacing stale values (including case variants on Windows, where environment
// names are case-insensitive); an empty version clears it.
func TestInjectSessionEnvAddsHarnessVersionLikeRust(t *testing.T) {
	sessionID := "session-1"
	env := map[string]string{
		"CODEX_SESSION_ID": "stale-session",
		"CODEX_VERSION":    "old-version",
		"codex_version":    "case-variant",
	}
	InjectSessionEnv(env, &sessionID, "1.2.3")
	if env[SessionIDEnvVar] != "session-1" || env[VersionEnvVar] != "1.2.3" {
		t.Fatalf("injected env = %#v", env)
	}
	if runtime.GOOS == "windows" {
		if _, ok := env["codex_version"]; ok {
			t.Fatalf("case-variant version survived: %#v", env)
		}
	} else if env["codex_version"] != "case-variant" {
		t.Fatalf("Unix environment names are case-sensitive: %#v", env)
	}

	InjectSessionEnv(env, nil, "")
	if _, ok := env[SessionIDEnvVar]; ok {
		t.Fatalf("empty session did not clear CODEX_SESSION_ID: %#v", env)
	}
	if _, ok := env[VersionEnvVar]; ok {
		t.Fatalf("empty version did not clear CODEX_VERSION: %#v", env)
	}
}
