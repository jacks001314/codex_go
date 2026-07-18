package bottompane

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestScrollStateWrapNavigationAndVisibility(t *testing.T) {
	state := NewScrollState()
	length := 10
	visible := 5

	state.ClampSelection(length)
	state.EnsureVisible(length, visible)
	if !state.HasSelection || state.SelectedIdx != 0 || state.ScrollTop != 0 {
		t.Fatalf("initial clamp = %#v", state)
	}

	state.MoveUpWrap(length)
	state.EnsureVisible(length, visible)
	if state.SelectedIdx != length-1 || state.ScrollTop > state.SelectedIdx {
		t.Fatalf("up wrap = %#v", state)
	}

	state.MoveDownWrap(length)
	state.EnsureVisible(length, visible)
	if state.SelectedIdx != 0 || state.ScrollTop != 0 {
		t.Fatalf("down wrap = %#v", state)
	}
}

func TestScrollStatePageAndJumpNavigationClamp(t *testing.T) {
	state := NewScrollState()
	length := 10
	visible := 4

	state.ClampSelection(length)
	state.PageDownClamped(length, visible)
	if state.SelectedIdx != 4 || state.ScrollTop != 1 {
		t.Fatalf("page down 1 = %#v", state)
	}
	state.PageDownClamped(length, visible)
	if state.SelectedIdx != 8 || state.ScrollTop != 5 {
		t.Fatalf("page down 2 = %#v", state)
	}
	state.PageDownClamped(length, visible)
	if state.SelectedIdx != 9 || state.ScrollTop != 6 {
		t.Fatalf("page down edge = %#v", state)
	}
	state.PageUpClamped(length, visible)
	if state.SelectedIdx != 5 || state.ScrollTop != 5 {
		t.Fatalf("page up = %#v", state)
	}
	state.JumpTop(length, visible)
	if state.SelectedIdx != 0 || state.ScrollTop != 0 {
		t.Fatalf("jump top = %#v", state)
	}
	state.JumpBottom(length, visible)
	if state.SelectedIdx != 9 || state.ScrollTop != 6 {
		t.Fatalf("jump bottom = %#v", state)
	}
}

func TestPendingInputPreviewRendersQueues(t *testing.T) {
	preview := NewPendingInputPreview()
	if preview.DesiredHeight(40) != 0 {
		t.Fatalf("empty desired height = %d", preview.DesiredHeight(40))
	}

	preview.QueuedMessages = append(preview.QueuedMessages, "Hello, world!")
	lines := preview.RenderLines(40)
	if len(lines) != 3 {
		t.Fatalf("one queued message lines = %#v", lines)
	}
	if !strings.Contains(lines[0], "Queued follow-up inputs") ||
		!strings.Contains(lines[1], "Hello, world!") ||
		!strings.Contains(lines[2], "Alt+Up edit last queued message") {
		t.Fatalf("queued render lines = %#v", lines)
	}

	preview = NewPendingInputPreview()
	preview.SetEditBinding("Shift+Left")
	preview.QueuedMessages = []string{"Hello, world!"}
	if lines := preview.RenderLines(40); !strings.Contains(lines[2], "Shift+Left") {
		t.Fatalf("remapped edit binding lines = %#v", lines)
	}
}

func TestPendingInputPreviewKeepsLongURLIntact(t *testing.T) {
	preview := NewPendingInputPreview()
	preview.QueuedMessages = []string{
		"example.test/api/v1/projects/alpha-team/releases/2026-02-17/builds/1234567890/artifacts/reports/performance/summary/detail/session_id=abc123def456ghi789",
	}
	lines := preview.RenderLines(36)
	if len(lines) != 3 {
		t.Fatalf("URL preview line count = %d lines=%#v", len(lines), lines)
	}
	for _, line := range lines {
		if strings.Contains(line, "\u2026") {
			t.Fatalf("URL preview should not add ellipsis row: %#v", lines)
		}
	}
}

func TestPendingInputPreviewOrdersPendingRejectedAndQueued(t *testing.T) {
	preview := NewPendingInputPreview()
	preview.PendingSteers = []string{"Please continue."}
	preview.RejectedSteers = []string{"Retry later."}
	preview.QueuedMessages = []string{"Queued question"}
	lines := strings.Join(preview.RenderLines(80), "\n")

	pending := strings.Index(lines, "after next tool call")
	rejected := strings.Index(lines, "at end of turn")
	queued := strings.Index(lines, "Queued follow-up inputs")
	if pending < 0 || rejected < 0 || queued < 0 || !(pending < rejected && rejected < queued) {
		t.Fatalf("section order wrong:\n%s", lines)
	}
}

