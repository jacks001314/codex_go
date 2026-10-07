package tool

import (
	"path"
	"strings"

	"codex_go/applypatch"
	"codex_go/metrics"
)

// agentsMdEditMetricName is the metric Rust increments for committed apply_patch
// changes to AGENTS.md (Rust #51652). The process-global recorder
// (metrics.InstallGlobal, installed by otelinit.InstallGlobalMetrics) is the Go
// counterpart of the Rust `session_telemetry.counter` call here: Go's
// telemetry.SessionTelemetry carries no metrics client, so the process-global
// recorder is the shared metrics seam (as in rollout/, model/, state/ and
// appserver/).
const agentsMdEditMetricName = "codex.agents_md.edit"

// recordAgentsMdEditMetrics mirrors Rust's apply_patch telemetry (#51652): for
// every committed change it counts the change path and, for an update whose move
// destination differs, the move destination too. The basename matches
// `agents.md` / `agents.override.md` case-insensitively and the counter is tagged
// with the normalized (lowercase) basename. result carries the changes committed
// before a later patch failure, so those are counted as well.
func recordAgentsMdEditMetrics(result *applypatch.ApplyResult) {
	if result == nil {
		return
	}
	for _, change := range result.Changes {
		paths := []string{change.Path}
		if change.MovePath != "" && change.MovePath != change.Path {
			paths = append(paths, change.MovePath)
		}
		for _, changedPath := range paths {
			// Rust's `PathUri::basename` returns the last `/`-separated path
			// segment, so `path.Base` (not `filepath.Base`) mirrors it.
			filename := strings.ToLower(path.Base(changedPath))
			if filename == "agents.md" || filename == "agents.override.md" {
				metrics.Counter(agentsMdEditMetricName, 1, map[string]string{"filename": filename})
			}
		}
	}
}
