package windowssandbox

import (
	"strings"
	"testing"
)

func TestSandboxLaunchEnvironmentRoundTripsUnicodeLikeRust(t *testing.T) {
	payload := strings.Repeat("alpha-世界-🙂-", 5000)
	env := map[string]string{
		"CODEX_SANDBOX_LAUNCH_999": "stale",
		"WORKLOAD":                 "preserved",
	}
	if err := EncodeSandboxLaunchPayload(payload, env); err != nil {
		t.Fatalf("EncodeSandboxLaunchPayload() error = %v", err)
	}
	if env["WORKLOAD"] != "preserved" {
		t.Fatalf("unrelated environment entry changed: %#v", env)
	}
	if _, ok := env["CODEX_SANDBOX_LAUNCH_999"]; ok {
		t.Fatal("stale transport entry survived encoding")
	}
	decoded, err := DecodeSandboxLaunchPayload(env)
	if err != nil {
		t.Fatalf("DecodeSandboxLaunchPayload() error = %v", err)
	}
	if decoded != payload {
		t.Fatalf("decoded payload mismatch: got %d bytes want %d", len(decoded), len(payload))
	}
	scrubbed, err := DecodeAndScrubSandboxLaunchPayload(env)
	if err != nil || scrubbed != payload {
		t.Fatalf("DecodeAndScrub() = %d, %v", len(scrubbed), err)
	}
	for key := range env {
		if IsSandboxLaunchEnvKey(key) {
			t.Fatalf("transport key survived scrub: %s", key)
		}
	}
}

func TestSandboxLaunchEnvironmentRejectsMalformedPayloadsLikeRust(t *testing.T) {
	for _, payload := range []string{"", "bad\x00value"} {
		if err := EncodeSandboxLaunchPayload(payload, map[string]string{}); err == nil {
			t.Fatalf("EncodeSandboxLaunchPayload(%q) succeeded", payload)
		}
	}
	env := map[string]string{
		"CODEX_SANDBOX_LAUNCH_COUNT": "2",
		"CODEX_SANDBOX_LAUNCH_BYTES": "4",
		"CODEX_SANDBOX_LAUNCH_0":     "abcd",
	}
	if _, err := DecodeSandboxLaunchPayload(env); err == nil {
		t.Fatal("missing chunk decoded successfully")
	}
}

func TestNeedsSandboxLaunchEnvironmentUsesUTF16LengthLikeRust(t *testing.T) {
	if NeedsSandboxLaunchEnvironment(strings.Repeat("a", 24_000)) {
		t.Fatal("24,000 UTF-16 units unexpectedly required environment transport")
	}
	if !NeedsSandboxLaunchEnvironment(strings.Repeat("a", 24_001)) {
		t.Fatal("24,001 UTF-16 units did not require environment transport")
	}
}
