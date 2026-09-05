package voicehost

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrIncompatibleVoiceHelper indicates the parent did not send the exact
// protocol version and build commit expected by this helper.
var ErrIncompatibleVoiceHelper = errors.New("incompatible voice helper")

// ErrInvalidVoiceControlSequence indicates an out-of-order or duplicate control
// message. Such messages fail closed without echoing input.
var ErrInvalidVoiceControlSequence = errors.New("invalid voice control sequence")

// defaultSessionFormat is the native PCM format used before device discovery or
// configuration is added to the control protocol.
var defaultSessionFormat = AudioFormat{SampleRate: 24000, Channels: 1, Encoding: AudioEncodingS16LE}

// RunHost runs the voice helper protocol over stdin and stdout. It owns WebRTC
// negotiation and the packaged runtime, but never opens audio devices through
// this control path.
func RunHost(ctx context.Context, stdin io.Reader, stdout io.Writer, buildCommit string) error {
	return RunHostWithRuntime(ctx, stdin, stdout, buildCommit, NewMiniAudioRuntime())
}

// RunHostWithRuntime is RunHost with an injectable runtime, primarily for tests.
func RunHostWithRuntime(ctx context.Context, stdin io.Reader, stdout io.Writer, buildCommit string, runtime Runtime) error {
	return runHost(ctx, stdin, stdout, buildCommit, runtime, func() (VoiceTransport, error) {
		return NewTransport()
	})
}

func runHost(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	buildCommit string,
	runtime Runtime,
	newTransport func() (VoiceTransport, error),
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if stdin == nil || stdout == nil {
		return ErrInvalidVoiceControlSequence
	}
	if buildCommit == "" {
		return ErrIncompatibleVoiceHelper
	}
	if runtime == nil {
		return errors.New("voice runtime is required")
	}

	hello, err := ReadMessage(stdin)
	if err != nil {
		return err
	}
	if hello == nil || hello.Type != TypeHello || hello.Protocol == nil || *hello.Protocol != 1 || hello.BuildCommit != buildCommit {
		return ErrIncompatibleVoiceHelper
	}
	if err := WriteMessage(stdout, NewSimpleMessage(TypeReady)); err != nil {
		return err
	}

	var transport VoiceTransport
	var answered bool
	var runtimeStarted bool
	defer func() {
		if transport != nil {
			_ = transport.Close()
		}
		if runtimeStarted {
			_ = runtime.Stop()
		}
	}()

	for {
		message, err := ReadMessage(stdin)
		if err != nil {
			return err
		}
		if message == nil {
			return nil
		}
		var reply Message
		switch message.Type {
		case TypeStartTransport:
			if transport != nil {
				return ErrInvalidVoiceControlSequence
			}
			transport, err = newTransport()
			if err != nil {
				return err
			}
			offer, err := transport.Offer(ctx)
			if err != nil {
				return err
			}
			sdp, err := NewSessionDescription(offer)
			if err != nil {
				return err
			}
			reply = NewSDPMessage(TypeOffer, sdp)
		case TypeApplyAnswer:
			if transport == nil || answered || message.SDP == nil {
				return ErrInvalidVoiceControlSequence
			}
			if err := transport.ApplyAnswer(ctx, message.SDP.SDP()); err != nil {
				return err
			}
			answered = true
			reply = NewSimpleMessage(TypeTransportReady)
		case TypeInitializeRuntime:
			if runtimeStarted {
				return ErrInvalidVoiceControlSequence
			}
			if err := runtime.Start(ctx, SessionConfig{Format: defaultSessionFormat}); err != nil {
				return fmt.Errorf("private audio runtime initialization failed: %w", err)
			}
			runtimeStarted = true
			reply = NewSimpleMessage(TypeRuntimeReady)
		case TypeClose:
			if transport != nil {
				if err := transport.Close(); err != nil {
					return err
				}
				transport = nil
			}
			if runtimeStarted {
				if err := runtime.Stop(); err != nil {
					return err
				}
				runtimeStarted = false
			}
			return WriteMessage(stdout, NewSimpleMessage(TypeClosed))
		default:
			return ErrInvalidVoiceControlSequence
		}
		if err := WriteMessage(stdout, reply); err != nil {
			return err
		}
	}
}
