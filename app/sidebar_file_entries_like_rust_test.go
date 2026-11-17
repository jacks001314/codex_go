package app

import (
	"strings"
	"testing"

	"codex_go/appserver"
)

// Rust parity: codex-rs/tui/src/app/sidebar_files.rs item_entries / resolve
// (#61920).

func sidebarTestItem(itemType, text string) appserver.ThreadItem {
	return appserver.ThreadItem{ID: "item-1", Type: itemType, Text: text}
}

func TestSidebarAgentMessageAndPlanEntriesLikeRust(t *testing.T) {
	item := sidebarTestItem("agentMessage", "See [a](docs/a.md) and [b](/workspace/repo/b.go:4:2).")
	entries := sidebarItemFileLinks("", item)
	if len(entries) != 2 || entries[0].Path != "docs/a.md" || entries[1].Path != "/workspace/repo/b.go" {
		t.Fatalf("agent message entries = %#v", entries)
	}
	if entries[1].Location == nil || entries[1].Location.Line != 4 || entries[1].Location.Column != 2 {
		t.Fatalf("b.go location = %#v", entries[1].Location)
	}
	plan := sidebarItemFileLinks("", sidebarTestItem("plan", "- Ship `docs/plan.md` via [plan](docs/plan.md)"))
	if len(plan) != 1 || plan[0].Path != "docs/plan.md" {
		t.Fatalf("plan entries = %#v", plan)
	}
}

func TestSidebarUserMessageSkipsNonTextAndDelegationLikeRust(t *testing.T) {
	item := appserver.ThreadItem{
		ID:   "item-user",
		Type: "userMessage",
		Content: []appserver.ThreadItemContent{
			{Type: "text", Text: "please read [notes](notes/readme.md)"},
			{Type: "image", ImageURL: "file:///workspace/repo/shot.png"},
		},
	}
	entries := sidebarItemFileLinks("", item)
	if len(entries) != 1 || entries[0].Path != "notes/readme.md" {
		t.Fatalf("user message entries = %#v", entries)
	}
	delegated := appserver.ThreadItem{
		ID:   "item-voice",
		Type: "userMessage",
		Content: []appserver.ThreadItemContent{{
			Type: "text",
			Text: "<realtime_delegation><input>see [a](a.md)</input></realtime_delegation>",
		}},
	}
	if entries := sidebarItemFileLinks("", delegated); len(entries) != 0 {
		t.Fatalf("delegation entries = %#v", entries)
	}
}

func TestSidebarFileChangeEntriesLikeRust(t *testing.T) {
	completed := appserver.ThreadItem{
		ID:     "item-patch",
		Type:   "fileChange",
		Status: "completed",
		Data: map[string]any{"changes": []any{
			map[string]any{"path": "src/added.go", "kind": "add"},
			map[string]any{"path": "src/old.go", "kind": map[string]any{"type": "update", "move_path": "src/new.go"}},
			map[string]any{"path": "src/touched.go", "kind": map[string]any{"type": "update"}},
			map[string]any{"path": "src/gone.go", "kind": "delete"},
		}},
	}
	entries := sidebarItemFileLinks("", completed)
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	if strings.Join(paths, ",") != "src/added.go,src/new.go,src/touched.go" {
		t.Fatalf("file change entries = %v", paths)
	}
	failed := completed
	failed.Status = "failed"
	if entries := sidebarItemFileLinks("", failed); len(entries) != 0 {
		t.Fatalf("incomplete patch entries = %#v", entries)
	}
}

func TestSidebarImageGenerationEntriesLikeRust(t *testing.T) {
	item := appserver.ThreadItem{
		ID:     "item-image",
		Type:   "imageGeneration",
		Status: "completed",
		Data:   map[string]any{"savedPath": "/workspace/repo/out/diagram.png"},
	}
	entries := sidebarItemFileLinks("", item)
	if len(entries) != 1 || entries[0].Path != "/workspace/repo/out/diagram.png" {
		t.Fatalf("image entries = %#v", entries)
	}
	pending := item
	pending.Status = "in_progress"
	if entries := sidebarItemFileLinks("", pending); len(entries) != 0 {
		t.Fatalf("pending image entries = %#v", entries)
	}
}

func TestSidebarEntriesResolveAgainstWorkspaceLikeRust(t *testing.T) {
	item := sidebarTestItem("agentMessage", strings.Join([]string{
		"[rel](docs/a.md)",
		"[abs](/workspace/repo/b.md)",
		"[host](file://other-host/workspace/c.md)",
		"[empty]()",
	}, " "))
	entries := sidebarItemFileLinks("/workspace/repo", item)
	if len(entries) != 2 {
		t.Fatalf("resolved entries = %#v", entries)
	}
	if entries[0].Path != "/workspace/repo/docs/a.md" || entries[1].Path != "/workspace/repo/b.md" {
		t.Fatalf("resolved paths = %q, %q", entries[0].Path, entries[1].Path)
	}
	unresolved := sidebarItemFileLinks("", item)
	if len(unresolved) < 2 || unresolved[0].Path != "docs/a.md" {
		t.Fatalf("empty workspace keeps raw paths: %#v", unresolved)
	}
}

func TestSidebarNewestItemFileLinksReversesLikeRust(t *testing.T) {
	item := sidebarTestItem("agentMessage", "[first](a.md) then [second](b.md)")
	newest := sidebarNewestItemFileLinks("", item)
	if len(newest) != 2 || newest[0].Path != "b.md" || newest[1].Path != "a.md" {
		t.Fatalf("newest entries = %#v", newest)
	}
}
