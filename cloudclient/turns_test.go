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
	trigger := "safety_recourse_follow_up"
	encoded := encodeAddUserMessageRequest(AddUserMessageRequest{
		ThreadID: "thread", ClientUserMessageID: "client",
		Input:       []UserMessageInput{{Kind: UserMessageText, Text: "hello"}},
		TurnTrigger: &trigger,
		Mode:        SubmissionQueue, SharedAccess: true,
	})
	text := protoMessage(1, []byte("hello"))
	userInput := protoMessage(1, text)
	input := protoMessage(1, userInput)
	want := protoMessage(4, []byte("client"))
	want = append(want, protoMessage(11, input)...)
	want = append(want, protoMessage(23, []byte("thread"))...)
	want = append(want, protoMessage(25, []byte(trigger))...)
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

// TestAddUserMessageEncodesModelAndEffortLikeRust mirrors Rust #60651: a staged
// model and effort travel on AddRequest fields 12 and 9, and queue mode cannot
// carry either setting.
func TestAddUserMessageEncodesModelAndEffortLikeRust(t *testing.T) {
	model, effort := "gpt-5.1", "high"
	encoded := encodeAddUserMessageRequest(AddUserMessageRequest{
		ThreadID: "thread", ClientUserMessageID: "client",
		Input: []UserMessageInput{{Kind: UserMessageText, Text: "hello"}},
		Model: &model, Effort: &effort,
		Mode: SubmissionStartIfIdle,
	})
	want := protoMessage(4, []byte("client"))
	want = append(want, protoMessage(9, []byte("high"))...)
	text := protoMessage(1, []byte("hello"))
	userInput := protoMessage(1, text)
	input := protoMessage(1, userInput)
	want = append(want, protoMessage(11, input)...)
	want = append(want, protoMessage(12, []byte("gpt-5.1"))...)
	want = append(want, protoMessage(23, []byte("thread"))...)
	want = append(want, protoVarint(30, 3)...)
	if !bytes.Equal(encoded, want) {
		t.Fatalf("add request = %x, want %x", encoded, want)
	}
	if validAddUserMessageRequest(AddUserMessageRequest{
		ThreadID: "thread", ClientUserMessageID: "client",
		Input: []UserMessageInput{{Kind: UserMessageText, Text: "hello"}},
		Model: &model, Mode: SubmissionQueue,
	}) {
		t.Fatal("queue mode accepted a staged model")
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

// wireContentItem is one decoded part of an AddUserMessage input; kind follows
// Rust's UserInput oneof tags (1 text, 2 image, ...).
type wireContentItem struct {
	kind   int
	text   string
	source int
	value  string
	detail int64
}

// decodeRequestInputs reads AddRequest.input (11) and its repeated UserInput (1).
func decodeRequestInputs(t *testing.T, encoded []byte) []wireContentItem {
	t.Helper()
	input, ok := bytesField(encoded, 11)
	if !ok {
		t.Fatalf("request = %x, want an input field", encoded)
	}
	items := []wireContentItem{}
	for _, part := range repeatedBytes(input, 1) {
		kind, value, err := selectOneof(part, 1, 7)
		if err != nil {
			t.Fatalf("read content item: %v", err)
		}
		item := wireContentItem{kind: kind}
		switch kind {
		case 1:
			item.text, _ = stringField(value, 1)
		case 2:
			source, reference, imageErr := selectOneof(value, 1, 2)
			if imageErr != nil {
				t.Fatalf("read image item: %v", imageErr)
			}
			item.source = source
			item.value, _ = stringField(reference, 2)
			if detail := optionalInt64Field(reference, 1); detail != nil {
				item.detail = *detail
			}
		}
		items = append(items, item)
	}
	return items
}

// TestAddUserMessageEncodesImageContentItemLikeRust pins the typed content item:
// an image part travels as the `image` alternative with its reference and
// detail, not as text that swallowed the data URL.
func TestAddUserMessageEncodesImageContentItemLikeRust(t *testing.T) {
	detail := ImageDetailHigh
	encoded := encodeAddUserMessageRequest(AddUserMessageRequest{
		ThreadID: "thread", ClientUserMessageID: "client",
		Input: []UserMessageInput{
			{Kind: UserMessageText, Text: "look at this"},
			{Kind: UserMessageImage, Image: ImageReference{URL: "data:image/png;base64,AAA"}, Detail: &detail},
		},
		Mode: SubmissionStartIfIdle,
	})
	parts := decodeRequestInputs(t, encoded)
	if len(parts) != 2 {
		t.Fatalf("content items = %d, want the text part and the image part", len(parts))
	}
	if parts[0].kind != 1 || parts[0].text != "look at this" {
		t.Fatalf("first content item = %#v, want the text part", parts[0])
	}
	if parts[1].kind != 2 {
		t.Fatalf("got text item, want image item (oneof tag %d)", parts[1].kind)
	}
	if parts[1].source != 1 || parts[1].value != "data:image/png;base64,AAA" {
		t.Fatalf("image reference = %q under source %d, want the data URL under the url alternative", parts[1].value, parts[1].source)
	}
	if parts[1].detail != 3 {
		t.Fatalf("image detail = %d, want 3 (high)", parts[1].detail)
	}

	fileID := encodeAddUserMessageRequest(AddUserMessageRequest{
		ThreadID: "thread", ClientUserMessageID: "client",
		Input: []UserMessageInput{{Kind: UserMessageImage, Image: ImageReference{FileID: "file-1"}}},
		Mode:  SubmissionStartIfIdle,
	})
	uploaded := decodeRequestInputs(t, fileID)
	if len(uploaded) != 1 || uploaded[0].kind != 2 || uploaded[0].source != 2 || uploaded[0].value != "file-1" || uploaded[0].detail != 0 {
		t.Fatalf("uploaded image = %#v, want the fileId alternative without a detail", uploaded)
	}
}

// TestAddUserMessageRejectsUnsupportedImageReferencesLikeRust pins the preflight:
// only inline data URLs and non-blank file IDs are acceptable, and a text
// element range must stay inside its text.
func TestAddUserMessageRejectsUnsupportedImageReferencesLikeRust(t *testing.T) {
	for name, request := range map[string]AddUserMessageRequest{
		"http image": {
			ThreadID: "thread", ClientUserMessageID: "client",
			Input: []UserMessageInput{{Kind: UserMessageImage, Image: ImageReference{URL: "https://example.com/a.png"}}},
		},
		"empty data payload": {
			ThreadID: "thread", ClientUserMessageID: "client",
			Input: []UserMessageInput{{Kind: UserMessageImage, Image: ImageReference{URL: "data:image/png;base64, "}}},
		},
		"blank file id": {
			ThreadID: "thread", ClientUserMessageID: "client",
			Input: []UserMessageInput{{Kind: UserMessageImage, Image: ImageReference{FileID: "  "}}},
		},
		"range past text": {
			ThreadID: "thread", ClientUserMessageID: "client",
			Input: []UserMessageInput{{Kind: UserMessageText, Text: "hi", TextElements: []UserMessageTextElement{{Start: 0, End: 9}}}},
		},
		"no content": {ThreadID: "thread", ClientUserMessageID: "client"},
	} {
		if validAddUserMessageRequest(request) {
			t.Fatalf("%s passed the preflight", name)
		}
	}
	valid := AddUserMessageRequest{
		ThreadID: "thread", ClientUserMessageID: "client",
		Input: []UserMessageInput{
			{Kind: UserMessageText, Text: "look", TextElements: []UserMessageTextElement{{Start: 0, End: 4}}},
			{Kind: UserMessageImage, Image: ImageReference{URL: "DATA:image/png;base64,AAA"}},
			{Kind: UserMessageImage, Image: ImageReference{FileID: "file-1"}},
		},
	}
	if !validAddUserMessageRequest(valid) {
		t.Fatal("a text part with a data URL and a file ID image was rejected")
	}
}
