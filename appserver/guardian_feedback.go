package appserver

import (
	"encoding/json"
	"strings"
	"sync"

	"codex_go/state"
)

// Rust parity: codex-rs/feedback/src/guardian.rs (#41936). Failed Guardian
// reviews are retained in a bounded process-local store and exported only by a
// feedback upload that explicitly includes logs.
const (
	guardianFeedbackFilename       = "auto-review-failures.jsonl"
	guardianFeedbackContentType    = "application/x-ndjson"
	guardianFeedbackMaxRecords     = 64
	guardianFeedbackMaxPerThread   = 8
	guardianFeedbackMaxBytes       = 8 * 1024 * 1024
	guardianFeedbackMaxRecordBytes = 4 * 1024 * 1024
)

// guardianReviewFeedbackRecord mirrors Rust's ReviewFeedbackRecord.
type guardianReviewFeedbackRecord struct {
	ReviewedThreadID string            `json:"reviewed_thread_id"`
	ReviewedTurnID   string            `json:"reviewed_turn_id"`
	ReviewerThreadID string            `json:"reviewer_thread_id"`
	Model            string            `json:"model"`
	Status           string            `json:"status"`
	Decision         *string           `json:"decision,omitempty"`
	Action           string            `json:"action"`
	ActionTruncated  bool              `json:"action_truncated"`
	Instructions     *string           `json:"instructions,omitempty"`
	History          []json.RawMessage `json:"history"`
	ContextOmitted   bool              `json:"context_omitted"`
}

type guardianFeedbackStoredRecord struct {
	threadID string
	data     []byte
}

type guardianFeedbackRecords struct {
	mu      sync.Mutex
	records []guardianFeedbackStoredRecord
	bytes   int
}

var globalGuardianFeedbackRecords guardianFeedbackRecords

