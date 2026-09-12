package voicehost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// deviceServiceInterval is how long the control loop waits for a privacy
// transition before servicing audio again.
const deviceServiceInterval = 5 * time.Millisecond

// ErrIncompatibleVoiceHelper indicates the parent did not send the exact
// protocol version and build commit expected by this helper.
var ErrIncompatibleVoiceHelper = errors.New("incompatible voice helper")

// ErrInvalidVoiceControlSequence indicates an out-of-order or duplicate control
// message. Such messages fail closed without echoing input.
var ErrInvalidVoiceControlSequence = errors.New("invalid voice control sequence")

// defaultSessionFormat is the native PCM format used before device discovery or
// configuration is added to the control protocol. The helper has no resampler,
// so the device, the bounded packer, the Opus codec, and the RTP clock must all
// agree on the 48 kHz Opus rate.
var defaultSessionFormat = AudioFormat{SampleRate: 48000, Channels: 1, Encoding: AudioEncodingS16LE}

// RunHost runs the voice helper protocol over stdin and stdout. It owns WebRTC
// negotiation and the packaged runtime, but never opens audio devices through
// this control path.
func RunHost(ctx context.Context, stdin io.Reader, stdout io.Writer, buildCommit string) error {
	installPackagedVoiceCodec()
	return RunHostWithRuntime(ctx, stdin, stdout, buildCommit, NewMiniAudioRuntime())
}

// installPackagedVoiceCodec loads the codec that ships beside the helper. A
// package without one keeps the control plane and device buffers working, and
// the runtime inspection surfaces report the codec as missing.
func installPackagedVoiceCodec() {
	packageDir := voicePackageDirFromExecutable()
	if packageDir == "" {
		return
	}
	codec, err := LoadPackagedVoiceCodec(packageDir)
	if err != nil {
		return
	}
	SetDefaultVoiceCodec(codec)
}

// voicePackageDirFromExecutable derives the package root from the helper's own
// path: <root>/codex-resources/voice/bin/codex-voice-host[.exe]. A helper that
// is not installed in that layout reports no package.
func voicePackageDirFromExecutable() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	bin := filepath.Dir(executable)
	voice := filepath.Dir(bin)
	resources := filepath.Dir(voice)
	root := filepath.Dir(resources)
	if filepath.Base(bin) != "bin" ||
		filepath.Base(voice) != "voice" ||
		filepath.Base(resources) != "codex-resources" {
		return ""
	}
	return root
}

// RunHostWithRuntime is RunHost with an injectable runtime, primarily for tests.
func RunHostWithRuntime(ctx context.Context, stdin io.Reader, stdout io.Writer, buildCommit string, runtime Runtime) error {
	return runHost(ctx, stdin, stdout, buildCommit, runtime, func() (VoiceTransport, error) {
		return NewTransport()
	})
}

// transportReadiness is implemented by transports that can report whether the
// ordered event channel is still open. It is optional so test transports stay
// small.
type transportReadiness interface {
	Ready() bool
}

// hostDevices is the opened local device pair for one session.
type hostDevices struct {
	source AudioSource
	sink   AudioSink
}

// close releases both device handles, tolerating partial opens.
func (d *hostDevices) close() {
	if d == nil {
		return
	}
	if d.source != nil {
		_ = d.source.Close()
		d.source = nil
	}
	if d.sink != nil {
		_ = d.sink.Close()
		d.sink = nil
	}
}

// controlQueueCapacity bounds unread control messages. The parent is strictly
// request/response, so this only absorbs a legitimate burst; an overflowing
// queue fails closed instead of buffering stale commands.
const controlQueueCapacity = 16

// controlReader reads framed messages on its own goroutine so the main loop can
// service audio while waiting for a privacy transition. A full queue fails
// closed, which is what keeps queued controls ahead of the next capture batch.
type controlReader struct {
	messages chan Message
	overflow atomic.Bool
	readErr  atomic.Value
}

func newControlReader(stdin io.Reader) *controlReader {
	reader := &controlReader{messages: make(chan Message, controlQueueCapacity)}
	go func() {
		defer close(reader.messages)
		for {
			message, err := ReadMessage(stdin)
			if err != nil {
				reader.readErr.Store(err)
				return
			}
			if message == nil {
				return
			}
			select {
			case reader.messages <- *message:
			default:
				reader.overflow.Store(true)
				return
			}
		}
	}()
	return reader
}

// next blocks for the next control message.
func (r *controlReader) next() (Message, bool) {
	message, ok := <-r.messages
	return message, ok
}

