package appserver

import (
	"encoding/json"
	"testing"
)

// TestFeedbackReportUsesCustomTitleLikeRust covers Rust #42814: a non-blank
// feedback_title tag becomes the event message and exception type, is kept
// verbatim in the tags, and gives each report a unique fingerprint so shared
// titles do not merge submissions.
func TestFeedbackReportUsesCustomTitleLikeRust(t *testing.T) {
	snapshot := &FeedbackSnapshot{ThreadID: "thread-1"}
	rawTitle := " \t\u2003Workflow  feedback\nwith context\r\n "
	title := "Workflow  feedback\nwith context"
	tags := map[string]string{"feedback_title": rawTitle, "entrypoint": "task-picker"}
	reason := "  The\nworkflow\twas useful.  "

	var first feedbackReportEvent
	for index := 0; index < 2; index++ {
		report, err := snapshot.FeedbackReport("other", &reason, tags, nil)
		if err != nil {
			t.Fatalf("FeedbackReport: %v", err)
		}
		var event feedbackReportEvent
		if err := json.Unmarshal(report, &event); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if event.Message != title || event.Level != "info" {
			t.Fatalf("event message/level = %q/%q, want %q/info", event.Message, event.Level, title)
		}
		if event.Exception == nil || len(event.Exception.Values) != 1 ||
			event.Exception.Values[0].Type != title || event.Exception.Values[0].Value != reason {
			t.Fatalf("exception = %#v, want type %q and reason %q", event.Exception, title, reason)
		}
		if event.Tags["feedback_title"] != rawTitle || event.Tags["entrypoint"] != "task-picker" ||
			event.Tags["thread_id"] != "thread-1" || event.Tags["classification"] != "other" {
			t.Fatalf("tags = %#v", event.Tags)
		}
		if len(event.Fingerprint) != 1 || event.Fingerprint[0] != event.EventID {
			t.Fatalf("fingerprint = %#v, want the event id %q", event.Fingerprint, event.EventID)
		}
		if index == 0 {
			first = event
			continue
		}
		if first.EventID == event.EventID || first.Fingerprint[0] == event.Fingerprint[0] {
			t.Fatalf("independent reports shared identity: %q/%q", first.EventID, event.EventID)
		}
	}
}

// TestFeedbackReportFallsBackForBlankTitlesLikeRust covers the other half of
// Rust #42814: absent, empty, or whitespace-only titles keep the
// classification-based session title and leave fingerprinting to Sentry.
func TestFeedbackReportFallsBackForBlankTitlesLikeRust(t *testing.T) {
	snapshot := &FeedbackSnapshot{ThreadID: "thread-1"}
	reason := "  Feedback with context.  "
	for _, title := range []string{"", " \t\n\u2003 "} {
		report, err := snapshot.FeedbackReport("bug", &reason, map[string]string{"feedback_title": title}, nil)
		if err != nil {
			t.Fatalf("FeedbackReport: %v", err)
		}
		var event feedbackReportEvent
		if err := json.Unmarshal(report, &event); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		want := "[Bug]: Codex session thread-1"
		if event.Message != want || event.Level != "error" {
			t.Fatalf("event message/level = %q/%q, want %q/error", event.Message, event.Level, want)
		}
		if event.Exception == nil || len(event.Exception.Values) != 1 || event.Exception.Values[0].Type != want {
			t.Fatalf("exception = %#v, want type %q", event.Exception, want)
		}
		if len(event.Fingerprint) != 0 {
			t.Fatalf("blank title set fingerprint = %#v", event.Fingerprint)
		}
	}
}