func (s *guardianFeedbackRecords) push(threadID string, record []byte) {
	if len(record)+1 > guardianFeedbackMaxBytes {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	perThread := 0
	oldestForThread := -1
	for index, stored := range s.records {
		if stored.threadID != threadID {
			continue
		}
		perThread++
		if oldestForThread < 0 {
			oldestForThread = index
		}
	}
	if perThread >= guardianFeedbackMaxPerThread && oldestForThread >= 0 {
		s.bytes -= len(s.records[oldestForThread].data) + 1
		s.records = append(s.records[:oldestForThread], s.records[oldestForThread+1:]...)
	}
	for len(s.records) >= guardianFeedbackMaxRecords || s.bytes+len(record)+1 > guardianFeedbackMaxBytes {
		if len(s.records) == 0 {
			return
		}
		s.bytes -= len(s.records[0].data) + 1
		s.records = s.records[1:]
	}
	s.bytes += len(record) + 1
	s.records = append(s.records, guardianFeedbackStoredRecord{threadID: threadID, data: record})
}

func (s *guardianFeedbackRecords) attachment(threadIDs []string) *FeedbackAttachment {
	if len(threadIDs) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(threadIDs))
	for _, threadID := range threadIDs {
		if threadID != "" {
			wanted[threadID] = struct{}{}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var buffer []byte
	for _, stored := range s.records {
		if _, ok := wanted[stored.threadID]; !ok {
			continue
		}
		buffer = append(buffer, stored.data...)
		buffer = append(buffer, '\n')
	}
	if len(buffer) == 0 {
		return nil
	}
	return &FeedbackAttachment{
		Filename:    guardianFeedbackFilename,
		ContentType: guardianFeedbackContentType,
		Buffer:      buffer,
	}
}

func recordGuardianReviewFailure(threadID string, record []byte) {
	globalGuardianFeedbackRecords.push(threadID, record)
}

// guardianReviewFailuresAttachment snapshots failures for the reported task
// tree without including other tasks' records.
func guardianReviewFailuresAttachment(threadIDs []string) *FeedbackAttachment {
	return globalGuardianFeedbackRecords.attachment(threadIDs)
}

func resetGuardianFeedbackRecordsForTest() {
	globalGuardianFeedbackRecords.mu.Lock()
	defer globalGuardianFeedbackRecords.mu.Unlock()
	globalGuardianFeedbackRecords.records = nil
	globalGuardianFeedbackRecords.bytes = 0
}

// guardianFeedbackCaptureEnabled mirrors Rust's review spawn guards (#41936):
// ephemeral threads and a managed feedback=enabled=false requirement retain
// nothing.
func (r *RuntimeRouter) guardianFeedbackCaptureEnabled(threadID string) bool {
	if r == nil {
		return false
	}
	if r.threadIsEphemeral(threadID) {
		return false
	}
	if r.services.Config != nil {
		if requirements := r.services.Config.Requirements(); requirements != nil && requirements.Requirements != nil {
			if feedback := requirements.Requirements.Feedback; feedback != nil && feedback.Enabled != nil {
				return *feedback.Enabled
			}
		}
	}
	return true
}

// guardianReviewFeedbackCapture holds the reviewer-side evidence recorded when
// the review ends without approval.
type guardianReviewFeedbackCapture struct {
	model        string
	instructions string
	history      []any
	decisionText string
}

func (c *guardianReviewFeedbackCapture) record(threadID, turnID string, attribution guardianReviewMetricAttribution, action state.Action) {
	status, decision := guardianFeedbackStatus(attribution, c.decisionText)
	if status == "" {
		return
	}
	formatted, truncated, err := state.FormatGuardianActionPretty(action)
	if err != nil {
		return
	}
	record := guardianReviewFeedbackRecord{
		ReviewedThreadID: threadID,
		ReviewedTurnID:   turnID,
		ReviewerThreadID: threadID,
		Model:            c.model,
		Status:           status,
		Decision:         decision,
		Action:           formatted,
		ActionTruncated:  truncated,
		History:          guardianFeedbackHistory(c.history),
	}
	if c.instructions != "" {
		instructions := c.instructions
		record.Instructions = &instructions
	}
	data, err := json.Marshal(record)
	if err == nil && len(data) <= guardianFeedbackMaxRecordBytes {
		recordGuardianReviewFailure(threadID, data)
		return
	}
	// Preserve the action and decision even when the optional reviewer context
	// is too large.
	record.Instructions = nil
	record.History = nil
	record.ContextOmitted = true
	data, err = json.Marshal(record)
	if err != nil || len(data) > guardianFeedbackMaxRecordBytes {
		return
	}
	recordGuardianReviewFailure(threadID, data)
}

func guardianFeedbackStatus(attribution guardianReviewMetricAttribution, decisionText string) (string, *string) {
	switch attribution.TerminalStatus {
	case "denied":
		return "denied", guardianFeedbackDecision(decisionText)
	case "timed_out":
		return "timed_out", nil
	case "aborted":
		return "aborted", nil
	case "failed_closed":
		if attribution.FailureReason == "parse_error" {
			return "invalid_decision", guardianFeedbackDecision(decisionText)
		}
		return "failed", nil
	case "", "approved":
		return "", nil
	default:
		return "failed", nil
	}
}

func guardianFeedbackDecision(decisionText string) *string {
	if decisionText == "" {
		return nil
	}
	value := decisionText
	return &value
}

func guardianFeedbackHistory(items []any) []json.RawMessage {
	if len(items) == 0 {
		return nil
	}
	history := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		data, err := json.Marshal(item)
		if err != nil {
			continue
		}
		history = append(history, data)
	}
	return history
}

// guardianFeedbackHistoryInput mirrors the reviewer session items the Go
// reviewer actually submitted: the prompt message followed by the review input
// items.
func guardianFeedbackHistoryInput(prompt string, items []any) []any {
	history := make([]any, 0, len(items)+1)
	if strings.TrimSpace(prompt) != "" {
		history = append(history, map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": prompt},
			},
		})
	}
	return append(history, items...)
}
