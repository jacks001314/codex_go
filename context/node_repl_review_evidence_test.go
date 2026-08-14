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
	if len(items) != 3 {
		t.Fatalf("multimodal items = %#v", items)
	}
	if items[0]["type"] != "text" || items[1]["type"] != "image" || items[2]["type"] != "image" {
		t.Fatalf("multimodal item types = %#v", items)
	}
	if !strings.Contains(items[0]["text"].(string), "text evidence") {
		t.Fatalf("multimodal text = %#v", items[0])
	}

	textOnly := &NodeReplReviewEvidence{}
	textOnly.Record("js", "cell", "call", []string{"plain"})
	fragment = textOnly.SnapshotSince(0)
	if fragment.HasImages() || len(fragment.MultimodalInputItems()) != 1 {
		t.Fatalf("text-only multimodal = %#v", fragment.MultimodalInputItems())
	}
}
