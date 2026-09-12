package agentsoverview

import (
	"strings"
	"testing"
)

// TestAgentsOverviewRendersPendingAttachments covers Rust #44027: the new-task
// prompt renders its pending image attachment labels.
func TestAgentsOverviewRendersPendingAttachments(t *testing.T) {
	view := New(nil, "", false)
	view.SetAttachments([]string{"  image: D:/tmp/chart.png", "  remote image: https://example.com/a.png"})
	rendered := strings.Join(view.RenderStyled(120, 30), "\n")
	if !strings.Contains(rendered, "image: D:/tmp/chart.png") {
		t.Fatalf("local image label missing:\n%s", rendered)
	}
	if !strings.Contains(rendered, "remote image: https://example.com/a.png") {
		t.Fatalf("remote image label missing:\n%s", rendered)
	}
	if !strings.Contains(rendered, "New task \u203a") {
		t.Fatalf("prompt missing:\n%s", rendered)
	}

	if labels := view.AttachmentLabels(); len(labels) != 2 {
		t.Fatalf("attachment labels = %#v", labels)
	}
	view.SetAttachments(nil)
	if labels := view.AttachmentLabels(); len(labels) != 0 {
		t.Fatalf("cleared labels = %#v", labels)
	}
}
