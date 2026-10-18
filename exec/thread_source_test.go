package exec

import (
	"testing"

	"codex_go/cli"
)

func TestExecThreadSourceAllowsCallerClassificationLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  *Request
		want string
	}{
		{name: "default", req: &Request{}, want: "user"},
		{name: "caller supplied", req: &Request{Exec: cli.ExecOptions{ThreadSource: "enterprise"}}, want: "enterprise"},
		{name: "trimmed", req: &Request{Exec: cli.ExecOptions{ThreadSource: "  custom  "}}, want: "custom"},
		{
			name: "subagent wins",
			req:  &Request{Exec: cli.ExecOptions{ThreadSource: "custom"}, subagent: &execSubagentContext{}},
			want: "subagent",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := execThreadSource(tc.req); got != tc.want {
				t.Fatalf("execThreadSource() = %q, want %q", got, tc.want)
			}
		})
	}
}
