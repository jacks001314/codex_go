package tea

import (
	"strings"
	"testing"
)

func TestAnnotateComposerHyperlinksWrapsURLs(t *testing.T) {
	got := annotateComposerHyperlinks("> visit https://example.com/a?b=1 now")
	if !strings.Contains(got, "\x1b]8;;https://example.com/a?b=1\x07") {
		t.Fatalf("composer content did not wrap the URL in an OSC-8 hyperlink: %q", got)
	}
}

func TestAnnotateComposerHyperlinksNoURLIsUnchanged(t *testing.T) {
	input := "just plain text\nsecond line"
	if got := annotateComposerHyperlinks(input); got != input {
		t.Fatalf("no-URL content changed: %q", got)
	}
}
