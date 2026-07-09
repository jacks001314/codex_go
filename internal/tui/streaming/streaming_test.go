package streaming

import (
	"strings"
	"testing"
	"time"
)

func durationPtr(value time.Duration) *time.Duration {
	return &value
}

func TestTableHoldbackScannerParity(t *testing.T) {
	if state := TableHoldbackStateFor("| A | B |\n"); state.Kind != TableHoldbackPendingHeader || state.SourceStart != 0 {
		t.Fatalf("pending state = %#v", state)
	}
	if state := TableHoldbackStateFor("| A | B |\n| --- | --- |\n"); state.Kind != TableHoldbackConfirmed || state.SourceStart != 0 {
		t.Fatalf("confirmed state = %#v", state)
	}
	if state := TableHoldbackStateFor("```sh\n| A | B |\n| --- | --- |\n```\n"); state.Kind != TableHoldbackNone {
		t.Fatalf("non-markdown fence state = %#v", state)
	}
	if state := TableHoldbackStateFor("```md\n| A | B |\n| --- | --- |\n```\n"); state.Kind != TableHoldbackConfirmed {
		t.Fatalf("markdown fence state = %#v", state)
	}
	if state := TableHoldbackStateFor("> ```sh\n> | A | B |\n> | --- | --- |\n> ```\n"); state.Kind != TableHoldbackNone {
		t.Fatalf("blockquote non-markdown fence state = %#v", state)
	}
}

func TestIncrementalHoldbackMatchesStatelessScan(t *testing.T) {
	chunks := []string{
		"status | owner\n",
		"\n",
		"> ```sh\n",
		"> | A | B |\n",
		"> | --- | --- |\n",
		"> ```\n",
		"> | Key | Value |\n",
		"> | --- | --- |\n",
	}
	scanner := NewTableHoldbackScanner()
	source := ""
	for _, chunk := range chunks {
		source += chunk
		scanner.PushSourceChunk(chunk)
		if got, want := scanner.State(), TableHoldbackStateFor(source); got != want {
			t.Fatalf("after %q got %#v want %#v\nsource:\n%s", chunk, got, want, source)
		}
	}
}

func TestAdaptiveChunkingPolicyParity(t *testing.T) {
	policy := NewAdaptiveChunkingPolicy()
	now := time.Unix(0, 0)
	decision := policy.Decide(QueueSnapshot{QueuedLines: 1, OldestAge: durationPtr(10 * time.Millisecond)}, now)
	if decision.Mode != ChunkingSmooth || decision.DrainPlan.Kind != DrainSingle {
		t.Fatalf("smooth decision = %#v", decision)
	}

	decision = policy.Decide(QueueSnapshot{QueuedLines: 8, OldestAge: durationPtr(10 * time.Millisecond)}, now)
	if decision.Mode != ChunkingCatchUp || !decision.EnteredCatchUp || decision.DrainPlan.Kind != DrainBatch || decision.DrainPlan.Limit != 8 {
		t.Fatalf("enter catch-up = %#v", decision)
	}

	decision = policy.Decide(QueueSnapshot{QueuedLines: 2, OldestAge: durationPtr(40 * time.Millisecond)}, now.Add(200*time.Millisecond))
	if decision.Mode != ChunkingCatchUp {
		t.Fatalf("pre-hold exit = %#v", decision)
	}
	decision = policy.Decide(QueueSnapshot{QueuedLines: 2, OldestAge: durationPtr(40 * time.Millisecond)}, now.Add(460*time.Millisecond))
	if decision.Mode != ChunkingSmooth || decision.DrainPlan.Kind != DrainSingle {
		t.Fatalf("post-hold exit = %#v", decision)
	}

	decision = policy.Decide(QueueSnapshot{QueuedLines: 8, OldestAge: durationPtr(20 * time.Millisecond)}, now.Add(500*time.Millisecond))
	if decision.Mode != ChunkingSmooth {
		t.Fatalf("reentry hold decision = %#v", decision)
	}
	decision = policy.Decide(QueueSnapshot{QueuedLines: 64, OldestAge: durationPtr(20 * time.Millisecond)}, now.Add(510*time.Millisecond))
	if decision.Mode != ChunkingCatchUp || decision.DrainPlan.Limit != 64 {
		t.Fatalf("severe reentry = %#v", decision)
	}
}

