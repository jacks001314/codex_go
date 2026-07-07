package review

import (
	"strings"
	"testing"
)

func TestFormatFindingsBlock(t *testing.T) {
	findings := []Finding{{
		Title: "Bug",
		Body:  "details",
		CodeLocation: CodeLocation{
			AbsoluteFilePath: "/repo/a.go",
			StartLine:        10,
			EndLine:          12,
		},
	}}
	got := FormatFindingsBlock(findings, []bool{false})
	if !strings.Contains(got, "- [ ] Bug - /repo/a.go:10-12") || !strings.Contains(got, "  details") {
		t.Fatalf("got = %q", got)
	}
}

func TestRenderOutputText(t *testing.T) {
	if RenderOutputText(&OutputEvent{}) != FallbackMessage {
		t.Fatalf("expected fallback")
	}
	output := &OutputEvent{OverallExplanation: "summary", Findings: []Finding{{Title: "Bug", CodeLocation: CodeLocation{AbsoluteFilePath: "a.go", StartLine: 1}}}}
	got := RenderOutputText(output)
	if !strings.Contains(got, "summary") || !strings.Contains(got, "Review comment:") {
		t.Fatalf("got = %q", got)
	}
}
