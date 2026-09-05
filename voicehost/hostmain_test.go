package voicehost

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

type recordingRuntime struct {
	started  bool
	stopped  bool
	startErr error
	stopErr  error
}

func (r *recordingRuntime) Name() string { return "recording" }

func (r *recordingRuntime) Start(context.Context, SessionConfig) error {
	r.started = true
	return r.startErr
}

func (r *recordingRuntime) Stop() error {
	r.stopped = true
	return r.stopErr
}

func (r *recordingRuntime) ListInputDevices(context.Context) ([]Device, error) {
	return []Device{}, nil
}

func (r *recordingRuntime) ListOutputDevices(context.Context) ([]Device, error) {
	return []Device{}, nil
}

func (r *recordingRuntime) OpenInput(context.Context, string) (AudioSource, error) {
	return nil, ErrRuntimeNotInitialized
}

func (r *recordingRuntime) OpenOutput(context.Context, string) (AudioSink, error) {
	return nil, ErrRuntimeNotInitialized
}

type fakeVoiceTransport struct {
	offer     string
	offerErr  error
	answer    string
	answerErr error
	closeErr  error
	closed    bool
}

func (f *fakeVoiceTransport) Offer(context.Context) (string, error) {
	return f.offer, f.offerErr
}

func (f *fakeVoiceTransport) ApplyAnswer(_ context.Context, sdp string) error {
	f.answer = sdp
	return f.answerErr
}

func (f *fakeVoiceTransport) Close() error {
	f.closed = true
	return f.closeErr
}

func TestRunHostHappyPath(t *testing.T) {
	runtime := &recordingRuntime{}
	transport := &fakeVoiceTransport{offer: "v=0\r\no=offer\r\n"}
	input := concatFrames(t,
		NewHello(1, "build-commit"),
		NewSimpleMessage(TypeInitializeRuntime),
		NewSimpleMessage(TypeStartTransport),
		NewSDPMessage(TypeApplyAnswer, mustSDP(t, "v=0\r\no=answer\r\n")),
		NewSimpleMessage(TypeClose),
	)
	var output bytes.Buffer
	err := runHost(context.Background(), bytes.NewReader(input), &output, "build-commit", runtime, func() (VoiceTransport, error) {
		return transport, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	responses := readAllFrames(t, output.Bytes())
	if len(responses) != 5 {
		t.Fatalf("responses = %d, want 5: %#v", len(responses), responses)
	}
	expected := []MessageType{TypeReady, TypeRuntimeReady, TypeOffer, TypeTransportReady, TypeClosed}
	for i, want := range expected {
		if responses[i].Type != want {
			t.Fatalf("response %d = %q, want %q", i, responses[i].Type, want)
		}
	}
	if responses[2].SDP == nil || responses[2].SDP.SDP() != "v=0\r\no=offer\r\n" {
		t.Fatalf("offer = %#v", responses[2].SDP)
	}
	if transport.answer != "v=0\r\no=answer\r\n" || !transport.closed {
		t.Fatalf("transport answer=%q closed=%v", transport.answer, transport.closed)
	}
	if !runtime.started || !runtime.stopped {
		t.Fatalf("runtime started=%v stopped=%v", runtime.started, runtime.stopped)
	}
}

func TestRunHostRejectsIncompatibleHello(t *testing.T) {
	input := concatFrames(t, NewHello(1, "other-build"))
	var output bytes.Buffer
	err := runHost(context.Background(), bytes.NewReader(input), &output, "build-commit", &recordingRuntime{}, func() (VoiceTransport, error) {
		return &fakeVoiceTransport{}, nil
	})
	if !errors.Is(err, ErrIncompatibleVoiceHelper) {
		t.Fatalf("error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("incompatible hello echoed output: %q", output.Bytes())
	}
}

func TestRunHostRejectsDuplicateRuntimeInitialization(t *testing.T) {
	runtime := &recordingRuntime{}
	input := concatFrames(t,
		NewHello(1, "build-commit"),
		NewSimpleMessage(TypeInitializeRuntime),
		NewSimpleMessage(TypeInitializeRuntime),
	)
	var output bytes.Buffer
	err := runHost(context.Background(), bytes.NewReader(input), &output, "build-commit", runtime, func() (VoiceTransport, error) {
		return &fakeVoiceTransport{}, nil
	})
	if !errors.Is(err, ErrInvalidVoiceControlSequence) {
		t.Fatalf("error = %v", err)
	}
}

func TestRunHostEOFAfterHelloReturnsCleanly(t *testing.T) {
	input := concatFrames(t, NewHello(1, "build-commit"))
	var output bytes.Buffer
	err := runHost(context.Background(), bytes.NewReader(input), &output, "build-commit", &recordingRuntime{}, func() (VoiceTransport, error) {
		return &fakeVoiceTransport{}, nil
	})
	if err != nil {
		t.Fatalf("clean EOF error = %v", err)
	}
	responses := readAllFrames(t, output.Bytes())
	if len(responses) != 1 || responses[0].Type != TypeReady {
		t.Fatalf("responses = %#v", responses)
	}
}

func concatFrames(t *testing.T, messages ...Message) []byte {
	t.Helper()
	var input bytes.Buffer
	for _, message := range messages {
		if err := WriteMessage(&input, message); err != nil {
			t.Fatal(err)
		}
	}
	return input.Bytes()
}

func readAllFrames(t *testing.T, data []byte) []Message {
	t.Helper()
	reader := bytes.NewReader(data)
	var messages []Message
	for {
		message, err := ReadMessage(reader)
		if err != nil {
			t.Fatal(err)
		}
		if message == nil {
			return messages
		}
		messages = append(messages, *message)
	}
}

func mustSDP(t *testing.T, value string) SessionDescription {
	t.Helper()
	sdp, err := NewSessionDescription(value)
	if err != nil {
		t.Fatal(err)
	}
	return sdp
}