func TestStreamControllerTableHoldback(t *testing.T) {
	controller := NewStreamController(80)
	controller.core.now = func() time.Time { return time.Unix(0, 0) }

	controller.Push("Before table.\n")
	if controller.QueuedLines() != 1 {
		t.Fatalf("queued before = %d", controller.QueuedLines())
	}
	cell, idle := controller.OnCommitTick()
	if cell == nil || !idle || strings.Join(cell.RawLines(), "|") != "Before table." {
		t.Fatalf("first tick cell=%#v idle=%v", cell, idle)
	}

	controller.Push("| A | B |\n")
	if got := strings.Join(controller.CurrentTailLines(), "|"); got != "| A | B |" {
		t.Fatalf("pending header tail = %q", got)
	}
	controller.Push("| --- | --- |\n")
	controller.Push("| 1 | 2 |\n")
	if controller.QueuedLines() != 0 {
		t.Fatalf("table should stay out of stable queue, queued=%d", controller.QueuedLines())
	}
	if got := strings.Join(controller.CurrentTailLines(), "|"); got != "| A | B ||| --- | --- ||| 1 | 2 |" {
		t.Fatalf("confirmed table tail = %q", got)
	}

	final, source := controller.Finalize()
	if source != "Before table.\n| A | B |\n| --- | --- |\n| 1 | 2 |\n" {
		t.Fatalf("source = %q", source)
	}
	if final == nil || strings.Join(final.RawLines(), "|") != "| A | B ||| --- | --- ||| 1 | 2 |" {
		t.Fatalf("final cell = %#v", final)
	}
}

func TestStreamControllerSetWidthRebuildsQueuedLines(t *testing.T) {
	controller := NewStreamController(120)
	if !controller.Push("This is a long line that should wrap into multiple rows when resized.\n") {
		t.Fatal("expected initial long line to enqueue")
	}
	if controller.QueuedLines() != 1 {
		t.Fatalf("queued before resize = %d, want 1", controller.QueuedLines())
	}

	controller.SetWidth(24)
	cell, idle := controller.OnCommitTickBatch(64)
	if cell == nil {
		t.Fatal("expected resized queued cell")
	}
	lines := cell.RawLines()
	if len(lines) <= 1 {
		t.Fatalf("resized lines = %#v, want wrapped rows", lines)
	}
	if !strings.Contains(strings.Join(lines, " "), "resized") {
		t.Fatalf("resized lines lost content: %#v", lines)
	}
	if !idle {
		t.Fatal("expected queue to drain after batch")
	}
}

func TestStreamControllerSetWidthPartialDrainKeepsPendingQueue(t *testing.T) {
	controller := NewStreamController(40)
	controller.Push("AAAA BBBB CCCC DDDD EEEE FFFF GGGG HHHH IIII JJJJ\n")
	controller.Push("second line\n")

	cell, idle := controller.OnCommitTick()
	if cell == nil {
		t.Fatal("expected first emitted line")
	}
	if idle {
		t.Fatal("queue should still have pending lines")
	}
	if controller.QueuedLines() == 0 {
		t.Fatal("expected pending queued lines before resize")
	}

	controller.SetWidth(20)
	if controller.QueuedLines() == 0 {
		t.Fatal("resize must preserve pending queued lines")
	}

	drained := []string{}
	for i := 0; i < 64; i++ {
		cell, isIdle := controller.OnCommitTick()
		if cell != nil {
			drained = append(drained, cell.RawLines()...)
		}
		if isIdle {
			break
		}
	}
	if !strings.Contains(strings.Join(drained, " "), "second line") {
		t.Fatalf("pending lines should continue draining after resize: %#v", drained)
	}
}

