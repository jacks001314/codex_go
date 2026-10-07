package agent

import "testing"

// TestAgentPathMatchesPrefixLikeRust pins agent_matches_prefix
// (codex-rs/core/src/agent/control.rs): the root prefix matches everything,
// every other prefix matches itself or a `/`-bounded descendant.
func TestAgentPathMatchesPrefixLikeRust(t *testing.T) {
	cases := []struct {
		path   string
		prefix string
		want   bool
	}{
		{"/root", "/root", true},
		{"/root/work", "/root", true},
		{"/morpheus", "/root", true},
		{"/root/work", "/root/work", true},
		{"/root/work/nested", "/root/work", true},
		// Rust compares the raw suffix, so `/root/work` + `/er` is a
		// legitimate child path and does match.
		{"/root/work/er", "/root/work", true},
		// The sibling `/root/worker` must not match the prefix `/root/work`.
		{"/root/worker", "/root/work", false},
		{"/root/work", "/root/worker", false},
		{"/root/worker", "/root/work/er", false},
		{"/root/work", "/root/work/nested", false},
	}
	for _, tc := range cases {
		if got := AgentPath(tc.path).MatchesPrefix(AgentPath(tc.prefix)); got != tc.want {
			t.Errorf("AgentPath(%q).MatchesPrefix(%q) = %t, want %t", tc.path, tc.prefix, got, tc.want)
		}
	}
}
