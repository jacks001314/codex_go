package turn

import (
	"reflect"
	"testing"

	"codex_go/retainedctx"
)

func mcpAttributionSource(toolName string, firstTurnID string) retainedctx.McpAttributionSource {
	return retainedctx.McpAttributionSource{
		ServerName:  "example",
		ToolName:    toolName,
		FirstTurnID: firstTurnID,
	}
}

func mcpAttributionSourcePointer(value string) *string { return &value }

// Mirrors Rust's `records_the_first_turn_for_each_unique_source`: the identity
// excludes the turn, so a later turn for the same server/tool is ignored and the
// earliest observed turn is kept.
func TestMcpAttributionRecordsTheFirstTurnForEachUniqueSource(t *testing.T) {
	recorder := NewMcpAttributionRecorder(true, nil)
	recorder.Record(mcpAttributionSource("search", "turn_1"))
	recorder.Record(mcpAttributionSource("search", "turn_2"))
	recorder.Record(mcpAttributionSource("fetch", "turn_2"))

	want := retainedctx.McpAttribution{
		Status:  retainedctx.McpAttributionStatusComplete,
		Sources: []retainedctx.McpAttributionSource{mcpAttributionSource("search", "turn_1"), mcpAttributionSource("fetch", "turn_2")},
	}
	if got := recorder.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

// Mirrors Rust's `restores_cumulative_item_and_compaction_checkpoints`: response
// item checkpoints and compaction replacement-history checkpoints both restore,
// and the cumulative identity set is the union.
func TestMcpAttributionRestoresCumulativeItemAndCompactionCheckpoints(t *testing.T) {
	initial := retainedctx.McpAttribution{
		Status:  retainedctx.McpAttributionStatusComplete,
		Sources: []retainedctx.McpAttributionSource{mcpAttributionSource("search", "turn_1")},
	}
	cumulative := retainedctx.McpAttribution{
		Status: retainedctx.McpAttributionStatusComplete,
		Sources: []retainedctx.McpAttributionSource{
			mcpAttributionSource("search", "turn_1"),
			mcpAttributionSource("fetch", "turn_2"),
		},
	}
	recorder := NewMcpAttributionRecorder(false, []retainedctx.McpAttribution{initial, cumulative})
	if got := recorder.Snapshot(); !reflect.DeepEqual(got, cumulative) {
		t.Fatalf("snapshot = %#v, want %#v", got, cumulative)
	}
}

// Mirrors Rust's `legacy_or_conflicting_history_is_not_complete`: pre-attribution
// history cannot prove earlier context was MCP-free, and two checkpoints that
// disagree on a source's first turn are a conflict.
func TestMcpAttributionLegacyOrConflictingHistoryIsNotComplete(t *testing.T) {
	legacy := NewMcpAttributionRecorder(false, nil).Snapshot()
	wantLegacy := retainedctx.McpAttribution{
		Status:      retainedctx.McpAttributionStatusAttributionError,
		ErrorReason: mcpAttributionErrorReasonPointer(retainedctx.McpAttributionErrorHistoryMissingCheckpoint),
		Sources:     []retainedctx.McpAttributionSource{},
	}
	if !reflect.DeepEqual(legacy, wantLegacy) {
		t.Fatalf("legacy snapshot = %#v, want %#v", legacy, wantLegacy)
	}

	recorder := NewMcpAttributionRecorder(false, []retainedctx.McpAttribution{
		{Status: retainedctx.McpAttributionStatusComplete, Sources: []retainedctx.McpAttributionSource{mcpAttributionSource("search", "turn_1")}},
		{Status: retainedctx.McpAttributionStatusComplete, Sources: []retainedctx.McpAttributionSource{mcpAttributionSource("search", "turn_2")}},
	})
	want := retainedctx.McpAttribution{
		Status:      retainedctx.McpAttributionStatusAttributionError,
		ErrorReason: mcpAttributionErrorReasonPointer(retainedctx.McpAttributionErrorCheckpointSourceConflict),
		Sources:     []retainedctx.McpAttributionSource{mcpAttributionSource("search", "turn_1")},
	}
	if got := recorder.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("conflicting snapshot = %#v, want %#v", got, want)
	}
}

// Mirrors Rust's `first_error_reason_survives_checkpoints_and_later_sources`: the
// first error reason is retained across a checkpoint round trip and later
// invalid observations.
func TestMcpAttributionFirstErrorReasonSurvivesCheckpointsAndLaterSources(t *testing.T) {
	recorder := NewMcpAttributionRecorder(true, nil)
	recorder.Record(mcpAttributionSource("", "turn_1"))
	recorder.Record(mcpAttributionSource("search", "turn_2"))
	checkpoint, _, ok := recorder.Checkpoint(true)
	if !ok {
		t.Fatal("expected an error checkpoint")
	}
	restored := NewMcpAttributionRecorder(false, []retainedctx.McpAttribution{checkpoint})
	restored.Record(mcpAttributionSource("", "turn_3"))

	want := retainedctx.McpAttribution{
		Status:      retainedctx.McpAttributionStatusAttributionError,
		ErrorReason: mcpAttributionErrorReasonPointer(retainedctx.McpAttributionErrorSourceInvalid),
		Sources:     []retainedctx.McpAttributionSource{mcpAttributionSource("search", "turn_2")},
	}
	if got := restored.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored snapshot = %#v, want %#v", got, want)
	}
}

