package chatwidget

import (
	"testing"
)

func TestExperimentalFeaturesViewUsesRegistryExperimentalStage(t *testing.T) {
	view := NewExperimentalFeaturesView(map[string]bool{
		"worktrees":     true,
		"network_proxy": false,
	})
	if view.Title != "Experimental Features" || len(view.Items) == 0 {
		t.Fatalf("view = %#v", view)
	}
	foundWorktrees := false
	foundMemories := false
	foundStable := false
	for _, item := range view.Items {
		if item.Key == "memories" {
			foundMemories = true
		}
		if item.Key == "worktrees" {
			foundWorktrees = true
		}
		if item.Key == "network_proxy" && (item.Name != "Network proxy" || item.Description != "Apply network proxy restrictions to sandboxed sessions that already have network access.") {
			t.Fatalf("network proxy item = %#v", item)
		}
		if item.Key == "plugins" {
			foundStable = true
		}
	}
	if foundMemories {
		t.Fatalf("stable memories feature should not be in experimental menu: %#v", view.Items)
	}
	// Rust #44870: worktrees is stable and enabled by default, so it is no
	// longer offered in the experimental menu.
	if foundWorktrees {
		t.Fatalf("stable worktrees feature should not be in experimental menu: %#v", view.Items)
	}
	if foundStable {
		t.Fatalf("stable feature should not be in experimental menu: %#v", view.Items)
	}
}
