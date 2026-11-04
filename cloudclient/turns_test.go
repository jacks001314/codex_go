package cloudclient

import (
	"bytes"
	"testing"
)

func testTurnSummaryBytes(id string, status int64) []byte {
	out := protoMessage(4, []byte(id))
	return append(out, protoVarint(8, status)...)
}

func TestAddUserMessageRequestEncoding(t *testing.T) {
	encoded := encodeAddUserMessageRequest(AddUserMessageRequest{
		ThreadID: "thread", ClientUserMessageID: "client", Text: "hello",
		Mode: SubmissionQueue, SharedAccess: true,
	})
	text := protoMessage(1, []byte("hello"))
	userInput := protoMessage(1, text)
	input := protoMessage(1, userInput)
	want := protoMessage(4, []byte("client"))
	want = append(want, protoMessage(11, input)...)
	want = append(want, protoMessage(23, []byte("thread"))...)
	want = append(want, protoVarint(26, 1)...)
	want = append(want, protoVarint(30, 2)...)
	if !bytes.Equal(encoded, want) {
		t.Fatalf("add request = %x, want %x", encoded, want)
	}
	interrupt := encodeInterruptTurnRequest(InterruptTurnRequest{ThreadID: "thread", TurnID: "turn", SharedAccess: true})
	wantInterrupt := protoMessage(1, []byte("thread"))
	wantInterrupt = append(wantInterrupt, protoMessage(2, []byte("turn"))...)
	wantInterrupt = append(wantInterrupt, protoVarint(3, 1)...)
	if !bytes.Equal(interrupt, wantInterrupt) {
		t.Fatalf("interrupt request = %x, want %x", interrupt, wantInterrupt)
	}
}

func TestAdmissionOutcomesPreserveTurnAndQueueIdentity(t *testing.T) {
	turn := testTurnSummaryBytes("turn-1", 1)
	response := append(protoMessage(1, turn), protoVarint(2, 1)...)
	response = append(response, protoMessage(3, []byte("client"))...)
	outcome, err := decodeAdmission("client", response)
	if err != nil || outcome.Kind != AdmissionStarted || outcome.Turn == nil || outcome.Turn.ID != "turn-1" {
		t.Fatalf("started = %#v err=%v", outcome, err)
	}

	response = append(protoMessage(1, turn), protoVarint(2, 2)...)
	response = append(response, protoMessage(3, []byte("client"))...)
	outcome, err = decodeAdmission("client", response)
	if err != nil || outcome.Kind != AdmissionSteered || outcome.Turn == nil || outcome.Turn.Status != TurnCompleted {
		t.Fatalf("steered = %#v err=%v", outcome, err)
	}

	response = protoVarint(2, 3)
	response = append(response, protoMessage(3, []byte("client"))...)
	outcome, err = decodeAdmission("client", response)
	if err != nil || outcome.Kind != AdmissionDuplicate || outcome.TurnID != nil {
		t.Fatalf("duplicate = %#v err=%v", outcome, err)
	}

	queued := protoMessage(1, []byte("submission"))
	queued = append(queued, protoMessage(3, []byte("client"))...)
	response = protoVarint(2, 4)
	response = append(response, protoMessage(3, []byte("client"))...)
	response = append(response, protoMessage(5, queued)...)
	outcome, err = decodeAdmission("client", response)
	if err != nil || outcome.Kind != AdmissionQueued || outcome.SubmissionID != "submission" {
		t.Fatalf("queued = %#v err=%v", outcome, err)
	}

	bad := append(protoMessage(1, turn), protoVarint(2, 1)...)
	bad = append(bad, protoMessage(3, []byte("other"))...)
	if _, err := decodeAdmission("client", bad); err == nil {
		t.Fatal("mismatched client message id was accepted")
	}
	if _, err := decodeAdmission("client", append(protoVarint(2, 4), protoMessage(3, []byte("client"))...)); err == nil {
		t.Fatal("queued admission without submission id was accepted")
	}
}

func TestMutationErrorMarksUnknownOutcome(t *testing.T) {
	err := &MutationError{Kind: MutationOutcomeUnknown, Err: ErrTimeout}
	if !IsMutationOutcomeUnknown(err) {
		t.Fatal("unknown outcome was not recognized")
	}
	if IsMutationOutcomeUnknown(&MutationError{Kind: MutationInvalidRequest}) {
		t.Fatal("invalid request was reported as unknown outcome")
	}
	if err.Error() == "" || err.Unwrap() != ErrTimeout {
		t.Fatalf("mutation error = %q unwrap=%v", err.Error(), err.Unwrap())
	}
}
