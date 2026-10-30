package markdown

import (
	"fmt"
	"strings"
	"testing"
)

// Mirrors Rust #42640 file_links_preserve_foreign_windows_paths_and_anchors:
// a file:// destination is parsed by inferred OS rather than the TUI host, so
// Windows drive and UNC paths shorten against their own form of the cwd and
// keep the line/column anchor (#L4C2 -> :4:2).
func TestFileLinksPreserveForeignWindowsPathsAndAnchorsLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		dest   string
		cwd    string
		expect string
	}{
		{
			name:   "windows drive path",
			dest:   "file:///C:/repo/Quarterly%20Report.xlsx",
			cwd:    "C:/repo",
			expect: "report (Quarterly Report.xlsx:4:2)",
		},
		{
			name:   "unc path",
			dest:   "file://server/share/Quarterly%20Report.xlsx",
			cwd:    "//server/share",
			expect: "report (Quarterly Report.xlsx:4:2)",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rendered := localLinkLabelRenderedText(t, fmt.Sprintf("[report](%s#L4C2)", testCase.dest), testCase.cwd)
			if got := strings.TrimSpace(rendered); got != testCase.expect {
				t.Fatalf("rendered = %q, want %q", got, testCase.expect)
			}
		})
	}
}
