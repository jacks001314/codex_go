package mentionsv2

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #61742: task mention suggestions show the Cloud marker without putting
// it into the title, search terms, or inserted mention text.
func TestTaskMentionCloudLabelLikeRust(t *testing.T) {
	candidate := TaskCandidate(codextui.TaskMention{ThreadID: "thread-cloud", Title: "Fix cache", Cloud: true})
	if candidate.DisplayName != "Fix cache" {
		t.Fatalf("display name = %q", candidate.DisplayName)
	}
	if candidate.Selection.InsertText != "@Fix cache" || candidate.Selection.Path != "thread://thread-cloud" {
		t.Fatalf("selection = %#v", candidate.Selection)
	}
	for _, term := range candidate.SearchTerms {
		if strings.Contains(term, "☁️") {
			t.Fatalf("search term gained the marker: %q", term)
		}
	}
	line := BuildLine(candidate.ToResult(nil, 0), false, 50, 20)
	if !strings.Contains(line, "☁️") || !strings.Contains(line, "Fix cache") {
		t.Fatalf("cloud task candidate line = %q", line)
	}
	if width := codextui.DisplayWidth(line); width > 50 {
		t.Fatalf("cloud task candidate width = %d, want <= 50", width)
	}

	local := TaskCandidate(codextui.TaskMention{ThreadID: "thread-local", Title: "Fix cache"})
	if line := BuildLine(local.ToResult(nil, 0), false, 50, 20); strings.Contains(line, "☁️") {
		t.Fatalf("local task candidate gained a marker: %q", line)
	}
}