func TestStreamControllerSetWidthAfterFirstLineEmitDoesNotRequeueFirstLine(t *testing.T) {
	controller := NewStreamController(120)
	controller.Push("FIRSTTOKEN contains enough words to wrap once the width is reduced dramatically.\n")
	controller.Push("second line remains pending\n")

	cell, _ := controller.OnCommitTick()
	if cell == nil {
		t.Fatal("expected first line emission")
	}

	controller.SetWidth(20)
	final, _ := controller.Finalize()
	remaining := []string{}
	if final != nil {
		remaining = final.RawLines()
	}
	if strings.Contains(strings.Join(remaining, " "), "FIRSTTOKEN") {
		t.Fatalf("first line should not be re-queued after resize: %#v", remaining)
	}
	if !strings.Contains(strings.Join(remaining, " "), "second line") {
		t.Fatalf("expected pending second line after resize: %#v", remaining)
	}
}

func TestStreamControllerSetWidthPartialWrappedEmitPreservesRemainder(t *testing.T) {
	controller := NewStreamController(18)
	controller.Push("alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu\n")

	cell, idle := controller.OnCommitTick()
	if cell == nil {
		t.Fatal("expected first wrapped line emission")
	}
	if idle {
		t.Fatal("expected queued wrapped remainder after one tick")
	}
	if controller.QueuedLines() == 0 {
		t.Fatal("expected queued wrapped remainder before resize")
	}

	controller.SetWidth(80)
	final, _ := controller.Finalize()
	remaining := []string{}
	if final != nil {
		remaining = final.RawLines()
	}
	joined := strings.Join(remaining, " ")
	if !strings.Contains(joined, "kappa") && !strings.Contains(joined, "lambda") && !strings.Contains(joined, "mu") {
		t.Fatalf("wrapped remainder was lost after resize: %#v", remaining)
	}
}

func TestStreamControllerSetWidthPreservesInFlightTail(t *testing.T) {
	controller := NewStreamController(80)
	controller.Push("tail without newline")
	controller.SetWidth(24)

	final, source := controller.Finalize()
	if source != "tail without newline" {
		t.Fatalf("source = %q, want tail without newline", source)
	}
	if final == nil || strings.Join(final.RawLines(), "|") != "tail without newline" {
		t.Fatalf("final cell = %#v", final)
	}
}

func TestRunCommitTickCatchUpDrainsBatch(t *testing.T) {
	controller := NewStreamController(80)
	now := time.Unix(0, 0)
	controller.core.now = func() time.Time { return now }
	for i := 0; i < 9; i++ {
		controller.Push("line\n")
	}
	policy := NewAdaptiveChunkingPolicy()
	output := RunCommitTick(policy, controller, nil, CommitTickAnyMode, now.Add(time.Second))
	if !output.HasController || !output.AllIdle || len(output.Cells) != 1 {
		t.Fatalf("output = %#v", output)
	}
	if got := len(output.Cells[0].RawLines()); got != 9 {
		t.Fatalf("drained lines = %d, want 9", got)
	}
	if policy.Mode() != ChunkingCatchUp {
		t.Fatalf("policy mode = %v", policy.Mode())
	}
}

func TestCommitTickCatchUpOnlySuppressesSmooth(t *testing.T) {
	controller := NewStreamController(80)
	now := time.Unix(0, 0)
	controller.core.now = func() time.Time { return now }
	controller.Push("one\n")
	policy := NewAdaptiveChunkingPolicy()
	output := RunCommitTick(policy, controller, nil, CommitTickCatchUpOnly, now)
	if output.HasController || len(output.Cells) != 0 || !output.AllIdle {
		t.Fatalf("suppressed output = %#v", output)
	}
	if controller.QueuedLines() != 1 {
		t.Fatalf("smooth queue should remain, got %d", controller.QueuedLines())
	}
}
