package features

import "testing"

func TestMCPSharedReplCwdIsStableAndEnabledLikeRust(t *testing.T) {
	const key = "mcp_shared_repl_cwd"
	if got := StageFor(key); got != StageStable {
		t.Fatalf("StageFor(%s) = %q, want %q", key, got, StageStable)
	}
	if !Defaults()[key] {
		t.Fatalf("Defaults()[%s] = false, want true", key)
	}
	if !Enabled(nil, key) {
		t.Fatalf("Enabled(nil, %s) = false, want true", key)
	}
	if Enabled(map[string]bool{key: false}, key) {
		t.Fatalf("explicit false did not disable %s", key)
	}
}
