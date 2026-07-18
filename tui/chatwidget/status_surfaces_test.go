package chatwidget

import (
	"reflect"
	"testing"

	bottompane "codex_go/tui/bottom_pane"
)

func TestStatusSurfaceSelectionsDefaultsAliasesAndInvalids(t *testing.T) {
	selection := NewStatusSurfaceSelections(nil, nil)
	if !reflect.DeepEqual(selection.StatusLineItems, []bottompane.StatusLineItem{bottompane.StatusLineModelWithReasoning, bottompane.StatusLineCurrentDir}) {
		t.Fatalf("default status items = %#v", selection.StatusLineItems)
	}
	if !reflect.DeepEqual(selection.TerminalTitleItems, []TerminalTitleItem{TerminalTitleSpinner, TerminalTitleProject}) {
		t.Fatalf("default title items = %#v", selection.TerminalTitleItems)
	}

	selection = NewStatusSurfaceSelections(
		[]string{"model-name", "project", "status", "approval", "context-usage", "session-id", "bad", "bad"},
		[]string{"spinner", "project", "status", "thread", "context-usage", "model-name", "bad-title", "bad-title"},
	)
	wantStatus := []bottompane.StatusLineItem{
		bottompane.StatusLineModelName,
		bottompane.StatusLineProjectRoot,
		bottompane.StatusLineStatus,
		bottompane.StatusLineApprovalMode,
		bottompane.StatusLineContextUsed,
		bottompane.StatusLineSessionID,
	}
	if !reflect.DeepEqual(selection.StatusLineItems, wantStatus) || !reflect.DeepEqual(selection.InvalidStatusLineItems, []string{`"bad"`}) {
		t.Fatalf("status parse = %#v invalid=%#v", selection.StatusLineItems, selection.InvalidStatusLineItems)
	}
	wantTitle := []TerminalTitleItem{
		TerminalTitleSpinner,
		TerminalTitleProject,
		TerminalTitleStatus,
		TerminalTitleThread,
		TerminalTitleContextUsed,
		TerminalTitleModel,
	}
	if !reflect.DeepEqual(selection.TerminalTitleItems, wantTitle) || !reflect.DeepEqual(selection.InvalidTerminalTitleItems, []string{`"bad-title"`}) {
		t.Fatalf("title parse = %#v invalid=%#v", selection.TerminalTitleItems, selection.InvalidTerminalTitleItems)
	}
	if _, ok := ParseStatusLineItem(" model "); ok {
		t.Fatalf("status line item parsing should be exact like Rust")
	}
	if _, ok := ParseTerminalTitleItem(" spinner "); ok {
		t.Fatalf("terminal title item parsing should be exact like Rust")
	}
}

func TestStatusSurfaceSelectionsUsageFlags(t *testing.T) {
	selection := NewStatusSurfaceSelections(
		[]string{"pull-request-number", "workspace-headline"},
		[]string{"git-branch"},
	)
	if !selection.UsesGitBranch() || !selection.UsesGitSummary() || !selection.UsesWorkspaceHeadline() {
		t.Fatalf("usage flags = %#v", selection)
	}
}

func TestStatusSurfacePreviewDataPlaceholdersLiveAndSuppression(t *testing.T) {
	data := DefaultStatusSurfacePreviewData()
	if value, ok := data.ValueFor(StatusPreviewProjectName); !ok || value != "my-project" {
		t.Fatalf("project placeholder = %q ok=%v", value, ok)
	}

	data.SetLive(StatusPreviewProjectName, "codex_go")
	data.SetPlaceholder(StatusPreviewProjectName, "ignored")
	if value, ok := data.ValueFor(StatusPreviewProjectName); !ok || value != "codex_go" {
		t.Fatalf("live value overwritten = %q ok=%v", value, ok)
	}

	data.SuppressPlaceholder(StatusPreviewWeeklyLimit)
	if _, ok := data.ValueFor(StatusPreviewWeeklyLimit); ok {
		t.Fatal("placeholder should be suppressed")
	}
	data.SuppressPlaceholder(StatusPreviewProjectName)
	if value, ok := data.ValueFor(StatusPreviewProjectName); !ok || value != "codex_go" {
		t.Fatalf("live value suppressed = %q ok=%v", value, ok)
	}
}

