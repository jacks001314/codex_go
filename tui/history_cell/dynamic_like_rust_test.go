package historycell

import (
	"fmt"
	"strings"
	"testing"

	"codex_go/tui"
)

// Rust #46710: the compact dynamic tool row keeps a bounded tail preview and
// reports the hidden line count, while the transcript keeps everything.
func TestDynamicToolCallCompactKeepsTailPreviewLikeRust(t *testing.T) {
	output := make([]string, 0, 10)
	for index := 1; index <= 10; index++ {
		output = append(output, fmt.Sprintf("line-%02d", index))
	}
	cell := DynamicToolCallCell{
		CallID:    "dyn-1",
		Name:      "task_create",
		Arguments: "{\"title\":\"demo\"}",
		Status:    "completed",
		Output:    output,
	}
	compact := strings.Join(cell.DisplayLines(80), "\n")
	for _, want := range []string{"\u2022 Called task_create", "\u2026 7 earlier lines hidden", "line-10"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact preview dropped %q:\n%s", want, compact)
		}
	}
	for _, hidden := range []string{"line-01", "line-07", "Arguments:"} {
		if strings.Contains(compact, hidden) {
			t.Fatalf("compact preview leaked %q:\n%s", hidden, compact)
		}
	}

	transcript := strings.Join(cell.TranscriptLines(80), "\n")
	for _, want := range []string{"Arguments: {\"title\":\"demo\"}", "line-01", "line-10"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript dropped %q:\n%s", want, transcript)
		}
	}

	raw := strings.Join(cell.RawLines(), "\n")
	for _, want := range []string{"Arguments: {\"title\":\"demo\"}", "line-01", "line-10"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("raw output dropped %q:\n%s", want, raw)
		}
	}
}

// A pending dynamic call has no output row yet; a completed one without a
// retained result reports that explicitly (Rust output_lines).
func TestDynamicToolCallReportsUnavailableOutputLikeRust(t *testing.T) {
	pending := DynamicToolCallCell{CallID: "dyn-2", Name: "task_create", Status: "in_progress"}
	if joined := strings.Join(pending.DisplayLines(80), "\n"); strings.Contains(joined, "output unavailable") {
		t.Fatalf("pending call rendered a result row:\n%s", joined)
	}
	done := DynamicToolCallCell{CallID: "dyn-3", Name: "task_create", Status: "completed"}
	if joined := strings.Join(done.DisplayLines(80), "\n"); !strings.Contains(joined, "(output unavailable)") {
		t.Fatalf("completed call without output = %q", joined)
	}
	empty := DynamicToolCallCell{CallID: "dyn-4", Name: "task_create", Status: "completed", Output: []string{}}
	if joined := strings.Join(empty.DisplayLines(80), "\n"); !strings.Contains(joined, "(no output)") {
		t.Fatalf("empty result = %q", joined)
	}
}

// The restored patch cell carries the persisted item identity (Rust
// PatchHistoryCell::with_activity_id) and exposes generic details.
func TestPatchHistoryCellCarriesActivityIDLikeRust(t *testing.T) {
	cell := NewPatchEvent(map[string]tui.FileChange{
		"src/main.rs": tui.NewUpdateFileChange("@@ -1 +1 @@\n-a\n+b\n", ""),
	}, "/workspace").WithActivityID("patch-9")
	ids := cell.ActivityIDs()
	if len(ids) != 1 || ids[0] != "patch:patch-9" {
		t.Fatalf("activity ids = %#v, want [patch:patch-9]", ids)
	}
	if disclosure := cell.ActivityDisclosure(80); !disclosure.Generic {
		t.Fatalf("patch disclosure = %#v, want generic", disclosure)
	}
}
