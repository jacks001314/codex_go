package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/internal/appserver"
)

func TestFeedbackTitlePlaceholderAndClassificationMatchRust(t *testing.T) {
	cases := []struct {
		category       FeedbackChoice
		title          string
		placeholder    string
		classification string
	}{
		{FeedbackBug, "Tell us more (bug)", feedbackDefaultPlaceholder, "bug"},
		{FeedbackBadResult, "Tell us more (bad result)", feedbackDefaultPlaceholder, "bad_result"},
		{FeedbackGoodResult, "Tell us more (good result)", feedbackDefaultPlaceholder, "good_result"},
		{FeedbackSafetyCheck, "Tell us more (safety check)", feedbackSafetyCheckPlaceholder, "safety_check"},
		{FeedbackOther, "Tell us more (other)", feedbackDefaultPlaceholder, "other"},
	}
	for _, tc := range cases {
		title, placeholder := FeedbackTitleAndPlaceholder(tc.category)
		if title != tc.title || placeholder != tc.placeholder {
			t.Fatalf("title/placeholder for %s = %q/%q", tc.category, title, placeholder)
		}
		if got := FeedbackClassification(tc.category); got != tc.classification {
			t.Fatalf("classification for %s = %q", tc.category, got)
		}
	}
	if got := FeedbackClassification("unknown"); got != "other" {
		t.Fatalf("unknown classification = %q", got)
	}
}

