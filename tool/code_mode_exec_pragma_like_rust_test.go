package tool

import (
	"context"
	"strings"
	"testing"
)

// Mirrors codex_code_mode::parse_exec_source
// (code-mode-protocol/src/description.rs), including its model-facing error
// strings.
func TestParseCodeModeSourceLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantSource string
		wantYield  *int
		wantMax    *int
		wantErr    string
	}{
		{name: "without pragma", input: "text('hi')", wantSource: "text('hi')"},
		{
			name:       "with pragma",
			input:      "// @exec: {\"yield_time_ms\": 10}\ntext('hi')",
			wantSource: "text('hi')",
			wantYield:  intPtr(10),
		},
		{
			name:       "explicit zero stays zero",
			input:      "// @exec: {\"yield_time_ms\": 0, \"max_output_tokens\": 0}\ntext('hi')",
			wantSource: "text('hi')",
			wantYield:  intPtr(0),
			wantMax:    intPtr(0),
		},
		{
			name:       "leading whitespace before the pragma",
			input:      "  // @exec: {}\ntext('hi')",
			wantSource: "text('hi')",
		},
		{
			name:       "null fields are absent",
			input:      "// @exec: {\"yield_time_ms\": null, \"max_output_tokens\": null}\ntext('hi')",
			wantSource: "text('hi')",
		},
		{
			name:    "empty input",
			input:   "  \n\t",
			wantErr: "exec expects raw JavaScript source text (non-empty). Provide JS only, optionally with first-line `// @exec: {\"yield_time_ms\": 10000, \"max_output_tokens\": 1000}`.",
		},
		{
			name:    "pragma without a body",
			input:   "// @exec: {\"yield_time_ms\": 1}",
			wantErr: "exec pragma must be followed by JavaScript source on subsequent lines",
		},
		{
			name:    "blank body",
			input:   "// @exec: {}\n   \n",
			wantErr: "exec pragma must be followed by JavaScript source on subsequent lines",
		},
		{
			name:    "empty pragma",
			input:   "// @exec:\ntext('hi')",
			wantErr: "exec pragma must be a JSON object with supported fields `yield_time_ms` and `max_output_tokens`",
		},
		{
			name:    "non-object pragma",
			input:   "// @exec: [1]\ntext('hi')",
			wantErr: "exec pragma must be a JSON object with supported fields `yield_time_ms` and `max_output_tokens`",
		},
		{
			name:    "invalid JSON",
			input:   "// @exec: {\ntext('hi')",
			wantErr: "exec pragma must be valid JSON with supported fields `yield_time_ms` and `max_output_tokens`: ",
		},
		{
			name:    "unknown key",
			input:   "// @exec: {\"timeout_ms\": 1}\ntext('hi')",
			wantErr: "exec pragma only supports `yield_time_ms` and `max_output_tokens`; got `timeout_ms`",
		},
		{
			name:    "negative yield",
			input:   "// @exec: {\"yield_time_ms\": -1}\ntext('hi')",
			wantErr: "exec pragma fields `yield_time_ms` and `max_output_tokens` must be non-negative safe integers: invalid value: integer `-1`, expected u64",
		},
		{
			name:    "string yield",
			input:   "// @exec: {\"yield_time_ms\": \"10\"}\ntext('hi')",
			wantErr: "exec pragma fields `yield_time_ms` and `max_output_tokens` must be non-negative safe integers: invalid type: string, expected u64",
		},
		{
			name:    "yield above the JS safe integer",
			input:   "// @exec: {\"yield_time_ms\": 9007199254740992}\ntext('hi')",
			wantErr: "exec pragma field `yield_time_ms` must be a non-negative safe integer",
		},
		{
			name:    "budget above the JS safe integer",
			input:   "// @exec: {\"max_output_tokens\": 9007199254740992}\ntext('hi')",
			wantErr: "exec pragma field `max_output_tokens` must be a non-negative safe integer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, options, err := parseCodeModeSource(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("error = nil, want %q", tc.wantErr)
				}
				if !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want prefix %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if source != tc.wantSource {
				t.Fatalf("source = %q, want %q", source, tc.wantSource)
			}
			if !sameOptionalInt(options.YieldTimeMS, tc.wantYield) {
				t.Fatalf("yield = %v, want %v", options.YieldTimeMS, tc.wantYield)
			}
			if !sameOptionalInt(options.MaxOutputTokens, tc.wantMax) {
				t.Fatalf("max = %v, want %v", options.MaxOutputTokens, tc.wantMax)
			}
		})
	}
}

func sameOptionalInt(got, want *int) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

// The exec tool surfaces Rust's parse message unchanged (Rust's handler maps
// parse errors straight to RespondToModel, with no prefix).
func TestExecReportsThePragmaErrorUnchangedLikeRust(t *testing.T) {
	exec, _ := NewCodeModeExecutors(NewRegistry())
	_, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "pragma-error",
		Payload: Payload{Kind: PayloadCustom, Input: "// @exec: [1]\ntext('hi')"},
	})
	if err == nil {
		t.Fatal("exec accepted a non-object pragma")
	}
	want := "exec pragma must be a JSON object with supported fields `yield_time_ms` and `max_output_tokens`"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
