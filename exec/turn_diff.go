package exec

import (
	"strings"

	"codex_go/runtimeutil"
	"codex_go/turn"
)

// execTurnDiffState mirrors the per-turn diff tracker that Rust's core updates
// on every applied patch (codex-rs/core/src/turn_diff_tracker.rs, driven from
// codex-rs/core/src/tools/events.rs:621-650) and that the Go app-server already
// ports for its `turn/diff/updated` notification
// (appserver/turn_runtime.go:4595-4625).
//
// `codex exec` drives the agent loop itself, so it owns the same tracker: every
// successfully applied patch feeds the applied changes, and the accumulated
// unified diff is handed to the human renderer, which reproduces Rust's
// EventProcessorWithHumanOutput::TurnDiffUpdated arm
// (codex-rs/exec/src/event_processor_with_human_output.rs:345-353).
type execTurnDiffState struct {
	tracker *runtimeutil.DiffTracker
}

func newExecTurnDiffState() *execTurnDiffState {
	return &execTurnDiffState{tracker: runtimeutil.NewDiffTracker()}
}

// trackExecution mirrors Rust's ToolEmitter::ApplyPatch tracker update plus its
// `should_emit_turn_diff` guard (codex-rs/core/src/tools/events.rs:621-650) and
// the app-server port's notifyTurnDiffFromSessionItem: the committed delta is
// tracked, then the accumulated diff is reported whenever the tracker changed
// and a diff existed before or exists now. The bool reports whether the
// producer emitted, so an empty diff still reaches the renderer and is
// remembered (Rust's "restored diff" case).
func (s *execTurnDiffState) trackExecution(execution *turn.ToolExecutionResult) (string, bool) {
	if s == nil || s.tracker == nil || execution == nil || execution.Output == nil {
		return "", false
	}
	if !isFileChangeExecution(execution) {
		return "", false
	}
	// Rust tracks the committed delta of failed patches too, but Go's
	// apply_patch executor publishes `appliedChanges` only on the success path,
	// so the exec producer follows the app-server port here.
	if fileChangeStatusFromToolOutput(execution.Output) != "completed" {
		return "", false
	}
	changes := execRuntimeFileChangesFromAny(firstNonNil(
		execution.Output.Data["appliedChanges"],
		execution.Output.Data["applied_changes"],
	))
	if len(changes) == 0 {
		return "", false
	}
	hadDiff := s.tracker.UnifiedDiff() != nil
	s.tracker.Track("", changes, true)
	diff := s.tracker.UnifiedDiff()
	if !hadDiff && diff == nil {
		return "", false
	}
	if diff == nil {
		return "", true
	}
	return *diff, true
}

// execRuntimeFileChangesFromAny converts an apply_patch `appliedChanges` payload
// into runtimeutil changes, mirroring the app-server converter that feeds its
// DiffTracker (appserver/turn_runtime.go:4667-4746).
func execRuntimeFileChangesFromAny(value any) []runtimeutil.FileChange {
	switch typed := value.(type) {
	case nil:
		return nil
	case []runtimeutil.FileChange:
		return append([]runtimeutil.FileChange(nil), typed...)
	case []map[string]any:
		changes := make([]runtimeutil.FileChange, 0, len(typed))
		for _, item := range typed {
			if change, ok := execRuntimeFileChangeFromMap(item); ok {
				changes = append(changes, change)
			}
		}
		return changes
	case []any:
		changes := make([]runtimeutil.FileChange, 0, len(typed))
		for _, raw := range typed {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if change, ok := execRuntimeFileChangeFromMap(item); ok {
				changes = append(changes, change)
			}
		}
		return changes
	default:
		return nil
	}
}

func execRuntimeFileChangeFromMap(change map[string]any) (runtimeutil.FileChange, bool) {
	if change == nil {
		return runtimeutil.FileChange{}, false
	}
	path := execFileChangeString(change, "path")
	if strings.TrimSpace(path) == "" {
		return runtimeutil.FileChange{}, false
	}
	out := runtimeutil.FileChange{
		Path:       path,
		OldContent: execFileChangeString(change, "oldContent", "old_content"),
		NewContent: execFileChangeString(change, "newContent", "new_content"),
	}
	switch strings.ToLower(strings.TrimSpace(execFileChangeString(change, "kind"))) {
	case "add":
		out.Kind = runtimeutil.ChangeAdd
	case "delete":
		out.Kind = runtimeutil.ChangeDelete
	default:
		out.Kind = runtimeutil.ChangeUpdate
	}
	if movePath := execFileChangeString(change, "movePath", "move_path"); strings.TrimSpace(movePath) != "" {
		out.MovePath = &movePath
	}
	if overwritten, ok := change["overwrittenContent"].(string); ok {
		out.OverwrittenContent = &overwritten
	} else if overwritten, ok := change["overwritten_content"].(string); ok {
		out.OverwrittenContent = &overwritten
	}
	return out, true
}

func execFileChangeString(change map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := change[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}
