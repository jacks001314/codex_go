package elevated

import (
	"codex_go/metrics"
	"codex_go/sandbox/windowssandbox/internal/setupenv"
)

// RunnerResultMetric compares registered and legacy runner outcomes at the
// local execution boundary (Rust `codex.windows_sandbox.runner_result`). Labels
// are fixed: never include command text, paths, or arbitrary error messages.
const RunnerResultMetric = "codex.windows_sandbox.runner_result"

func recordRunnerResult(phase string, outcome string) {
	metrics.Counter(RunnerResultMetric, 1, map[string]string{
		"runtime": runtimeLabel(setupenv.Current()),
		"phase":   phase,
		"outcome": outcome,
	})
}

func runtimeLabel(runtime setupenv.Runtime) string {
	if runtime.IsLegacy() {
		return "legacy"
	}
	return "registered"
}

// commandOutcome ports Rust's `command_outcome`.
func commandOutcome(exitCode int, err error) string {
	switch {
	case err != nil:
		return "transport_error"
	case exitCode == 0:
		return "success"
	default:
		return "nonzero_exit"
	}
}

// RecordCommandOutcome records the outcome of one runner command.
func RecordCommandOutcome(exitCode int, err error) {
	recordRunnerResult("command", commandOutcome(exitCode, err))
}
