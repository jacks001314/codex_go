package bottompane

import "testing"

// Rust #38282: the status-line and terminal-title pickers accept the
// thread-usage ids and show their shared preview placeholders.
func TestThreadUsageItemIDsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		id   string
		item StatusLineItem
	}{
		{id: "thread-credits", item: StatusLineThreadCredits},
		{id: "estimated-thread-cost", item: StatusLineEstimatedThreadCost},
		{id: "thread-name", item: StatusLineThreadName},
	} {
		item, ok := ParseStatusLineItem(tc.id)
		if !ok || item != tc.item {
			t.Fatalf("ParseStatusLineItem(%q) = %v ok=%v", tc.id, item, ok)
		}
		if got := StatusLineItemID(item); got != tc.id {
			t.Fatalf("StatusLineItemID(%v) = %q, want %q", item, got, tc.id)
		}
	}

	for _, tc := range []struct {
		id      string
		item    TerminalTitleItem
		preview StatusSurfacePreviewItem
	}{
		{id: "thread-credits", item: TerminalTitleThreadCredits, preview: StatusPreviewThreadCredits},
		{id: "estimated-thread-cost", item: TerminalTitleEstimatedThreadCost, preview: StatusPreviewEstimatedThreadCost},
		{id: "thread-name", item: TerminalTitleThreadName, preview: StatusPreviewThreadName},
	} {
		item, ok := ParseTerminalTitleItem(tc.id)
		if !ok || item != tc.item {
			t.Fatalf("ParseTerminalTitleItem(%q) = %v ok=%v", tc.id, item, ok)
		}
		preview, ok := item.PreviewItem()
		if !ok || preview != tc.preview {
			t.Fatalf("PreviewItem(%v) = %q ok=%v", item, preview, ok)
		}
	}
}

func TestThreadUsagePreviewPlaceholdersLikeRust(t *testing.T) {
	if got := StatusSurfacePreviewPlaceholder(StatusPreviewThreadCredits); got != "5.2 credits" {
		t.Fatalf("thread-credits placeholder = %q", got)
	}
	if got := StatusSurfacePreviewPlaceholder(StatusPreviewEstimatedThreadCost); got != "~$1.82" {
		t.Fatalf("estimated-thread-cost placeholder = %q", got)
	}
	if got := StatusSurfacePreviewPlaceholder(StatusPreviewThreadName); got != "thread name" {
		t.Fatalf("thread-name placeholder = %q", got)
	}
}
