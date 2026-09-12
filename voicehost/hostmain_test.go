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

type fakeAudioSource struct {
	closed bool
}

func (s *fakeAudioSource) Read(context.Context) (Frame, error) { return Frame{}, nil }
func (s *fakeAudioSource) Close() error {
	s.closed = true
	return nil
}

type fakeAudioSink struct {
	closed bool
}

func (s *fakeAudioSink) Write(context.Context, Frame) error { return nil }
func (s *fakeAudioSink) Close() error {
	s.closed = true
	return nil
}

// controllableRuntime opens device handles and records ordered privacy
// controls, mirroring a native runtime that implements ControlRuntime.
type controllableRuntime struct {
	recordingRuntime
	controlCalls  []AudioControls
	state         AudioState
	controlErr    error
	openInputErr  error
	openOutputErr error
	source        *fakeAudioSource
	sink          *fakeAudioSink
}

func (r *controllableRuntime) OpenInput(context.Context, string) (AudioSource, error) {
	if r.openInputErr != nil {
		return nil, r.openInputErr
	}
	r.source = &fakeAudioSource{}
	return r.source, nil
}

func (r *controllableRuntime) OpenOutput(context.Context, string) (AudioSink, error) {
	if r.openOutputErr != nil {
		return nil, r.openOutputErr
	}
	r.sink = &fakeAudioSink{}
	return r.sink, nil
}

func (r *controllableRuntime) SetControls(controls AudioControls) error {
	if r.controlErr != nil {
		return r.controlErr
	}
	r.controlCalls = append(r.controlCalls, controls)
	return nil
}

func (r *controllableRuntime) AudioState() AudioState { return r.state }

func TestRunHostDeviceControlSequence(t *testing.T) {
	runtime := &controllableRuntime{state: AudioState{MicrophonePeak: 7, SpeakerPeak: 9}}
	transport := &fakeVoiceTransport{offer: "v=0\r\no=offer\r\n"}
	input := concatFrames(t,
		NewHello(1, "build-commit"),
		NewSimpleMessage(TypeInitializeRuntime),
		NewSimpleMessage(TypeStartTransport),
		NewSDPMessage(TypeApplyAnswer, mustSDP(t, "v=0\r\no=answer\r\n")),
		NewSimpleMessage(TypeOpenDevices),
		NewAudioControlsMessage(AudioControls{MicrophoneMuted: true}),
		NewSimpleMessage(TypeInspectAudio),
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
	expected := []MessageType{
		TypeReady,
		TypeRuntimeReady,
		TypeOffer,
		TypeTransportReady,
		TypeDevicesOpened,
		TypeAudioControlsApplied,
		TypeAudioState,
		TypeClosed,
	}
	if len(responses) != len(expected) {
		t.Fatalf("responses = %d, want %d: %#v", len(responses), len(expected), responses)
	}
	for i, want := range expected {
		if responses[i].Type != want {
			t.Fatalf("response %d = %q, want %q", i, responses[i].Type, want)
		}
	}
	state := responses[6].State
	if state == nil || state.MicrophonePeak != 7 || state.SpeakerPeak != 9 {
		t.Fatalf("audio state = %#v", state)
	}
	if len(runtime.controlCalls) != 1 || !runtime.controlCalls[0].MicrophoneMuted {
		t.Fatalf("control calls = %#v", runtime.controlCalls)
	}
	if runtime.source == nil || runtime.sink == nil || !runtime.source.closed || !runtime.sink.closed {
		t.Fatalf("devices were not closed: source=%#v sink=%#v", runtime.source, runtime.sink)
	}
}

func TestRunHostRejectsOutOfOrderDeviceControls(t *testing.T) {
	answered := concatFrames(t,
		NewHello(1, "build-commit"),
		NewSimpleMessage(TypeInitializeRuntime),
		NewSimpleMessage(TypeStartTransport),
		NewSDPMessage(TypeApplyAnswer, mustSDP(t, "v=0\r\no=answer\r\n")),
	)
	tests := []struct {
		name    string
		message []Message
	}{
		{
			name:    "open devices before runtime",
			message: []Message{NewHello(1, "build-commit"), NewSimpleMessage(TypeOpenDevices)},
		},
		{
			name:    "open devices before answer",
			message: []Message{NewHello(1, "build-commit"), NewSimpleMessage(TypeInitializeRuntime), NewSimpleMessage(TypeOpenDevices)},
		},
		{
			name:    "controls before devices",
			message: []Message{NewAudioControlsMessage(AudioControls{})},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := append(append([]byte{}, answered...), concatFrames(t, test.message...)...)
			var output bytes.Buffer
			err := runHost(context.Background(), bytes.NewReader(input), &output, "build-commit", &controllableRuntime{}, func() (VoiceTransport, error) {
				return &fakeVoiceTransport{offer: "v=0\r\no=offer\r\n"}, nil
			})
			if !errors.Is(err, ErrInvalidVoiceControlSequence) {
				t.Fatalf("error = %v, want ErrInvalidVoiceControlSequence", err)
			}
			var exitErr *HelperExitError
			if !errors.As(err, &exitErr) || exitErr.Stage != HelperExitControlSequence {
				t.Fatalf("stage = %#v, want controlSequence", err)
			}
		})
	}
}

