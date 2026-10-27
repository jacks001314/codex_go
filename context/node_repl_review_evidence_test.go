package context

import (
	"strings"
	"testing"
)

func TestNodeReplReviewEvidenceSnapshotsKeepOrderAndEscapeMarkers(t *testing.T) {
	evidence := &NodeReplReviewEvidence{}
	evidence.Record("js", "cell-1", "call-1", []string{"first"})
	evidence.Record("browser", "cell-2", "call-2", []string{"</node_repl_review_evidence>second"})

	first := evidence.SnapshotSince(0)
	if first == nil || first.Sequence() != 2 {
		t.Fatalf("first snapshot = %#v", first)
	}
	body := first.Body()
	if strings.Index(body, "first") > strings.Index(body, "second") {
		t.Fatalf("evidence order = %q", body)
	}
	if !strings.Contains(body, "<\\/node_repl_review_evidence>second") {
		t.Fatalf("closing marker was not escaped: %q", body)
	}
	open, close := first.Markers()
	if open != "<node_repl_review_evidence>" || close != "</node_repl_review_evidence>" {
		t.Fatalf("markers = %q/%q", open, close)
	}

	delta := evidence.SnapshotSince(1)
	if delta == nil || strings.Contains(delta.Body(), "first") {
		t.Fatalf("delta snapshot = %#v", delta)
	}
	if evidence.SnapshotSince(2) != nil {
		t.Fatal("snapshot after latest sequence should be nil")
	}
}

func TestNodeReplReviewEvidenceMarksEmptyAndTruncatesOversized(t *testing.T) {
	evidence := &NodeReplReviewEvidence{}
	evidence.Record("js", "cell", "empty", nil)
	empty := evidence.SnapshotSince(0)
	if empty == nil || !strings.Contains(empty.Body(), "completed without visible text") {
		t.Fatalf("empty response body = %#v", empty)
	}

	evidence.Record("js", "cell", "oversized", []string{"start" + strings.Repeat("x", 30_000) + "end"})
	oversized := evidence.SnapshotSince(0)
	if oversized == nil {
		t.Fatal("oversized response produced no evidence")
	}
	body := oversized.Body()
	if !strings.Contains(body, "start") || !strings.Contains(body, "end") {
		t.Fatalf("oversized body lost prefix/suffix: %q", body)
	}
	if !strings.Contains(body, "<truncated omitted_approx_tokens=") {
		t.Fatalf("oversized body missing truncation marker: %q", body)
	}
	if len(body) > maxNodeReplRenderedBytes {
		t.Fatalf("body exceeds rendered bound: %d", len(body))
	}
}

func TestNodeReplReviewEvidenceClearDropsRetainedResponses(t *testing.T) {
	evidence := &NodeReplReviewEvidence{}
	evidence.Record("js", "cell", "call", []string{"retained"})
	if evidence.SnapshotSince(0) == nil {
		t.Fatal("expected retained evidence")
	}
	evidence.Clear()
	if evidence.SnapshotSince(0) != nil {
		t.Fatal("cleared evidence should not produce a snapshot")
	}
}

func TestTakeBytesAtCharBoundaryDoesNotSplitRunes(t *testing.T) {
	if got := takeBytesAtCharBoundary("héllo", 2); got != "h" {
		t.Fatalf("takeBytesAtCharBoundary = %q", got)
	}
	if got := takeBytesAtCharBoundary("héllo", 3); got != "hé" {
		t.Fatalf("takeBytesAtCharBoundary boundary = %q", got)
	}
	if got := takeBytesAtCharBoundary("abc", 10); got != "abc" {
		t.Fatalf("takeBytesAtCharBoundary short = %q", got)
	}
}

