package app

import (
	"testing"
)

// Rust parity: #43708 (95327467c3) user_verification_requests_tests.rs. A
// request may only start one verification attempt, and results produced for a
// cancelled, resolved, or dropped request are rejected as stale.
func TestUserVerificationRequestsPreventDuplicateAttemptsLikeRust(t *testing.T) {
	requests := &UserVerificationRequests{}
	params := UserVerificationParams{Title: "Verify", Description: "Approve deployment", Challenge: "AQID"}
	requests.NoteRequest("server-1", "request-1", params)

	attempt, ok := requests.Begin("server-1", "request-1")
	if !ok {
		t.Fatal("the first attempt was refused")
	}
	if attempt.ID == "" || attempt.Params != params {
		t.Fatalf("attempt = %#v", attempt)
	}
	if _, again := requests.Begin("server-1", "request-1"); again {
		t.Fatal("a duplicate attempt was started for the same request")
	}
	if !requests.IsPending("server-1", "request-1", attempt.ID) {
		t.Fatal("the running attempt is not pending")
	}
}

func TestUserVerificationRequestsRejectStaleAttemptsLikeRust(t *testing.T) {
	requests := &UserVerificationRequests{}
	requests.NoteRequest("server-1", "request-1", UserVerificationParams{Title: "Verify"})
	attempt, ok := requests.Begin("server-1", "request-1")
	if !ok {
		t.Fatal("attempt was refused")
	}

	requests.Remove("server-1", "request-1")
	if requests.IsPending("server-1", "request-1", attempt.ID) {
		t.Fatal("a removed request still accepted its result")
	}
	select {
	case <-attempt.Cancelled:
	default:
		t.Fatal("removing the request did not cancel the attempt")
	}
	if _, restarted := requests.Begin("server-1", "request-1"); restarted {
		t.Fatal("a resolved request could start another verification")
	}
}

// Rust clears the map on reconnect, which cancels every outstanding token.
func TestUserVerificationRequestsClearCancelsOutstandingLikeRust(t *testing.T) {
	requests := &UserVerificationRequests{}
	requests.NoteRequest("server-1", "request-1", UserVerificationParams{Title: "Verify"})
	requests.NoteRequest("server-1", "request-2", UserVerificationParams{Title: "Verify again"})
	first, ok := requests.Begin("server-1", "request-1")
	if !ok {
		t.Fatal("first attempt was refused")
	}

	requests.Clear()
	for _, attempt := range []UserVerificationAttempt{first} {
		if requests.IsPending("server-1", "request-1", attempt.ID) {
			t.Fatal("a cleared request still accepted its result")
		}
		select {
		case <-attempt.Cancelled:
		default:
			t.Fatal("clearing did not cancel the attempt")
		}
	}
	if _, begun := requests.Begin("server-1", "request-2"); begun {
		t.Fatal("a cleared request could start a verification")
	}
}

// The approved parameters are kept apart from the view so a redrawn prompt
// cannot change what the verifier signs.
func TestUserVerificationRequestsKeepApprovedParamsLikeRust(t *testing.T) {
	requests := &UserVerificationRequests{}
	requests.NoteRequest("server-1", "request-1", UserVerificationParams{
		Title:       "Verify",
		Description: "Approve deployment of release 42",
		Challenge:   "AQID",
	})
	attempt, ok := requests.Begin("server-1", "request-1")
	if !ok {
		t.Fatal("attempt was refused")
	}
	if attempt.Params.Description != "Approve deployment of release 42" || attempt.Params.Challenge != "AQID" {
		t.Fatalf("approved params = %#v", attempt.Params)
	}
	if _, unknown := requests.Begin("server-2", "request-1"); unknown {
		t.Fatal("an unnoted server started a verification")
	}
}
