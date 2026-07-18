package review

import (
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
	want := "\nReview comment:\n\n- [ ] Bug — /repo/a.go:10-12\n  details"
	if got != want {
		t.Fatalf("got = %q, want %q", got, want)
	}
}

func TestFormatFindingsBlockMatchesRustPlainAndBodyLines(t *testing.T) {
	findings := []Finding{{
		Title: "First",
		Body:  "line one\nline two\n",
		CodeLocation: CodeLocation{
			AbsoluteFilePath: "/repo/a.go",
			StartLine:        10,
			EndLine:          12,
		},
	}, {
		Title: "Second",
		CodeLocation: CodeLocation{
			AbsoluteFilePath: "/repo/b.go",
			StartLine:        3,
			EndLine:          4,
		},
	}}
	got := FormatFindingsBlock(findings, nil)
	want := "\nFull review comments:\n\n- First — /repo/a.go:10-12\n  line one\n  line two\n\n- Second — /repo/b.go:3-4"
	if got != want {
		t.Fatalf("got = %q, want %q", got, want)
	}
}

func TestRenderOutputText(t *testing.T) {
	if RenderOutputText(&OutputEvent{}) != FallbackMessage {
		t.Fatalf("expected fallback")
	}
	output := &OutputEvent{OverallExplanation: "summary", Findings: []Finding{{Title: "Bug", CodeLocation: CodeLocation{AbsoluteFilePath: "a.go", StartLine: 1}}}}
	got := RenderOutputText(output)
	want := "summary\n\nReview comment:\n\n- Bug — a.go:1-1"
	if got != want {
		t.Fatalf("got = %q, want %q", got, want)
	}
}

func TestParseOutputEventMatchesRustJSONAndSubstringFallback(t *testing.T) {
	text := `review result: {"findings":[{"title":"Bug","body":"details","confidence_score":0.7,"priority":1,"code_location":{"absolute_file_path":"/repo/a.go","line_range":{"start":10,"end":12}}}],"overall_correctness":"patch is incorrect","overall_explanation":"summary","overall_confidence_score":0.8} thanks`
	output := ParseOutputEvent(text)
	if output.OverallCorrectness != "patch is incorrect" || output.OverallExplanation != "summary" || output.OverallConfidenceScore != 0.8 {
		t.Fatalf("output = %#v", output)
	}
	if len(output.Findings) != 1 {
		t.Fatalf("findings = %#v", output.Findings)
	}
	finding := output.Findings[0]
	if finding.Title != "Bug" || finding.Body != "details" || finding.ConfidenceScore != 0.7 || finding.Priority != 1 {
		t.Fatalf("finding = %#v", finding)
	}
	if finding.CodeLocation.AbsoluteFilePath != "/repo/a.go" || finding.CodeLocation.StartLine != 10 || finding.CodeLocation.EndLine != 12 {
		t.Fatalf("location = %#v", finding.CodeLocation)
	}
}

func TestParseOutputEventFallsBackToPlainTextLikeRust(t *testing.T) {
	text := `{"findings":[],"overall_explanation":"missing required fields"}`
	output := ParseOutputEvent(text)
	if output.OverallExplanation != text || len(output.Findings) != 0 {
		t.Fatalf("fallback output = %#v", output)
	}
}

func TestReviewRolloutMessagesMatchRustTemplates(t *testing.T) {
	userMessage, assistantMessage := ReviewRolloutMessages(&OutputEvent{OverallExplanation: "Finding A\nFinding B"})
	wantUser := "<user_action>\n  <context>User initiated a review task. Here's the full review output from reviewer model. User may select one or more comments to resolve.</context>\n  <action>review</action>\n  <results>\n  Finding A\nFinding B\n  </results>\n  </user_action>\n"
	if userMessage != wantUser {
		t.Fatalf("user message = %q, want %q", userMessage, wantUser)
	}
	if assistantMessage != "Finding A\nFinding B" {
		t.Fatalf("assistant message = %q", assistantMessage)
	}
	if ReviewRolloutUserMessageID != "review_rollout_user" || ReviewRolloutAssistantMessageID != "review_rollout_assistant" {
		t.Fatalf("review rollout ids drifted")
	}

	interruptedUser, interruptedAssistant := ReviewRolloutMessages(nil)
	wantInterrupted := "<user_action>\n  <context>User initiated a review task, but was interrupted. If user asks about this, tell them to re-initiate a review with `/review` and wait for it to complete.</context>\n  <action>review</action>\n  <results>\n  None.\n  </results>\n</user_action>\n"
	if interruptedUser != wantInterrupted || interruptedAssistant != InterruptedMessage {
		t.Fatalf("interrupted user = %q assistant = %q", interruptedUser, interruptedAssistant)
	}
}