// tryNext returns the next control message without blocking.
func (r *controlReader) tryNext() (Message, bool, bool) {
	select {
	case message, ok := <-r.messages:
		return message, ok, true
	default:
		return Message{}, false, false
	}
}

// err reports why the control stream ended.
func (r *controlReader) err() error {
	if r == nil {
		return nil
	}
	if r.overflow.Load() {
		return ErrInvalidVoiceControlSequence
	}
	if value := r.readErr.Load(); value != nil {
		if err, ok := value.(error); ok {
			return err
		}
	}
	return nil
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
		return withExitStage(HelperExitControlSequence, ErrInvalidVoiceControlSequence)
	}
	if buildCommit == "" {
		return withExitStage(HelperExitControlSequence, ErrIncompatibleVoiceHelper)
	}
	if runtime == nil {
		return withExitStage(HelperExitControlSequence, errors.New("voice runtime is required"))
	}

	reader := newControlReader(stdin)
	hello, open := reader.next()
	if !open {
		if err := reader.err(); err != nil {
			return withExitStage(HelperExitControlRead, err)
		}
		return withExitStage(HelperExitControlSequence, ErrIncompatibleVoiceHelper)
	}
	if hello.Type != TypeHello || hello.Protocol == nil || *hello.Protocol != 1 || hello.BuildCommit != buildCommit {
		return withExitStage(HelperExitControlSequence, ErrIncompatibleVoiceHelper)
	}
	if err := WriteMessage(stdout, NewSimpleMessage(TypeReady)); err != nil {
		return withExitStage(HelperExitReply, err)
	}

	var (
		transport      VoiceTransport
		devices        *hostDevices
		media          *mediaSession
		audioSender    voiceSender
		answered       bool
		runtimeStarted bool
		serviceErr     error
	)
	defer func() {
		devices.close()
		media = nil
		if transport != nil {
			_ = transport.Close()
		}
		if runtimeStarted {
			_ = runtime.Stop()
		}
	}()

	// nextControl waits for the next control message. While devices are open it
	// services audio every device interval first, so pending privacy controls
	// and shutdown always precede the next capture batch.
	nextControl := func() (Message, bool) {
		if devices == nil {
			return reader.next()
		}
		for {
			select {
			case message, open := <-reader.messages:
				return message, open
			case <-time.After(deviceServiceInterval):
				if err := serviceHostMedia(media); err != nil {
					serviceErr = err
					return Message{}, false
				}
			}
		}
	}

	for {
		message, open := nextControl()
		if !open {
			if serviceErr != nil {
				return withExitStage(HelperExitAudioService, serviceErr)
			}
			if err := reader.err(); err != nil {
				return withExitStage(HelperExitControlRead, err)
			}
			return nil
		}
		var reply Message
		switch message.Type {
		case TypeStartTransport:
			if transport != nil {
				return withExitStage(HelperExitControlSequence, ErrInvalidVoiceControlSequence)
			}
			next, err := newTransport()
			if err != nil {
				return withExitStage(HelperExitTransport, err)
			}
			// Attach the audio track before the offer is gathered so the SDP
			// advertises the media path. Peer audio that arrives before the
			// session exists is dropped rather than queued: a peer must not
			// send audio before negotiation and device startup complete.
			if enabler, ok := next.(interface {
				EnableAudio(RTPPacketSink) error
			}); ok {
				if err := enabler.EnableAudio(func(payload []byte, ssrc uint32, at time.Time) {
					if media != nil {
						_ = media.handleRTP(payload, ssrc, at)
					}
				}); err != nil {
					return withExitStage(HelperExitTransport, err)
				}
			}
			if provider, ok := next.(interface{ AudioSender() voiceSender }); ok {
				audioSender = provider.AudioSender()
			}
			transport = next
			offer, err := transport.Offer(ctx)
			if err != nil {
				return withExitStage(HelperExitTransport, err)
			}
			sdp, err := NewSessionDescription(offer)
			if err != nil {
				return withExitStage(HelperExitTransport, err)
			}
			reply = NewSDPMessage(TypeOffer, sdp)
		case TypeApplyAnswer:
			if transport == nil || answered || message.SDP == nil {
				return withExitStage(HelperExitControlSequence, ErrInvalidVoiceControlSequence)
			}
			err := transport.ApplyAnswer(ctx, message.SDP.SDP())
			if errors.Is(err, ErrVoiceTransportTimeout) {
				// The parent reaps this helper before considering a fresh
				// negotiation, so report the timeout and retire quietly.
				if writeErr := WriteMessage(stdout, NewSimpleMessage(TypeTransportTimedOut)); writeErr != nil {
					return withExitStage(HelperExitReply, writeErr)
				}
				return nil
			}
			if err != nil {
				return withExitStage(HelperExitTransport, err)
			}
			answered = true
			reply = NewSimpleMessage(TypeTransportReady)
		case TypeInitializeRuntime:
			if runtimeStarted {
				return withExitStage(HelperExitControlSequence, ErrInvalidVoiceControlSequence)
			}
			if err := runtime.Start(ctx, SessionConfig{Format: defaultSessionFormat}); err != nil {
				return withExitStage(HelperExitRuntime, fmt.Errorf("private audio runtime initialization failed: %w", err))
			}
			runtimeStarted = true
			reply = NewSimpleMessage(TypeRuntimeReady)
		case TypeOpenDevices:
			// Devices open only after negotiation so nothing captures audio
			// before the peer is ready.
			if devices != nil || !runtimeStarted || !answered {
				return withExitStage(HelperExitControlSequence, ErrInvalidVoiceControlSequence)
			}
			opened, err := openHostDevices(ctx, runtime)
			if err != nil {
				return withExitStage(HelperExitOpenDevices, err)
			}
			devices = opened
			media = openHostMedia(audioSender, runtime)
			reply = NewSimpleMessage(TypeDevicesOpened)
		case TypeSetAudioControls:
			if transport == nil || devices == nil || message.Controls == nil {
				return withExitStage(HelperExitControlSequence, ErrInvalidVoiceControlSequence)
			}
			if media != nil {
				if err := media.setControls(*message.Controls); err != nil {
					return withExitStage(HelperExitAudioControls, err)
				}
			}
			if control, ok := runtime.(ControlRuntime); ok {
				if err := control.SetControls(*message.Controls); err != nil {
					return withExitStage(HelperExitAudioControls, err)
				}
			}
			reply = NewSimpleMessage(TypeAudioControlsApplied)
		case TypeInspectAudio:
			if answered {
				if readiness, ok := transport.(transportReadiness); ok && !readiness.Ready() {
					return withExitStage(HelperExitInspectAudio, errors.New("voice connection closed"))
				}
			}
			state := AudioState{}
			if media != nil {
				state = media.takeAudioState()
			} else if devices != nil {
				if control, ok := runtime.(ControlRuntime); ok {
					state = control.AudioState()
				}
			}
			reply = NewAudioStateMessage(state)
		case TypeClose:
			media = nil
			devices.close()
			devices = nil
			if transport != nil {
				if err := transport.Close(); err != nil {
					return withExitStage(HelperExitShutdown, err)
				}
				transport = nil
			}
			if runtimeStarted {
				if err := runtime.Stop(); err != nil {
					return withExitStage(HelperExitShutdown, err)
				}
				runtimeStarted = false
			}
			if err := WriteMessage(stdout, NewSimpleMessage(TypeClosed)); err != nil {
				return withExitStage(HelperExitReply, err)
			}
			return nil
		default:
			return withExitStage(HelperExitControlSequence, ErrInvalidVoiceControlSequence)
		}
		if err := WriteMessage(stdout, reply); err != nil {
			return withExitStage(HelperExitReply, err)
		}
	}
}

