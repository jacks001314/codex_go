package markdown

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

const markdownThreadLinkID = "019e60fc-0518-7c21-9596-980fe97225ba"

// TestRenderThreadLinkLabelsLikeRust covers Rust's markdown_destination arm for
// saved-conversation links: the label renders on its own and carries an OSC 8
// target the TUI can hit-test.
func TestRenderThreadLinkLabelsLikeRust(t *testing.T) {
	destination := "codex://threads/" + markdownThreadLinkID
	rendered, err := RenderWithTheme("See [Review notes]("+destination+") for details.", 80, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "\x1b]8;;"+destination+"\x07Review notes\x1b]8;;\x07") {
		t.Fatalf("thread link label missing its app target:\n%q", rendered)
	}
	visible := strings.TrimSpace(codextui.StripOSC8(utils.StripANSI(rendered)))
	if visible != "See Review notes for details." {
		t.Fatalf("visible thread link text = %q", visible)
	}
	if strings.Contains(visible, destination) {
		t.Fatalf("thread link destination leaked into the transcript: %q", visible)
	}
	if got, ok := codextui.ThreadIDFromLink(codextui.OSC8LinksInLine(rendered)[0].Destination); !ok || got != markdownThreadLinkID {
		t.Fatalf("rendered destination did not parse back to the thread id: %#v", codextui.OSC8LinksInLine(rendered))
	}
}

// TestRenderThreadLinkWithoutLabelKeepsDestinationVisibleLikeRust: a link with
// no visible label shows its destination (Rust appends the destination when the
// label is blank), and that text stays clickable.
func TestRenderThreadLinkWithoutLabelKeepsDestinationVisibleLikeRust(t *testing.T) {
	destination := "codex://threads/" + markdownThreadLinkID
	rendered, err := RenderWithTheme("See []("+destination+") now.", 80, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "\x1b]8;;"+destination+"\x07"+destination+"\x1b]8;;\x07") {
		t.Fatalf("label-less thread link is not clickable:\n%q", rendered)
	}
	visible := strings.TrimSpace(codextui.StripOSC8(utils.StripANSI(rendered)))
	if !strings.Contains(visible, destination) || !strings.HasPrefix(visible, "See") || !strings.HasSuffix(visible, "now.") {
		t.Fatalf("label-less visible text = %q", visible)
	}
}

// TestRenderWebLinksStillUseTerminalHyperlinksLikeRust guards the web arm that
// shares the same rewrite pass.
func TestRenderWebLinksStillUseTerminalHyperlinksLikeRust(t *testing.T) {
	rendered, err := RenderWithTheme("See [docs](https://example.com/docs) for details.", 80, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "\x1b]8;;https://example.com/docs\x07docs\x1b]8;;\x07") {
		t.Fatalf("web link target missing:\n%q", rendered)
	}
	if strings.Contains(rendered, "codex://threads/") {
		t.Fatalf("unexpected thread target:\n%q", rendered)
	}
}