func TestPendingInputPreviewUsesRustLineSplitting(t *testing.T) {
	preview := NewPendingInputPreview()
	preview.QueuedMessages = []string{""}
	lines := preview.RenderLines(40)
	if len(lines) != 2 || !strings.Contains(lines[0], "Queued follow-up inputs") || !strings.Contains(lines[1], "edit last queued message") {
		t.Fatalf("empty message should render header and edit hint only, got %#v", lines)
	}

	preview.QueuedMessages = []string{"first\r\nsecond\n"}
	lines = preview.RenderLines(40)
	if len(lines) != 4 {
		t.Fatalf("CRLF/trailing newline lines = %#v", lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), "\r") {
		t.Fatalf("CRLF terminators should be stripped like Rust str::lines: %#v", lines)
	}
	if !strings.Contains(lines[1], "first") || !strings.Contains(lines[2], "second") {
		t.Fatalf("CRLF lines not preserved: %#v", lines)
	}
}

func TestPasteBurstASCIIHoldAndFlush(t *testing.T) {
	var burst PasteBurst
	t0 := time.Unix(0, 0)
	if got := burst.OnPlainChar('a', t0); got.Decision != CharDecisionRetainFirstChar {
		t.Fatalf("first char decision = %#v", got)
	}
	t1 := t0.Add(PasteBurstRecommendedFlushDelay() + time.Millisecond)
	if got := burst.FlushIfDue(t1); got.Kind != FlushTyped || got.Typed != 'a' {
		t.Fatalf("flush typed = %#v", got)
	}
	if burst.IsActive() {
		t.Fatal("burst should be inactive")
	}
}

func TestPasteBurstFastCharsBecomePaste(t *testing.T) {
	var burst PasteBurst
	t0 := time.Unix(0, 0)
	burst.OnPlainChar('a', t0)
	t1 := t0.Add(time.Millisecond)
	if got := burst.OnPlainChar('b', t1); got.Decision != CharDecisionBeginBufferFromPending {
		t.Fatalf("second char decision = %#v", got)
	}
	burst.AppendCharToBuffer('b', t1)

	t2 := t1.Add(PasteBurstRecommendedActiveFlushDelay() + time.Millisecond)
	if got := burst.FlushIfDue(t2); got.Kind != FlushPaste || got.Text != "ab" {
		t.Fatalf("flush paste = %#v", got)
	}
	if !burst.NewlineShouldInsertInsteadOfSubmit(t2) {
		t.Fatal("newline should still insert immediately after paste flush")
	}
	t3 := t1.Add(PasteEnterSuppressWindow + time.Millisecond)
	if burst.NewlineShouldInsertInsteadOfSubmit(t3) {
		t.Fatal("newline window should expire after paste window")
	}
}

func TestPasteBurstRetroGrab(t *testing.T) {
	var burst PasteBurst
	now := time.Unix(0, 0)
	if _, ok := burst.DecideBeginBuffer(now, "ab", 2); ok {
		t.Fatal("short prefix should not retro grab")
	}
	grab, ok := burst.DecideBeginBuffer(now, "a b", 2)
	if !ok || grab.StartByte != 1 || grab.Grabbed != " b" {
		t.Fatalf("retro grab = %#v ok=%v", grab, ok)
	}
	if !burst.NewlineShouldInsertInsteadOfSubmit(now) {
		t.Fatal("newline should insert while burst window active")
	}
}

func TestStatusLineFromSegments(t *testing.T) {
	line, ok := StatusLineFromSegments([]StatusLineSegment{
		{Item: StatusLineModelName, Text: "gpt-5"},
		{Item: StatusLineCurrentDir, Text: "/repo"},
		{Item: StatusLineGitBranch, Text: "main"},
	}, true)
	if !ok {
		t.Fatal("status line ok=false")
	}
	if got := line.PlainText(); got != "gpt-5 \u00b7 /repo \u00b7 main" {
		t.Fatalf("PlainText = %q", got)
	}
	if got := []StatusLineAccent{line.Spans[0].Accent, line.Spans[2].Accent, line.Spans[4].Accent}; !reflect.DeepEqual(got, []StatusLineAccent{
		StatusLineAccentModel,
		StatusLineAccentPath,
		StatusLineAccentBranch,
	}) {
		t.Fatalf("accents = %#v", got)
	}

	line, ok = StatusLineFromSegments([]StatusLineSegment{{Item: StatusLinePullRequestNumber, Text: "PR #42"}}, false)
	if !ok || !line.Spans[0].Dim || !line.Spans[0].Underline {
		t.Fatalf("PR status line = %#v ok=%v", line, ok)
	}
	if _, ok := StatusLineFromSegments(nil, true); ok {
		t.Fatal("empty status line ok=true")
	}
}
