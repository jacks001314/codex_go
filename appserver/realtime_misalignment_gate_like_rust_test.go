package appserver

import (
	"errors"
	"testing"

	"codex_go/codexapi"
)

// Rust #43581: a misalignment failure retires only that thread's voice
// handoffs, and a later voice session starts with a fresh gate.
func TestRetiredVoiceHandoffsBlockUntilNextSessionLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	if router.voiceHandoffsRetired("thread-1") {
		t.Fatal("a fresh router must not block handoffs")
	}
	router.retireVoiceHandoffsForMisalignment("thread-1")
	if !router.voiceHandoffsRetired("thread-1") {
		t.Fatal("a misalignment violation must retire the thread's handoffs")
	}
	if router.voiceHandoffsRetired("thread-2") {
		t.Fatal("retirement leaked into another thread")
	}
	router.clearVoiceHandoffsRetired("thread-1")
	if router.voiceHandoffsRetired("thread-1") {
		t.Fatal("a new voice session must be able to hand off again")
	}
}

// The retired gate is per thread and ignores an empty thread id, so a malformed
// failure cannot block every conversation.
func TestRetiredVoiceHandoffsIgnoreEmptyThread(t *testing.T) {
	router := &RuntimeRouter{}
	router.retireVoiceHandoffsForMisalignment("   ")
	if router.voiceHandoffsRetired("") || router.voiceHandoffsRetired("thread-1") {
		t.Fatal("an empty thread id must not retire any handoffs")
	}
}

func TestIsMisalignmentPolicyErrorLikeRust(t *testing.T) {
	if !isMisalignmentPolicyError(&codexapi.APIError{Kind: codexapi.ErrorMisalignmentPolicyViolation}) {
		t.Fatal("the misalignment kind must be recognised")
	}
	if isMisalignmentPolicyError(&codexapi.APIError{Kind: codexapi.ErrorRateLimitExceeded}) {
		t.Fatal("an unrelated API error must not retire handoffs")
	}
	if isMisalignmentPolicyError(errors.New("plain failure")) {
		t.Fatal("a non-API error must not retire handoffs")
	}
	if isMisalignmentPolicyError(nil) {
		t.Fatal("nil must not retire handoffs")
	}
}
