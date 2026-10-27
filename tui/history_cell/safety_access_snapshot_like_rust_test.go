package historycell

import (
	"strings"
	"testing"

	"codex_go/tui"
)

// TestSafetyAccessBlockLinesMatchRustSnapshot locks the visible output of the
// safety block cells to Rust's snapshots in
// codex-rs/tui/src/app/tests/snapshots/...safety_recourse.snap. The snapshot
// renders the history cell at width 80.
func TestSafetyAccessBlockLinesMatchRustSnapshot(t *testing.T) {
	loading := NewCyberPolicyErrorEvent(tui.DaybreakNoticeApply)
	wantLoading := strings.Split(strings.Join([]string{
		"ⓘ This content can’t be shown",
		"  We take extra care with some cybersecurity requests. If you’re doing",
		"  authorized security work, apply for Daybreak to get broader access.",
		"  Learn more: https://help.openai.com/en/articles/20001326",
		"  Apply for Daybreak: https://openai.com/form/enterprise-trusted-access-for-",
		"  cyber/",
	}, "\n"), "\n")
	if got := loading.DisplayLines(80); strings.Join(got, "\n") != strings.Join(wantLoading, "\n") {
		t.Fatalf("loading block =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantLoading, "\n"))
	}

	explained := loading
	explained.Explanation = "We take extra caution with requests that could enable access to accounts or systems beyond permitted scope. We can still help with an overview of account security and recovery."
	wantExplained := strings.Split(strings.Join([]string{
		"ⓘ This content can’t be shown",
		"  We take extra caution with requests that could enable access to accounts or",
		"  systems beyond permitted scope. We can still help with an overview of",
		"  account security and recovery. If you’re doing authorized security work,",
		"  apply for Daybreak to get broader access.",
		"  Learn more: https://help.openai.com/en/articles/20001326",
		"  Apply for Daybreak: https://openai.com/form/enterprise-trusted-access-for-",
		"  cyber/",
	}, "\n"), "\n")
	if got := explained.DisplayLines(80); strings.Join(got, "\n") != strings.Join(wantExplained, "\n") {
		t.Fatalf("explained block =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantExplained, "\n"))
	}
}
