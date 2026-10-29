package elevated

import (
	"errors"
	"testing"

	"codex_go/sandbox/windowssandbox/internal/setupenv"
)

// TestCommandOutcomeLikeRust ports `runner_metrics_tests.rs`.
func TestCommandOutcomeLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		exitCode int
		err      error
		want     string
	}{
		{name: "success", exitCode: 0, want: "success"},
		{name: "nonzero", exitCode: 2, want: "nonzero_exit"},
		{name: "transport", exitCode: 0, err: errors.New("pipe closed"), want: "transport_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandOutcome(tc.exitCode, tc.err); got != tc.want {
				t.Fatalf("commandOutcome(%d, %v) = %q, want %q", tc.exitCode, tc.err, got, tc.want)
			}
		})
	}
}

func TestRuntimeLabelMatchesSetupSelection(t *testing.T) {
	if got := runtimeLabel(setupenv.Legacy); got != "legacy" {
		t.Fatalf("runtimeLabel(legacy) = %q", got)
	}
	if got := runtimeLabel(setupenv.Registered); got != "registered" {
		t.Fatalf("runtimeLabel(registered) = %q", got)
	}
}