// Mirrors Rust's `restored_diagnostics_do_not_change_attribution_state`: an
// error reason carried by a non-error checkpoint is dropped, an error
// checkpoint without a reason becomes `restored_error_unknown`, and a complete
// checkpoint with no sources is invalid.
func TestMcpAttributionRestoredDiagnosticsDoNotChangeAttributionState(t *testing.T) {
	complete := NewMcpAttributionRecorder(false, []retainedctx.McpAttribution{{
		Status:      retainedctx.McpAttributionStatusComplete,
		ErrorReason: mcpAttributionErrorReasonPointer(retainedctx.McpAttributionErrorSourceInvalid),
		Sources:     []retainedctx.McpAttributionSource{mcpAttributionSource("search", "turn_1")},
	}}).Snapshot()
	wantComplete := retainedctx.McpAttribution{
		Status:  retainedctx.McpAttributionStatusComplete,
		Sources: []retainedctx.McpAttributionSource{mcpAttributionSource("search", "turn_1")},
	}
	if !reflect.DeepEqual(complete, wantComplete) {
		t.Fatalf("complete snapshot = %#v, want %#v", complete, wantComplete)
	}

	for _, testCase := range []struct {
		name       string
		checkpoint retainedctx.McpAttribution
		want       retainedctx.McpAttributionErrorReason
	}{
		{
			name:       "error without reason",
			checkpoint: retainedctx.McpAttribution{Status: retainedctx.McpAttributionStatusAttributionError},
			want:       retainedctx.McpAttributionErrorRestoredErrorUnknown,
		},
		{
			name:       "complete without sources",
			checkpoint: retainedctx.McpAttribution{Status: retainedctx.McpAttributionStatusComplete},
			want:       retainedctx.McpAttributionErrorCheckpointInvalid,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := NewMcpAttributionRecorder(false, []retainedctx.McpAttribution{testCase.checkpoint}).Snapshot()
			want := retainedctx.McpAttribution{
				Status:      retainedctx.McpAttributionStatusAttributionError,
				ErrorReason: mcpAttributionErrorReasonPointer(testCase.want),
				Sources:     []retainedctx.McpAttributionSource{},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot = %#v, want %#v", got, want)
			}
		})
	}
}

// Mirrors Rust's `acknowledging_an_older_checkpoint_does_not_clear_newer_changes`:
// an acknowledgement clamped to an older revision still leaves the newer change
// dirty, and a forced checkpoint always yields the current state.
func TestMcpAttributionAcknowledgingAnOlderCheckpointDoesNotClearNewerChanges(t *testing.T) {
	recorder := NewMcpAttributionRecorder(true, nil)
	_, initialRevision, ok := recorder.Checkpoint(false)
	if !ok {
		t.Fatal("expected the initial checkpoint")
	}
	recorder.Record(mcpAttributionSource("search", "turn_1"))
	recorder.MarkPersisted(initialRevision)

	_, latestRevision, ok := recorder.Checkpoint(false)
	if !ok {
		t.Fatal("expected a dirty checkpoint after the record")
	}
	recorder.MarkPersisted(latestRevision)
	if _, _, ok := recorder.Checkpoint(false); ok {
		t.Fatal("acknowledged checkpoint still reports dirty")
	}
	if _, _, ok := recorder.Checkpoint(true); !ok {
		t.Fatal("forced checkpoint should always yield the current state")
	}
}

// TestMcpAttributionSourcePointersCompareByValueLikeRust pins the dedup key: two
// sources with equal connector/plugin identifiers but distinct pointers are the
// same source.
func TestMcpAttributionSourcePointersCompareByValueLikeRust(t *testing.T) {
	recorder := NewMcpAttributionRecorder(true, nil)
	first := retainedctx.McpAttributionSource{
		ConnectorID: mcpAttributionSourcePointer("connector"),
		PluginID:    mcpAttributionSourcePointer("plugin"),
		ServerName:  "example",
		ToolName:    "search",
		FirstTurnID: "turn_1",
	}
	second := retainedctx.McpAttributionSource{
		ConnectorID: mcpAttributionSourcePointer("connector"),
		PluginID:    mcpAttributionSourcePointer("plugin"),
		ServerName:  "example",
		ToolName:    "search",
		FirstTurnID: "turn_2",
	}
	recorder.Record(first)
	recorder.Record(second)
	if got := recorder.Snapshot(); len(got.Sources) != 1 || got.Sources[0].FirstTurnID != "turn_1" {
		t.Fatalf("snapshot = %#v, want the single first source", got)
	}
}

// TestExecutedToolCallRecorderExposesMcpAttributionLikeRust pins the embedding:
// the call recorder carries the cumulative attribution and refuses to re-seed a
// history after recording began.
func TestExecutedToolCallRecorderExposesMcpAttributionLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.RecordMcpSource(mcpAttributionSource("search", "turn_1"))
	if got := recorder.McpAttributionSnapshot(); got.Status != retainedctx.McpAttributionStatusComplete {
		t.Fatalf("snapshot = %#v, want complete", got)
	}
	if _, _, ok := recorder.McpAttributionCheckpoint(false); !ok {
		t.Fatal("expected a dirty checkpoint after recording")
	}
	// A late history seed must not discard the recorded evidence.
	recorder.SeedMcpAttribution(false, []retainedctx.McpAttribution{{
		Status:  retainedctx.McpAttributionStatusComplete,
		Sources: []retainedctx.McpAttributionSource{mcpAttributionSource("fetch", "turn_9")},
	}})
	if got := recorder.McpAttributionSnapshot(); len(got.Sources) != 1 || got.Sources[0].ToolName != "search" {
		t.Fatalf("snapshot after late seed = %#v, want the recorded search source", got)
	}
}

func mcpAttributionErrorReasonPointer(reason retainedctx.McpAttributionErrorReason) *retainedctx.McpAttributionErrorReason {
	return &reason
}
