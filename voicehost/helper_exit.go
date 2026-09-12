package voicehost

import "fmt"

// HelperExitStage identifies the fixed phase a voice helper failed in. The
// numeric codes are a same-build contract shared with the Rust helper so the
// parent can classify a failure without reading untyped child output.
type HelperExitStage int

const (
	// HelperExitControlRead means reading a framed control message failed.
	HelperExitControlRead HelperExitStage = 20
	// HelperExitControlQueue means the bounded control queue overflowed.
	HelperExitControlQueue HelperExitStage = 21
	// HelperExitParentGone means stdin closed and the helper retired itself.
	HelperExitParentGone HelperExitStage = 22
	// HelperExitRuntime means the private audio runtime failed to initialize.
	HelperExitRuntime HelperExitStage = 23
	// HelperExitTransport means WebRTC transport setup or negotiation failed.
	HelperExitTransport HelperExitStage = 24
	// HelperExitOpenDevices means opening local audio devices failed.
	HelperExitOpenDevices HelperExitStage = 25
	// HelperExitAudioIngress means reading inbound peer audio failed.
	HelperExitAudioIngress HelperExitStage = 26
	// HelperExitAudioService means servicing capture or playback failed.
	HelperExitAudioService HelperExitStage = 27
	// HelperExitInspectAudio means reporting audio levels failed.
	HelperExitInspectAudio HelperExitStage = 28
	// HelperExitAudioControls means applying privacy controls failed.
	HelperExitAudioControls HelperExitStage = 29
	// HelperExitReply means writing a framed reply failed.
	HelperExitReply HelperExitStage = 30
	// HelperExitShutdown means closing the session failed.
	HelperExitShutdown HelperExitStage = 31
	// HelperExitControlSequence means the control messages arrived out of order.
	HelperExitControlSequence HelperExitStage = 32
	// HelperExitPlayout means the playback pipeline failed.
	HelperExitPlayout HelperExitStage = 33
	// HelperExitRender means rendering echo-reference audio failed.
	HelperExitRender HelperExitStage = 35
	// HelperExitCapture means capture processing failed.
	HelperExitCapture HelperExitStage = 36
	// HelperExitDevice means a local audio device failed.
	HelperExitDevice HelperExitStage = 37
	// HelperExitSend means sending captured audio to the peer failed.
	HelperExitSend HelperExitStage = 39
)

// Code returns the process exit code for the stage.
func (s HelperExitStage) Code() int {
	return int(s)
}

// String returns a stable label used in bounded diagnostics.
func (s HelperExitStage) String() string {
	switch s {
	case HelperExitControlRead:
		return "controlRead"
	case HelperExitControlQueue:
		return "controlQueue"
	case HelperExitParentGone:
		return "parentGone"
	case HelperExitRuntime:
		return "runtime"
	case HelperExitTransport:
		return "transport"
	case HelperExitOpenDevices:
		return "openDevices"
	case HelperExitAudioIngress:
		return "audioIngress"
	case HelperExitAudioService:
		return "audioService"
	case HelperExitInspectAudio:
		return "inspectAudio"
	case HelperExitAudioControls:
		return "audioControls"
	case HelperExitReply:
		return "reply"
	case HelperExitShutdown:
		return "shutdown"
	case HelperExitControlSequence:
		return "controlSequence"
	case HelperExitPlayout:
		return "playout"
	case HelperExitRender:
		return "render"
	case HelperExitCapture:
		return "capture"
	case HelperExitDevice:
		return "device"
	case HelperExitSend:
		return "send"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// HelperExitStageFromCode maps a helper exit code back to its stage. Codes that
// are not part of the contract report false.
func HelperExitStageFromCode(code int) (HelperExitStage, bool) {
	switch HelperExitStage(code) {
	case HelperExitControlRead,
		HelperExitControlQueue,
		HelperExitParentGone,
		HelperExitRuntime,
		HelperExitTransport,
		HelperExitOpenDevices,
		HelperExitAudioIngress,
		HelperExitAudioService,
		HelperExitInspectAudio,
		HelperExitAudioControls,
		HelperExitReply,
		HelperExitShutdown,
		HelperExitControlSequence,
		HelperExitPlayout,
		HelperExitRender,
		HelperExitCapture,
		HelperExitDevice,
		HelperExitSend:
		return HelperExitStage(code), true
	default:
		return 0, false
	}
}

// HelperExitError carries the stage that failed so the process can exit with
// the same code the Rust helper uses and the parent can log a bounded phase.
type HelperExitError struct {
	Stage HelperExitStage
	Err   error
}

// Error implements error.
func (e *HelperExitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Stage.String()
	}
	return fmt.Sprintf("%s: %v", e.Stage.String(), e.Err)
}

// Unwrap exposes the underlying failure.
func (e *HelperExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// withExitStage tags a failure with the helper phase that produced it. A nil
// error stays nil so callers can wrap unconditionally.
func withExitStage(stage HelperExitStage, err error) error {
	if err == nil {
		return nil
	}
	return &HelperExitError{Stage: stage, Err: err}
}