func TestRunHostRejectsDuplicateOpenDevices(t *testing.T) {
	input := concatFrames(t,
		NewHello(1, "build-commit"),
		NewSimpleMessage(TypeInitializeRuntime),
		NewSimpleMessage(TypeStartTransport),
		NewSDPMessage(TypeApplyAnswer, mustSDP(t, "v=0\r\no=answer\r\n")),
		NewSimpleMessage(TypeOpenDevices),
		NewSimpleMessage(TypeOpenDevices),
	)
	var output bytes.Buffer
	err := runHost(context.Background(), bytes.NewReader(input), &output, "build-commit", &controllableRuntime{}, func() (VoiceTransport, error) {
		return &fakeVoiceTransport{offer: "v=0\r\no=offer\r\n"}, nil
	})
	if !errors.Is(err, ErrInvalidVoiceControlSequence) {
		t.Fatalf("error = %v", err)
	}
}

func TestRunHostReportsTransportTimeout(t *testing.T) {
	transport := &fakeVoiceTransport{offer: "v=0\r\no=offer\r\n", answerErr: ErrVoiceTransportTimeout}
	input := concatFrames(t,
		NewHello(1, "build-commit"),
		NewSimpleMessage(TypeStartTransport),
		NewSDPMessage(TypeApplyAnswer, mustSDP(t, "v=0\r\no=answer\r\n")),
	)
	var output bytes.Buffer
	err := runHost(context.Background(), bytes.NewReader(input), &output, "build-commit", &controllableRuntime{}, func() (VoiceTransport, error) {
		return transport, nil
	})
	if err != nil {
		t.Fatalf("timeout must retire quietly, got %v", err)
	}
	responses := readAllFrames(t, output.Bytes())
	if len(responses) != 3 || responses[2].Type != TypeTransportTimedOut {
		t.Fatalf("responses = %#v", responses)
	}
	if !transport.closed {
		t.Fatal("timed-out transport was not closed")
	}
}

func TestRunHostInspectAudioBeforeDevicesReportsDefault(t *testing.T) {
	input := concatFrames(t,
		NewHello(1, "build-commit"),
		NewSimpleMessage(TypeInspectAudio),
	)
	var output bytes.Buffer
	err := runHost(context.Background(), bytes.NewReader(input), &output, "build-commit", &controllableRuntime{state: AudioState{MicrophonePeak: 5}}, func() (VoiceTransport, error) {
		return &fakeVoiceTransport{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	responses := readAllFrames(t, output.Bytes())
	if len(responses) != 2 || responses[1].Type != TypeAudioState {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[1].State == nil || responses[1].State.MicrophonePeak != 0 {
		t.Fatalf("state before devices open = %#v", responses[1].State)
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

// TestDefaultSessionFormatMatchesOpusRTPPipeline locks the device PCM format to
// the Opus RTP pipeline. The helper has no resampler, so a device rate that
// differs from opusClockRate would make the 480-sample block timing and the
// 960-sample (20 ms) frames silently wrong.
func TestDefaultSessionFormatMatchesOpusRTPPipeline(t *testing.T) {
	if defaultSessionFormat.SampleRate != opusClockRate {
		t.Fatalf("default session rate = %d, want Opus clock %d",
			defaultSessionFormat.SampleRate, opusClockRate)
	}
	if defaultSessionFormat.Channels != 1 || defaultSessionFormat.Encoding != AudioEncodingS16LE {
		t.Fatalf("default session format = %+v", defaultSessionFormat)
	}
	if want := opusClockRate / 100; audioBlockSamples != want {
		t.Fatalf("audioBlockSamples = %d, want %d (10 ms at %d Hz)", audioBlockSamples, want, opusClockRate)
	}
	if want := opusClockRate / 50; voiceFrameSamples != want {
		t.Fatalf("voiceFrameSamples = %d, want %d (20 ms at %d Hz)", voiceFrameSamples, want, opusClockRate)
	}
}