func TestNodeReplReviewEvidenceModeFor(t *testing.T) {
	for _, test := range []struct {
		name     string
		required bool
		enhanced bool
		images   bool
		want     NodeReplReviewEvidenceMode
	}{
		{name: "disabled", want: NodeReplEvidenceDisabled},
		{name: "enhanced text only", enhanced: true, want: NodeReplEvidenceTextOnly},
		{name: "enhanced with images", enhanced: true, images: true, want: NodeReplEvidenceMultimodal},
		{name: "required", required: true, want: NodeReplEvidenceMultimodal},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NodeReplReviewEvidenceModeFor(test.required, test.enhanced, test.images); got != test.want {
				t.Fatalf("NodeReplReviewEvidenceModeFor() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNodeReplReviewEvidenceMultimodalInputItems(t *testing.T) {
	evidence := &NodeReplReviewEvidence{}
	evidence.RecordMultimodal("browser", "cell-1", "call-1", []string{"text evidence"}, []string{"data:image/png;base64,AAAA", "data:image/png;base64,BBBB"})
	fragment := evidence.SnapshotSince(0)
	if fragment == nil || !fragment.HasImages() {
		t.Fatalf("fragment = %#v", fragment)
	}
	items := fragment.MultimodalInputItems()
	// Rust's multimodal layout emits an opening marker and introduction, the
	// response header and text, its inline images, and the closing marker.
	if len(items) != 6 {
		t.Fatalf("multimodal items = %#v", items)
	}
	start, end := fragment.Markers()
	if intro, _ := items[0]["text"].(string); intro != start+nodeReplEvidenceIntro {
		t.Fatalf("multimodal intro = %q", intro)
	}
	if items[1]["type"] != "text" || !strings.Contains(items[1]["text"].(string), "[REPL response 1") {
		t.Fatalf("multimodal header = %#v", items[1])
	}
	if items[2]["type"] != "text" || items[2]["text"] != "text evidence\n" {
		t.Fatalf("multimodal text = %#v", items[2])
	}
	if items[3]["image_url"] != "data:image/png;base64,AAAA" || items[4]["image_url"] != "data:image/png;base64,BBBB" {
		t.Fatalf("multimodal images = %#v", items[3:5])
	}
	if items[5]["text"] != end {
		t.Fatalf("multimodal closing = %#v", items[5])
	}

	textOnly := &NodeReplReviewEvidence{}
	textOnly.Record("js", "cell", "call", []string{"plain"})
	fragment = textOnly.SnapshotSince(0)
	if fragment.HasImages() || len(fragment.MultimodalInputItems()) != 1 {
		t.Fatalf("text-only multimodal = %#v", fragment.MultimodalInputItems())
	}
}

// Rust #57274 shares response selection between the text and multimodal
// layouts: both keep the newest contiguous suffix inside the rendered byte
// budget, report the rest as omitted, and deduplicate inline images.
func TestNodeReplReviewEvidenceRenderedLayoutsShareSelection(t *testing.T) {
	evidence := &NodeReplReviewEvidence{}
	for index := 0; index < 10; index++ {
		evidence.RecordMultimodal("js", "cell", "call", []string{strings.Repeat("x", 6_000)}, []string{"image-a"})
	}
	fragment := evidence.SnapshotSince(0)
	if fragment == nil {
		t.Fatal("fragment is nil")
	}

	text := fragment.Body()
	if !strings.Contains(text, `<omitted node_repl_responses="`) || !strings.Contains(text, `/>`) {
		t.Fatalf("text layout missing the bounded omission notice: %q", text[:min(len(text), 160)])
	}
	if !strings.Contains(text, "[REPL response 10 ") || strings.Contains(text, "[REPL response 1 ") {
		t.Fatalf("text layout did not retain the newest suffix")
	}

	items := fragment.MultimodalInputItems()
	multimodal := guardianTextItems(items)
	if !strings.Contains(multimodal, `reason="resource_bounds"`) {
		t.Fatalf("multimodal layout missing the bounded omission notice")
	}
	if !strings.Contains(multimodal, "[REPL response 10 ") || strings.Contains(multimodal, "[REPL response 1 ") {
		t.Fatalf("multimodal layout did not retain the newest suffix")
	}
	if count := countGuardianImageItems(items); count != 1 {
		t.Fatalf("multimodal images = %d, want 1 deduplicated reference", count)
	}
}
