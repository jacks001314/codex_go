package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
)

const guardianDenyAssessment = `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"risky"}`

func parseGuardianFeedbackAttachment(t *testing.T, attachment *FeedbackAttachment) []guardianReviewFeedbackRecord {
	t.Helper()
	if attachment == nil {
		t.Fatal("guardian feedback attachment is nil")
	}
	if attachment.Filename != guardianFeedbackFilename || attachment.ContentType != guardianFeedbackContentType {
		t.Fatalf("attachment = %#v, want %s/%s", attachment, guardianFeedbackFilename, guardianFeedbackContentType)
	}
	var records []guardianReviewFeedbackRecord
	for _, line := range strings.Split(strings.TrimSpace(string(attachment.Buffer)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record guardianReviewFeedbackRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("record did not parse: %v (%q)", err, line)
		}
		records = append(records, record)
	}
	return records
}

// Mirrors Rust #41936: denied and invalid decisions are retained with the
// reviewed action, decision, model, instructions and reviewer history, while
// approved reviews are never captured.
func TestGuardianReviewFeedbackRecordsFailuresLikeRust(t *testing.T) {
	resetGuardianFeedbackRecordsForTest()
	t.Cleanup(resetGuardianFeedbackRecordsForTest)
	action := state.Action{Type: "command", Command: "rm -rf build", CWD: "/repo"}
	capture := &guardianReviewFeedbackCapture{
		model:        "review-model",
		instructions: "review instructions",
		history:      []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "evidence"}}}},
		decisionText: guardianDenyAssessment,
	}
	capture.record("thread-denied", "turn-1", guardianReviewMetricAttribution{TerminalStatus: "denied", FailureReason: "none"}, action)
	capture.record("thread-invalid", "turn-2", guardianReviewMetricAttribution{TerminalStatus: "failed_closed", FailureReason: "parse_error"}, action)
	capture.record("thread-approved", "turn-3", guardianReviewMetricAttribution{TerminalStatus: "approved"}, action)

	records := parseGuardianFeedbackAttachment(t, guardianReviewFailuresAttachment([]string{"thread-denied", "thread-invalid", "thread-approved"}))
	if len(records) != 2 {
		t.Fatalf("records = %#v, want denied and invalid_decision only", records)
	}
	denied := records[0]
	if denied.ReviewedThreadID != "thread-denied" || denied.Status != "denied" || denied.Model != "review-model" {
		t.Fatalf("denied record = %#v", denied)
	}
	if denied.Decision == nil || *denied.Decision != guardianDenyAssessment {
		t.Fatalf("denied decision = %#v, want the raw assessment", denied.Decision)
	}
	if denied.Action == "" || !strings.Contains(denied.Action, `"rm -rf build"`) || denied.ActionTruncated {
		t.Fatalf("denied action = %q truncated=%v", denied.Action, denied.ActionTruncated)
	}
	if denied.Instructions == nil || *denied.Instructions != "review instructions" || len(denied.History) != 1 {
		t.Fatalf("denied context = instructions:%v history:%d, want both retained", denied.Instructions, len(denied.History))
	}
	if records[1].Status != "invalid_decision" || records[1].ReviewedThreadID != "thread-invalid" {
		t.Fatalf("invalid record = %#v", records[1])
	}
	if guardianReviewFailuresAttachment([]string{"thread-approved"}) != nil {
		t.Fatal("an approved review produced a failure attachment")
	}
}

// Mirrors Rust's oversized-context fallback: the action and decision survive,
// the reviewer context is dropped whole, and context_omitted is explicit.
func TestGuardianReviewFeedbackOversizeOmitsContextLikeRust(t *testing.T) {
	resetGuardianFeedbackRecordsForTest()
	t.Cleanup(resetGuardianFeedbackRecordsForTest)
	capture := &guardianReviewFeedbackCapture{
		model:        "review-model",
		instructions: strings.Repeat("i", guardianFeedbackMaxRecordBytes),
		history:      []any{map[string]any{"type": "message", "text": strings.Repeat("h", guardianFeedbackMaxRecordBytes)}},
		decisionText: guardianDenyAssessment,
	}
	capture.record("thread-oversize", "turn-oversize", guardianReviewMetricAttribution{TerminalStatus: "denied"}, state.Action{Type: "command", Command: "ls", CWD: "/repo"})
	records := parseGuardianFeedbackAttachment(t, guardianReviewFailuresAttachment([]string{"thread-oversize"}))
	if len(records) != 1 {
		t.Fatalf("records = %#v, want one bounded record", records)
	}
	record := records[0]
	if !record.ContextOmitted || record.Instructions != nil || len(record.History) != 0 {
		t.Fatalf("oversized record = %#v, want context omitted", record)
	}
	if record.Action == "" || record.Decision == nil || record.Status != "denied" {
		t.Fatalf("oversized record lost the action/decision: %#v", record)
	}
}

