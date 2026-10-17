package context

import (
	"strings"
	"testing"
)

func TestDeferredToolsStateFragmentRendersInitialAndDeltaLikeRust(t *testing.T) {
	initial := Render(DeferredToolsStateFragment(map[string]string{
		"app":     "  control the Codex App  \nAdditional instructions.",
		"gmail":   "access your Google Gmail Account & labels",
		"hotline": "",
	}, nil, false))
	wantInitial := "<tools>\nDeferred tool namespaces:\n- app: control the Codex App\n- gmail: access your Google Gmail Account & labels\n- hotline\n</tools>"
	if initial == nil || initial.Role != RoleDeveloper || initial.Content != wantInitial {
		t.Fatalf("initial = %#v, want %q", initial, wantInitial)
	}

	delta := Render(DeferredToolsStateFragment(map[string]string{
		"app":   "control the Codex App",
		"gmail": "access your Google Gmail Account",
	}, map[string]string{
		"gmail":   "old Gmail description",
		"hotline": "access hotline information",
	}, true))
	wantDelta := "<tools>\nAdded deferred tool namespaces:\n- app: control the Codex App\n- gmail: access your Google Gmail Account\nRemoved deferred tool namespaces:\n- hotline: access hotline information\n</tools>"
	if delta == nil || delta.Content != wantDelta {
		t.Fatalf("delta = %#v, want %q", delta, wantDelta)
	}

	if got := DeferredToolsStateFragment(map[string]string{"app": "same"}, map[string]string{"app": "same"}, true); got != nil {
		t.Fatalf("unchanged fragment = %#v, want nil", got)
	}
	removed := Render(DeferredToolsStateFragment(nil, map[string]string{"app": "same"}, true))
	if removed == nil || !strings.Contains(removed.Content, "Removed deferred tool namespaces:\n- app: same") || !strings.Contains(removed.Content, "No deferred tool namespaces remain.") {
		t.Fatalf("removed = %#v", removed)
	}
}

func TestDeferredToolsStateFragmentBoundsDescriptionAndRenderedBytes(t *testing.T) {
	normalized := NormalizeDeferredToolNamespaces(map[string]string{"app": strings.Repeat("界", maxDeferredNamespaceDescriptionRunes+1)})
	if got := len([]rune(normalized["app"])); got != maxDeferredNamespaceDescriptionRunes {
		t.Fatalf("description rune count = %d", got)
	}

	namespaces := map[string]string{}
	for i := 0; i < 100; i++ {
		namespaces[strings.Repeat("n", 20)+string(rune('A'+i%26))+strings.Repeat("x", i)] = strings.Repeat("&", maxDeferredNamespaceDescriptionRunes)
	}
	rendered := Render(DeferredToolsStateFragment(namespaces, nil, false))
	if rendered == nil || len(rendered.Content) > maxDeferredToolsFragmentBytes {
		t.Fatalf("rendered bytes = %d, want <= %d", len(rendered.Content), maxDeferredToolsFragmentBytes)
	}
	if !strings.Contains(rendered.Content, "additional namespaces omitted") {
		t.Fatalf("rendered = %q, want omission marker", rendered.Content)
	}
}

// TestDeferredToolsFragmentReportMeasuresBeforeAndAfterLikeRust pins Rust
// #47873: sizes include the <tools> wrapper and the full entry text before the
// cap, while the after size counts only kept entries.
func TestDeferredToolsFragmentReportMeasuresBeforeAndAfterLikeRust(t *testing.T) {
	fragment, report := DeferredToolsStateFragmentWithReport(map[string]string{
		"app": "control the Codex App",
	}, nil, false)
	if fragment == nil || report == nil {
		t.Fatalf("fragment/report = %#v/%#v", fragment, report)
	}
	if !report.Snapshot {
		t.Fatal("absent previous state must be a snapshot")
	}
	wantBefore := len("<tools>") + len("</tools>") + 1 +
		len("Deferred tool namespaces") + len(":\n") +
		len("- ") + len("app") + len("\n") +
		len(": ") + len("control the Codex App")
	wantAfter := len("<tools>") + len(fragment.Body()) + len("</tools>")
	if report.BeforeNamespaces != 1 || report.AfterNamespaces != 1 {
		t.Fatalf("namespace counts = %d/%d", report.BeforeNamespaces, report.AfterNamespaces)
	}
	if report.BeforeBytes != wantBefore || report.AfterBytes != wantAfter {
		t.Fatalf("bytes = %d/%d, want %d/%d", report.BeforeBytes, report.AfterBytes, wantBefore, wantAfter)
	}

	_, delta := DeferredToolsStateFragmentWithReport(
		map[string]string{"app": "changed"},
		map[string]string{"app": "old"},
		true,
	)
	if delta == nil || delta.Snapshot {
		t.Fatalf("known previous state must be a delta: %#v", delta)
	}
	if fragment, report := DeferredToolsStateFragmentWithReport(map[string]string{"app": "same"}, map[string]string{"app": "same"}, true); fragment != nil || report != nil {
		t.Fatalf("unchanged state emitted %#v/%#v", fragment, report)
	}
}
