package bottompane

import (
	"reflect"
	"testing"
)

func TestStatusSurfacePreviewDataMatchesRustPlaceholdersAndRateLimitCopy(t *testing.T) {
	data := DefaultStatusSurfacePreviewData()
	if value, ok := data.ValueFor(StatusPreviewModel); !ok || value != "gpt-5.2-codex" {
		t.Fatalf("model placeholder = %q ok=%v", value, ok)
	}
	data.SetLive(StatusPreviewFiveHourLimit, "5h 24% left")
	if got := data.RateLimitItemName(StatusPreviewFiveHourLimit, "five-hour-limit"); got != "five-hour-limit" {
		t.Fatalf("rate name = %q", got)
	}
	if got := data.RateLimitItemDescription(StatusPreviewFiveHourLimit, "fallback"); got != "Remaining usage on the 5-hour usage limit (omitted when unavailable)" {
		t.Fatalf("rate desc = %q", got)
	}
	data.SetLive(StatusPreviewWeeklyLimit, "\u00a0weekly 10% left")
	if got := data.RateLimitItemName(StatusPreviewWeeklyLimit, "fallback"); got != "weekly-limit" {
		t.Fatalf("unicode-trimmed rate name = %q", got)
	}
	data.SuppressPlaceholder(StatusPreviewGitBranch)
	if _, ok := data.ValueFor(StatusPreviewGitBranch); ok {
		t.Fatalf("git branch placeholder should be suppressed")
	}
}

func TestStatusLineSetupParsingAndPreviewMatchesRust(t *testing.T) {
	data := NewStatusSurfacePreviewData(map[StatusSurfacePreviewItem]string{
		StatusPreviewModel:      "gpt-5",
		StatusPreviewCurrentDir: "/repo",
	})
	items, invalid := ParseStatusLineItems([]string{"model-name", "current-dir", "context-usage", "bad"})
	if !reflect.DeepEqual(invalid, []string{"bad"}) {
		t.Fatalf("invalid = %#v", invalid)
	}
	if _, ok := ParseStatusLineItem(" model "); ok {
		t.Fatalf("status line item parsing should be exact")
	}
	gotIDs := []string{}
	for _, item := range items {
		gotIDs = append(gotIDs, item.ID)
	}
	wantIDs := []string{"model", "current-dir", "context-used"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("ids = %#v, want %#v", gotIDs, wantIDs)
	}
	state := NewStatusLineSetupState([]string{"model", "current-dir"}, true, data)
	if state.PreviewText != "gpt-5 · /repo" {
		t.Fatalf("preview = %q", state.PreviewText)
	}
}

func TestTerminalTitleParsingAndPreviewMatchesRust(t *testing.T) {
	items, ok := ParseTerminalTitleItems([]string{"project", "spinner", "status", "thread"})
	if !ok {
		t.Fatalf("expected parse ok")
	}
	want := []TerminalTitleItem{TerminalTitleProject, TerminalTitleSpinner, TerminalTitleStatus, TerminalTitleThread}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("items = %#v, want %#v", items, want)
	}
	data := NewStatusSurfacePreviewData(map[StatusSurfacePreviewItem]string{
		StatusPreviewProjectName: "repo",
		StatusPreviewStatus:      "Working",
		StatusPreviewThreadTitle: "ship it",
	})
	preview, ok := PreviewLineForTitleItems(items, data)
	if !ok || preview != "[ ! ] Action Required | repo | Working | ship it" {
		t.Fatalf("preview = %q ok=%v", preview, ok)
	}
	items = []TerminalTitleItem{TerminalTitleProject, TerminalTitleStatus, TerminalTitleThread}
	preview, ok = PreviewLineForTitleItems(items, data)
	if !ok || preview != "repo | Working | ship it" {
		t.Fatalf("non-spinner preview = %q ok=%v", preview, ok)
	}
	if _, ok := ParseTerminalTitleItems([]string{"project", "bad"}); ok {
		t.Fatalf("invalid title ids should reject all")
	}
	if _, ok := ParseTerminalTitleItem(" project "); ok {
		t.Fatalf("terminal title parsing should be exact")
	}
}
