package chatwidget

import (
	"reflect"
	"testing"

	"codex_go/internal/tui"
	historycell "codex_go/internal/tui/history_cell"
)

func TestToolLifecycleMcpActiveStartCompleteMatchesRust(t *testing.T) {
	state := ToolLifecycleState{}

	start := state.HandleMcpToolCallStartedNow(ToolLifecycleThreadItem{
		Kind:      ToolLifecycleThreadItemMcpToolCall,
		ID:        "mcp-1",
		Server:    "docs",
		Tool:      "lookup",
		Arguments: `{"q":"rust"}`,
	})
	if !start.Handled || state.ActiveCellKind() != ToolLifecycleActiveMcpToolCall || state.ActiveCellRevision != 1 {
		t.Fatalf("start = %#v state=%#v", start, state)
	}
	if !reflect.DeepEqual(start.Actions, []ToolLifecycleAction{
		ToolLifecycleActionRecordVisibleTurnActivity,
		ToolLifecycleActionFlushAnswerStream,
		ToolLifecycleActionBumpActiveCellRevision,
		ToolLifecycleActionRequestRedraw,
	}) {
		t.Fatalf("start actions = %#v", start.Actions)
	}

	done := state.HandleMcpToolCallCompletedNow(ToolLifecycleThreadItem{
		Kind:   ToolLifecycleThreadItemMcpToolCall,
		ID:     "mcp-1",
		Server: "docs",
		Tool:   "lookup",
		Result: []string{"first line\nsecond line"},
	})
	if !done.Handled || !done.HadWorkActivity || state.ActiveCellKind() != ToolLifecycleNoActiveCell {
		t.Fatalf("done = %#v state=%#v", done, state)
	}
	if len(done.History) != 1 || done.History[0].Kind != ToolLifecycleHistoryMcpToolCall {
		t.Fatalf("done history = %#v", done.History)
	}
	cell, ok := done.History[0].Cell.(historycell.McpToolCallCell)
	if !ok || cell.Result == nil || !reflect.DeepEqual(cell.Result.Content, []string{"first line\nsecond line"}) {
		t.Fatalf("mcp cell = %#v ok=%v", done.History[0].Cell, ok)
	}
	if len(state.Active) != 0 {
		t.Fatalf("active tools = %#v", state.Active)
	}
}

func TestToolLifecycleMcpCompletionWithoutActiveCreatesCompletedCellMatchRust(t *testing.T) {
	state := ToolLifecycleState{}
	done := state.HandleMcpToolCallCompletedNow(ToolLifecycleThreadItem{
		Kind:   ToolLifecycleThreadItemMcpToolCall,
		ID:     "mcp-2",
		Server: "docs",
		Tool:   "bad",
		Error:  "boom",
	})
	if len(done.History) != 1 || done.History[0].Kind != ToolLifecycleHistoryMcpToolCall {
		t.Fatalf("history = %#v", done.History)
	}
	cell := done.History[0].Cell.(historycell.McpToolCallCell)
	if cell.Result == nil || !cell.Result.IsError || cell.Result.Error != "boom" {
		t.Fatalf("cell = %#v", cell)
	}
}

func TestToolLifecycleWebSearchActiveAndFallbackMatchRust(t *testing.T) {
	state := ToolLifecycleState{}
	begin := state.OnWebSearchBegin("search-1")
	if !begin.RecordedVisibleTurnActivity || state.ActiveCellKind() != ToolLifecycleActiveWebSearchCall {
		t.Fatalf("begin = %#v state=%#v", begin, state)
	}

	end := state.OnWebSearchEnd("search-1", "rust tui", historycell.WebSearchAction{Kind: historycell.WebSearchActionSearch, Query: "rust tui"})
	if len(end.History) != 1 || end.History[0].Kind != ToolLifecycleHistoryWebSearch || !end.HadWorkActivity {
		t.Fatalf("end = %#v", end)
	}
	activeCell := end.History[0].Cell.(historycell.WebSearchCell)
	if !activeCell.Completed || activeCell.Query != "rust tui" {
		t.Fatalf("active search cell = %#v", activeCell)
	}

	fallback := state.OnWebSearchEnd("search-2", "docs", historycell.WebSearchAction{Kind: historycell.WebSearchActionOpenPage, URL: "https://example.test"})
	if len(fallback.History) != 1 || fallback.History[0].ID != "search-2" {
		t.Fatalf("fallback = %#v", fallback)
	}
	fallbackCell := fallback.History[0].Cell.(historycell.WebSearchCell)
	if !fallbackCell.Completed || fallbackCell.Action == nil || fallbackCell.Action.URL != "https://example.test" {
		t.Fatalf("fallback cell = %#v", fallbackCell)
	}
}

func TestToolLifecyclePatchImageAndQueuedRoutesMatchRust(t *testing.T) {
	state := ToolLifecycleState{}
	patch := state.OnPatchApplyBegin(map[string]tui.FileChange{
		"a.go": tui.NewAddFileChange("package main\n"),
	}, `D:\repo`)
	if len(patch.History) != 1 || patch.History[0].Kind != ToolLifecycleHistoryPatchEvent || !patch.RecordedVisibleTurnActivity {
		t.Fatalf("patch = %#v", patch)
	}

	failed := state.HandleQueuedItemCompletedNow(ToolLifecycleThreadItem{
		Kind:        ToolLifecycleThreadItemFileChange,
		ID:          "patch-1",
		PatchStatus: PatchApplyStatusFailed,
	})
	if len(failed.History) != 1 || failed.History[0].Kind != ToolLifecycleHistoryPatchFailure || !failed.HadWorkActivity {
		t.Fatalf("failed = %#v", failed)
	}

	imageBegin := state.OnImageGenerationBegin()
	if !reflect.DeepEqual(imageBegin.Actions, []ToolLifecycleAction{
		ToolLifecycleActionRecordVisibleTurnActivity,
		ToolLifecycleActionFlushAnswerStream,
	}) {
		t.Fatalf("image begin actions = %#v", imageBegin.Actions)
	}

	imageEnd := state.OnImageGenerationEnd("img-1", "completed", "a red cube", `D:\repo\out.png`)
	if len(imageEnd.History) != 1 || imageEnd.History[0].Kind != ToolLifecycleHistoryImageGeneration {
		t.Fatalf("image end = %#v", imageEnd)
	}
}