// openHostDevices opens the default input and output devices for one session.
// A failed output open releases the input so a retry starts clean.
func openHostDevices(ctx context.Context, runtime Runtime) (*hostDevices, error) {
	source, err := runtime.OpenInput(ctx, "")
	if err != nil {
		return nil, err
	}
	sink, err := runtime.OpenOutput(ctx, "")
	if err != nil {
		_ = source.Close()
		return nil, err
	}
	return &hostDevices{source: source, sink: sink}, nil
}

// openHostMedia binds the media session to the transport's audio track and the
// runtime's device pipeline. It returns nil when either side is unavailable, in
// which case the session runs the control plane only.
func openHostMedia(sender voiceSender, runtime Runtime) *mediaSession {
	if sender == nil {
		return nil
	}
	mediaRuntime, ok := runtime.(MediaRuntime)
	if !ok {
		return nil
	}
	pipeline := mediaRuntime.Pipeline()
	if pipeline == nil {
		return nil
	}
	pipeline.recordSessionStart()
	return newMediaSession(defaultVoiceCodec, pipeline, sender)
}

// serviceHostMedia runs one media service pass, which is bounded so a waiting
// privacy control is never postponed indefinitely.
func serviceHostMedia(media *mediaSession) error {
	if media == nil {
		return nil
	}
	_, err := media.service(time.Now())
	return err
}
