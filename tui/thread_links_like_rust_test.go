package tui

import (
	"strings"
	"testing"
)

const threadLinkTestID = "019e60fc-0518-7c21-9596-980fe97225ba"

// TestThreadIDFromLinkOnlyAcceptsCanonicalLinksLikeRust mirrors
// codex-rs/tui/src/thread_links_tests.rs: only an exact
// `codex://threads/<canonical uuid>` destination is navigable.
func TestThreadIDFromLinkOnlyAcceptsCanonicalLinksLikeRust(t *testing.T) {
	if got, ok := ThreadIDFromLink("codex://threads/" + threadLinkTestID); !ok || got != threadLinkTestID {
		t.Fatalf("ThreadIDFromLink(valid) = %q, %v", got, ok)
	}
	for _, destination := range []string{
		"https://threads/" + threadLinkTestID,
		"codex://threads/" + threadLinkTestID + "/extra",
		"codex://threads/" + threadLinkTestID + "?other=1",
		"codex://threads/" + threadLinkTestID + "#fragment",
		"codex://threads/\x07" + threadLinkTestID,
		"codex://threads/not-a-thread",
		"codex://threads/" + strings.ToUpper(threadLinkTestID),
		"codex://threads/" + strings.ReplaceAll(threadLinkTestID, "-", ""),
		"codex://threads/urn:uuid:" + threadLinkTestID,
	} {
		if got, ok := ThreadIDFromLink(destination); ok {
			t.Fatalf("ThreadIDFromLink(%q) = %q, want rejected", destination, got)
		}
	}
}

// TestMarkdownDestinationKeepsThreadLinksLikeRust covers Rust's
// markdown_destination: saved-conversation links keep their own scheme and
// terminal links stay restricted to safe web URLs.
func TestMarkdownDestinationKeepsThreadLinksLikeRust(t *testing.T) {
	if got, ok := MarkdownDestination("codex://threads/" + threadLinkTestID); !ok || got != "codex://threads/"+threadLinkTestID {
		t.Fatalf("MarkdownDestination(thread link) = %q, %v", got, ok)
	}
	if _, ok := MarkdownDestination("codex://threads/not-a-thread"); ok {
		t.Fatal("MarkdownDestination accepted an invalid thread link")
	}
	if _, ok := MarkdownDestination("javascript:alert(1)"); ok {
		t.Fatal("MarkdownDestination accepted a non-web scheme")
	}
	if got, ok := MarkdownDestination("https://example.com/docs"); !ok || got != "https://example.com/docs" {
		t.Fatalf("MarkdownDestination(web) = %q, %v", got, ok)
	}
}

// TestStripThreadLinkHyperlinksKeepsLabelsAndWebLinksLikeRust verifies the frame
// never hands a saved-conversation target to the terminal while web links keep
// their OSC 8 destination.
func TestStripThreadLinkHyperlinksKeepsLabelsAndWebLinksLikeRust(t *testing.T) {
	line := "see " + "\x1b]8;;codex://threads/" + threadLinkTestID + "\x07Review notes\x1b]8;;\x07" +
		" and " + OSC8Hyperlink("https://example.com/docs", "docs")
	stripped := StripThreadLinkHyperlinks(line)
	if strings.Contains(stripped, "codex://threads/") {
		t.Fatalf("thread link reached the frame: %q", stripped)
	}
	if !strings.Contains(stripped, "Review notes") {
		t.Fatalf("thread link label dropped: %q", stripped)
	}
	if !strings.Contains(stripped, "\x1b]8;;https://example.com/docs\x07docs\x1b]8;;\x07") {
		t.Fatalf("web link lost its OSC 8 target: %q", stripped)
	}
	if links := OSC8LinksInLine(line); len(links) == 0 || links[0].Destination != "codex://threads/"+threadLinkTestID {
		t.Fatalf("app-side range missing before stripping: %#v", links)
	}
}
