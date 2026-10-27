package status

import (
	"testing"

	"codex_go/auth"
)

// TestWorkspaceDisplayLikeRust mirrors Rust workspace_display (#57366).
func TestWorkspaceDisplayLikeRust(t *testing.T) {
	name := func(value string) *string { return &value }
	routing := func(id string) *auth.WorkspaceRouting {
		return &auth.WorkspaceRouting{ChatGPTAccountID: id}
	}
	cases := []struct {
		name    string
		value   *string
		routing *auth.WorkspaceRouting
		want    string
	}{
		{name: "name and id", value: name("Acme"), routing: routing("ws-1"), want: "Acme (ws-1)"},
		{name: "name trimmed", value: name("  Acme  "), routing: nil, want: "Acme"},
		{name: "id fallback", value: nil, routing: routing("ws-1"), want: "ws-1"},
		{name: "blank name falls back", value: name("   "), routing: routing("ws-1"), want: "ws-1"},
		{name: "control characters discard name", value: name("Acme\tInc"), routing: routing("ws-1"), want: "ws-1"},
		{name: "nothing known", value: nil, routing: nil, want: ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := WorkspaceDisplay(test.value, test.routing); got != test.want {
				t.Fatalf("WorkspaceDisplay(%v, %v) = %q, want %q", test.value, test.routing, got, test.want)
			}
		})
	}
}