// Mirrors Rust's bounded store: at most eight records per thread and 64
// process-wide.
func TestGuardianFeedbackStoreLimitsLikeRust(t *testing.T) {
	resetGuardianFeedbackRecordsForTest()
	t.Cleanup(resetGuardianFeedbackRecordsForTest)
	capture := &guardianReviewFeedbackCapture{decisionText: guardianDenyAssessment}
	action := state.Action{Type: "command", Command: "ls", CWD: "/repo"}
	for index := 0; index < 10; index++ {
		capture.record("thread-repeat", "turn", guardianReviewMetricAttribution{TerminalStatus: "denied"}, action)
	}
	if records := parseGuardianFeedbackAttachment(t, guardianReviewFailuresAttachment([]string{"thread-repeat"})); len(records) != guardianFeedbackMaxPerThread {
		t.Fatalf("per-thread records = %d, want %d", len(records), guardianFeedbackMaxPerThread)
	}
	threadIDs := make([]string, 0, 70)
	for index := 0; index < 70; index++ {
		threadID := "thread-" + strings.Repeat("x", index%3) + string(rune('a'+index%26)) + string(rune('a'+index/26))
		threadIDs = append(threadIDs, threadID)
		capture.record(threadID, "turn", guardianReviewMetricAttribution{TerminalStatus: "denied"}, action)
	}
	if records := parseGuardianFeedbackAttachment(t, guardianReviewFailuresAttachment(threadIDs)); len(records) > guardianFeedbackMaxRecords {
		t.Fatalf("process-wide records = %d, want at most %d", len(records), guardianFeedbackMaxRecords)
	}
}

// Mirrors the review integration: a denied Guardian review is captured, an
// approved one is not.
func TestGuardianReviewCapturesFailureEvidenceLikeRust(t *testing.T) {
	resetGuardianFeedbackRecordsForTest()
	t.Cleanup(resetGuardianFeedbackRecordsForTest)
	for _, testCase := range []struct {
		name       string
		message    string
		wantStatus string
	}{
		{name: "denied", message: guardianDenyAssessment, wantStatus: "denied"},
		{name: "approved", message: guardianDecisionDurationAssessment, wantStatus: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reviewer := &modelGuardianReviewer{
				agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
					return &model.AgentResponse{Message: testCase.message}, nil
				}),
				store: state.NewReviewStore(),
			}
			threadID := "thread-review-" + testCase.name
			_, _, err := reviewer.Review(context.Background(), threadID, "turn-review", "call-review", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
			if err != nil {
				t.Fatalf("review error = %v", err)
			}
			attachment := guardianReviewFailuresAttachment([]string{threadID})
			if testCase.wantStatus == "" {
				if attachment != nil {
					t.Fatalf("approved review produced an attachment: %#v", attachment)
				}
				return
			}
			records := parseGuardianFeedbackAttachment(t, attachment)
			if len(records) != 1 || records[0].Status != testCase.wantStatus || records[0].ReviewedTurnID != "turn-review" {
				t.Fatalf("records = %#v, want one %s record", records, testCase.wantStatus)
			}
			if len(records[0].History) == 0 {
				t.Fatal("reviewer history was not captured")
			}
		})
	}
}

// Mirrors Rust's opt-in export: only a feedback request with includeLogs gets
// the auto-review-failures.jsonl attachment.
func TestFeedbackUploadIncludesGuardianFailuresLikeRust(t *testing.T) {
	resetGuardianFeedbackRecordsForTest()
	t.Cleanup(resetGuardianFeedbackRecordsForTest)
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := "feedback-guardian-thread"
	now := time.Now().UTC()
	if err := store.Save(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  session.Metadata{CWD: home, ModelProvider: "openai"},
	}); err != nil {
		t.Fatalf("Save(%s) error = %v", threadID, err)
	}
	recordGuardianReviewFailure(threadID, []byte(`{"status":"denied","action":"{\"tool\":\"exec_command\"}"}`))
	snapshot := &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Feedback: snapshot})
	response := router.Handle(requestWithParams(t, IntID(1), MethodFeedbackUpload, FeedbackUploadParams{
		Classification: "bug",
		ThreadID:       stringPointerAppserver(threadID),
		IncludeLogs:    true,
	}))
	if response.Error != nil {
		t.Fatalf("feedback upload error = %+v", response.Error)
	}
	if attachment := feedbackAttachmentByName(snapshot.LastPrepared.Attachments, guardianFeedbackFilename); attachment == nil || !strings.Contains(string(attachment.Buffer), `"status":"denied"`) {
		t.Fatalf("attachments = %#v, want the guardian failure attachment", snapshot.LastPrepared.Attachments)
	}
	if attachment := feedbackAttachmentByName(snapshot.LastPrepared.Attachments, feedbackThreadIndexFilename); attachment == nil ||
		!strings.Contains(string(attachment.Buffer), `"thread_id":"feedback-guardian-thread"`) {
		t.Fatalf("attachments = %#v, want the feedback thread index", snapshot.LastPrepared.Attachments)
	}

	plain := &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)}
	plainRouter := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Feedback: plain})
	response = plainRouter.Handle(requestWithParams(t, IntID(2), MethodFeedbackUpload, FeedbackUploadParams{
		Classification: "bug",
		ThreadID:       stringPointerAppserver(threadID),
	}))
	if response.Error != nil {
		t.Fatalf("plain feedback upload error = %+v", response.Error)
	}
	if attachment := feedbackAttachmentByName(plain.LastPrepared.Attachments, guardianFeedbackFilename); attachment != nil {
		t.Fatalf("plain feedback upload carried the guardian attachment: %#v", attachment)
	}
	if attachment := feedbackAttachmentByName(plain.LastPrepared.Attachments, feedbackThreadIndexFilename); attachment != nil {
		t.Fatalf("plain feedback upload carried the thread index: %#v", attachment)
	}
}