func TestFeedbackNoteViewSubmitCancelPasteAndRowsMatchRustCore(t *testing.T) {
	view := NewFeedbackNoteView(FeedbackBug, "turn-123", true)
	if !view.Paste("  something broke  ") {
		t.Fatal("Paste should accept non-empty text")
	}
	submit, ok := view.HandleKey("enter")
	if !ok || submit.Category != FeedbackBug || submit.Reason == nil || *submit.Reason != "something broke" {
		t.Fatalf("submit = %#v ok=%v", submit, ok)
	}
	if submit.TurnID == nil || *submit.TurnID != "turn-123" || !submit.IncludeLogs || !view.Complete {
		t.Fatalf("submit metadata = %#v complete=%v", submit, view.Complete)
	}

	empty := NewFeedbackNoteView(FeedbackGoodResult, "", false)
	submit = empty.Submit()
	if submit.Reason != nil || submit.TurnID != nil || submit.IncludeLogs {
		t.Fatalf("empty submit = %#v", submit)
	}

	cancel := NewFeedbackNoteView(FeedbackOther, "", true)
	cancel.HandleKey("esc")
	if !cancel.Complete {
		t.Fatal("esc should complete/cancel feedback note view")
	}

	rows := NewFeedbackNoteView(FeedbackSafetyCheck, "", true).Rows(80)
	for _, want := range []string{
		feedbackGutter + "Tell us more (safety check)",
		feedbackGutter + feedbackSafetyCheckPlaceholder,
		StandardPopupHintLine,
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("rows missing %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
}

func TestFeedbackCategoryAndDisabledParamsMatchRust(t *testing.T) {
	params := FeedbackCategorySelectionParams()
	if params.Title != "How was this?" {
		t.Fatalf("title = %q", params.Title)
	}
	gotNames := make([]string, len(params.Items))
	for i, item := range params.Items {
		gotNames[i] = item.Name
		if !item.Dismiss || item.Description == "" {
			t.Fatalf("item missing dismiss/description: %#v", item)
		}
	}
	wantNames := []string{"bug", "bad result", "good result", "safety check", "other"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("feedback item order = %#v", gotNames)
	}

	disabled := FeedbackDisabledParams()
	if disabled.Title != "Sending feedback is disabled" || disabled.Subtitle == "" || disabled.FooterHint != StandardPopupHintLine {
		t.Fatalf("disabled params = %#v", disabled)
	}
	if len(disabled.Items) != 1 || disabled.Items[0].Name != "Close" || !disabled.Items[0].Dismiss {
		t.Fatalf("disabled item = %#v", disabled.Items)
	}
}

func TestFeedbackUploadConsentRowsDiagnosticsAndItemsMatchRust(t *testing.T) {
	diagnostics := appserver.NewFeedbackDiagnostics([]appserver.FeedbackDiagnostic{{
		Headline: "Proxy environment variables are set and may affect connectivity.",
		Details:  []string{"HTTP_PROXY = http://proxy.example.com:8080"},
	}})
	params := FeedbackUploadConsentParams(FeedbackUploadConsentOptions{
		Category:                  FeedbackBug,
		RolloutPath:               `C:\tmp\rollout.jsonl`,
		AutoReviewRolloutFilename: "auto-review-rollout.jsonl",
		IncludeWindowsSandboxLog:  true,
		Diagnostics:               diagnostics,
	})
	rows := params.HeaderLines
	rendered := strings.Join(rows, "\n")
	for _, want := range []string{
		"Upload logs?",
		"The following files will be sent:",
		"  \u2022 codex-logs.log",
		"  \u2022 codex-doctor-report.json",
		"  \u2022 windows-sandbox.log",
		"  \u2022 rollout.jsonl",
		"  \u2022 auto-review-rollout.jsonl",
		"  \u2022 codex-connectivity-diagnostics.txt",
		"Connectivity diagnostics",
		"  - Proxy environment variables are set and may affect connectivity.",
		"    - HTTP_PROXY = http://proxy.example.com:8080",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("consent rows missing %q:\n%s", want, rendered)
		}
	}
	if len(params.Items) != 2 || params.Items[0].Name != "Yes" || params.Items[1].Name != "No" {
		t.Fatalf("consent items = %#v", params.Items)
	}
	if params.Items[0].IncludeLogs == nil || !*params.Items[0].IncludeLogs || params.Items[1].IncludeLogs == nil || *params.Items[1].IncludeLogs {
		t.Fatalf("include log selections = %#v", params.Items)
	}
}

func TestFeedbackConnectivityDetailsHiddenForGoodResult(t *testing.T) {
	diagnostics := appserver.NewFeedbackDiagnostics([]appserver.FeedbackDiagnostic{{
		Headline: "Proxy environment variables are set and may affect connectivity.",
	}})
	if !ShouldShowFeedbackConnectivityDetails(FeedbackBug, diagnostics) {
		t.Fatal("bug feedback should show diagnostics")
	}
	if ShouldShowFeedbackConnectivityDetails(FeedbackGoodResult, diagnostics) {
		t.Fatal("good result feedback should hide diagnostics details")
	}
	rows := FeedbackUploadConsentHeaderRows(FeedbackUploadConsentOptions{
		Category:    FeedbackGoodResult,
		Diagnostics: diagnostics,
	})
	if !bottomPaneContainsRow(rows, "  \u2022 codex-connectivity-diagnostics.txt") {
		t.Fatalf("diagnostic attachment should still be listed:\n%s", strings.Join(rows, "\n"))
	}
	if bottomPaneContainsRow(rows, "Connectivity diagnostics") {
		t.Fatalf("diagnostic details should be hidden for good result:\n%s", strings.Join(rows, "\n"))
	}
}

func TestFeedbackUploadConsentPreservesAttachmentFilenamesMatchRust(t *testing.T) {
	rows := FeedbackUploadConsentHeaderRows(FeedbackUploadConsentOptions{
		Category:                  FeedbackBug,
		RolloutPath:               `C:\tmp\ rollout file.jsonl `,
		AutoReviewRolloutFilename: " auto-review.jsonl ",
	})
	for _, want := range []string{
		"  \u2022  rollout file.jsonl ",
		"  \u2022  auto-review.jsonl ",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("rows missing %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
}

func TestFeedbackIssueURLAndSuccessRowsMatchRust(t *testing.T) {
	external := FeedbackIssueURLForCategory(FeedbackBug, "thread-1", FeedbackAudienceExternal)
	wantExternal := "https://github.com/openai/codex/issues/new?template=3-cli.yml&steps=Uploaded%20thread:%20thread-1"
	if external != wantExternal {
		t.Fatalf("external issue url = %q", external)
	}
	if got := FeedbackIssueURLForCategory(FeedbackGoodResult, "thread-1", FeedbackAudienceExternal); got != "" {
		t.Fatalf("good result issue url = %q", got)
	}
	employeeRows := FeedbackSuccessRows(FeedbackBug, true, "thread-2", FeedbackAudienceOpenAIEmployee)
	for _, want := range []string{
		"\u2022 Feedback uploaded. Please report this in #codex-feedback:",
		"  http://go/codex-feedback-internal",
		"    https://go/codex-feedback/thread-2",
	} {
		if !bottomPaneContainsRow(employeeRows, want) {
			t.Fatalf("employee rows missing %q:\n%s", want, strings.Join(employeeRows, "\n"))
		}
	}
	goodRows := FeedbackSuccessRows(FeedbackGoodResult, false, "thread-3", FeedbackAudienceExternal)
	wantGood := []string{
		"\u2022 Feedback recorded (no logs). Thanks for the feedback!",
		"",
		"  Thread ID: thread-3",
	}
	if !reflect.DeepEqual(goodRows, wantGood) {
		t.Fatalf("good result rows = %#v", goodRows)
	}
}