func TestStatusSurfacePreviewRateLimitCopy(t *testing.T) {
	data := NewStatusSurfacePreviewData(map[StatusSurfacePreviewItem]string{
		StatusPreviewFiveHourLimit: "5h 42% left",
		StatusPreviewWeeklyLimit:   "\u00a0secondary usage 9% left",
	})
	if got := data.RateLimitItemName(StatusPreviewFiveHourLimit, "fallback"); got != "five-hour-limit" {
		t.Fatalf("five hour name = %q", got)
	}
	if got := data.RateLimitItemName(StatusPreviewWeeklyLimit, "fallback"); got != "secondary-usage-limit" {
		t.Fatalf("weekly name = %q", got)
	}
	if got := data.RateLimitItemDescription(StatusPreviewReasoning, "fallback"); got != "fallback" {
		t.Fatalf("fallback description = %q", got)
	}
}

func TestStatusLineForItemsUsesPreviewValues(t *testing.T) {
	data := NewStatusSurfacePreviewData(map[StatusSurfacePreviewItem]string{
		StatusPreviewModelWithReasoning: "gpt-5 high",
		StatusPreviewCurrentDir:         "/repo",
		StatusPreviewGitBranch:          "main",
	})
	line, ok := data.StatusLineForItems([]bottompane.StatusLineItem{
		bottompane.StatusLineModelWithReasoning,
		bottompane.StatusLineCurrentDir,
		bottompane.StatusLineGitBranch,
	}, true)
	if !ok || line.PlainText() != "gpt-5 high · /repo · main" {
		t.Fatalf("status line = %q ok=%v", line.PlainText(), ok)
	}
}

func TestTerminalTitleTextSeparatorsAndActionRequired(t *testing.T) {
	data := NewStatusSurfacePreviewData(map[StatusSurfacePreviewItem]string{
		StatusPreviewProjectName: "codex_go",
		StatusPreviewStatus:      "Working",
		StatusPreviewThreadTitle: "thread",
	})
	items := []TerminalTitleItem{TerminalTitleProject, TerminalTitleSpinner, TerminalTitleStatus, TerminalTitleThread}
	title, ok := TerminalTitleText(items, data, TerminalTitleRenderOptions{SpinnerText: "*"})
	if !ok || title != "codex_go * Working | thread" {
		t.Fatalf("title = %q ok=%v", title, ok)
	}

	title, ok = TerminalTitleText(items, data, TerminalTitleRenderOptions{ActionRequired: true})
	if !ok || title != "[ ! ] Action Required | codex_go | thread" {
		t.Fatalf("action title = %q ok=%v", title, ok)
	}

	preview, ok := PreviewLineForTitleItems(items, data)
	if !ok || preview != "[ ! ] Action Required | codex_go | Working | thread" {
		t.Fatalf("preview title = %q ok=%v", preview, ok)
	}
}

func TestTerminalTitleFrameAndTruncate(t *testing.T) {
	if TerminalTitleSpinnerFrame(10) != TerminalTitleSpinnerFrames[0] {
		t.Fatalf("spinner wrap = %q", TerminalTitleSpinnerFrame(10))
	}
	if got := TruncateTerminalTitlePart("abcdef", 5); got != "ab..." {
		t.Fatalf("truncate = %q", got)
	}
	if got := TruncateTerminalTitlePart("abcdef", 3); got != "abc" {
		t.Fatalf("small truncate = %q", got)
	}
	if got := TruncateTerminalTitlePart("e\u0301e\u0301e\u0301e\u0301e\u0301e\u0301", 5); got != "e\u0301e\u0301..." {
		t.Fatalf("grapheme truncate = %q", got)
	}
	if got := TruncateTerminalTitlePart("e\u0301e\u0301e\u0301e\u0301", 3); got != "e\u0301e\u0301e\u0301" {
		t.Fatalf("small grapheme truncate = %q", got)
	}
}
