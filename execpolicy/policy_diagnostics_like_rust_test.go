package execpolicy

import (
	"strings"
	"testing"
)

// The expectations below are byte-for-byte the stderr 0.166 prints for the same
// policy file; out/r73-execpolicy.txt and out/r73-edge.txt hold the side-by-side
// comparison this table was generated from.
func TestPolicyDiagnosticsMatchRust166(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "invalid decision",
			body: "prefix_rule(pattern=[\"ls\"], decision=\"maybe\")\n",
			want: "Error: failed to parse policy at PATH\n\nCaused by:\n    starlark error: Traceback (most recent call last):\n      * PATH:1, in <module>\n          prefix_rule(pattern=[\"ls\"], decision=\"maybe\")\n    error: invalid decision: maybe\n     --> PATH:1:1\n      |\n    1 | prefix_rule(pattern=[\"ls\"], decision=\"maybe\")\n      | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^\n      |\n    \n",
		},
		{
			name: "missing parameter",
			body: "prefix_rule(decision=\"allow\")\n",
			want: "Error: failed to parse policy at PATH\n\nCaused by:\n    starlark error: Traceback (most recent call last):\n      * PATH:1, in <module>\n          prefix_rule(decision=\"allow\")\n    error: Missing parameter `pattern` for call to `prefix_rule`\n     --> PATH:1:1\n      |\n    1 | prefix_rule(decision=\"allow\")\n      | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^\n      |\n    \n",
		},
		{
			name: "repeated named argument",
			body: "prefix_rule(pattern=[\"ls\"], pattern=[\"ls\"], decision=\"allow\")\n",
			want: "Error: failed to parse policy at PATH\n\nCaused by:\n    starlark error: error: repeated named argument\n     --> PATH:1:29\n      |\n    1 | prefix_rule(pattern=[\"ls\"], pattern=[\"ls\"], decision=\"allow\")\n      |                             ^^^^^^^\n      |\n    \n",
		},
		{
			name: "stray closing paren",
			body: "prefix_rule(pattern=[\"ls\"], decision=\"allow\"))\n",
			want: "Error: failed to parse policy at PATH\n\nCaused by:\n    starlark error: error: Parse error: unexpected symbol ')', expected new line\n     --> PATH:1:46\n      |\n    1 | prefix_rule(pattern=[\"ls\"], decision=\"allow\"))\n      |                                              ^\n      |\n    \n",
		},
		{
			name: "multiline frame",
			body: "prefix_rule(\n    pattern=[\"ls\", \"cat\"],\n    decision=\"maybe\",\n)\n",
			want: "Error: failed to parse policy at PATH\n\nCaused by:\n    starlark error: Traceback (most recent call last):\n      * PATH:1, in <module>\n          prefix_rule(\n    error: invalid decision: maybe\n     --> PATH:1:1\n      |\n    1 | / prefix_rule(\n    2 | |     pattern=[\"ls\", \"cat\"],\n    3 | |     decision=\"maybe\",\n    4 | | )\n      | |_^\n      |\n    \n",
		},
		{
			name: "unknown variable",
			body: "prefix_rules(pattern=[\"ls\"])\n",
			want: "Error: failed to parse policy at PATH\n\nCaused by:\n    starlark error: error: Variable `prefix_rules` not found, did you mean `prefix_rule`?\n     --> PATH:1:1\n      |\n    1 | prefix_rules(pattern=[\"ls\"])\n      | ^^^^^^^^^^^^\n      |\n    \n",
		},
		{
			name: "local before assignment",
			body: "prefix_rule(pattern = PATTERN)\nPATTERN = [\"git\"]\n",
			want: "Error: failed to parse policy at PATH\n\nCaused by:\n    starlark error: error: Local variable `PATTERN` referenced before assignment\n     --> PATH:1:23\n      |\n    1 | prefix_rule(pattern = PATTERN)\n      |                       ^^^^^^^\n      |\n    \n",
		},
		{
			name: "empty pattern",
			body: "prefix_rule(pattern=[])\n",
			want: "Error: failed to parse policy at PATH\n\nCaused by:\n    starlark error: Traceback (most recent call last):\n      * PATH:1, in <module>\n          prefix_rule(pattern=[])\n    error: invalid pattern element: pattern cannot be empty\n     --> PATH:1:1\n      |\n    1 | prefix_rule(pattern=[])\n      | ^^^^^^^^^^^^^^^^^^^^^^^\n      |\n    \n",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			path := writePolicy(t, tt.body)
			_, err := LoadPolicies([]string{path})
			if err == nil {
				t.Fatalf("LoadPolicies(%q) succeeded, want a diagnostic", tt.body)
			}
			want := strings.ReplaceAll(tt.want, "PATH", path)
			// The table holds the raw 0.166 stderr. "Error: " and the final newline are
			// added by main when the error escapes (cmd/internal/codexmain), the same
			// way anyhow renders it upstream, so rebuild the whole line here.
			if got := "Error: " + err.Error() + "\n"; got != want {
				t.Fatalf("diagnostic mismatch\n got: %q\nwant: %q", got, want)
			}
		})
	}
}
