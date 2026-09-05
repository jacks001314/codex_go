package voicehost

import (
	"context"
	"errors"
	"time"
)

// ErrRuntimeNotInitialized is returned when an audio device is opened before
// Start or after Stop.
var ErrRuntimeNotInitialized = errors.New("voice runtime is not initialized")

// AudioEncoding identifies the sample encoding carried by a frame.
type AudioEncoding string

const (
	// AudioEncodingS16LE is signed 16-bit little-endian PCM, the common
	// interchange format used by native capture and playback paths.
	AudioEncodingS16LE AudioEncoding = "s16le"
)

// AudioFormat describes a PCM stream.
type AudioFormat struct {
	SampleRate int
	Channels   int
	Encoding   AudioEncoding
}

// Frame is one decoded or captured audio buffer. Data is always PCM; transport
// encoding (for example Opus) is handled outside this interface.
type Frame struct {
	Data       []byte
	Format     AudioFormat
	Samples    int
	CapturedAt time.Time
}

// Device describes an input or output audio device.
type Device struct {
	ID        string
	Name      string
	IsDefault bool
}

// AudioSource reads captured PCM frames.
type AudioSource interface {
	Read(ctx context.Context) (Frame, error)
	Close() error
}

// AudioSink writes PCM frames for playback.
type AudioSink interface {
	Write(ctx context.Context, frame Frame) error
	Close() error
}

// SessionConfig contains the transport-level settings required to start a
// runtime. It deliberately excludes backend credentials and conversation state.
type SessionConfig struct {
	InputDeviceID  string
	OutputDeviceID string
	Format         AudioFormat
}

// Runtime owns native audio capture and playback for one process. A runtime is
// expected to start once, expose devices while active, and release all native
// resources when Stop returns.
type Runtime interface {
	Name() string
	Start(ctx context.Context, config SessionConfig) error
	Stop() error
	ListInputDevices(ctx context.Context) ([]Device, error)
	ListOutputDevices(ctx context.Context) ([]Device, error)
	OpenInput(ctx context.Context, deviceID string) (AudioSource, error)
	OpenOutput(ctx context.Context, deviceID string) (AudioSink, error)
}

// NullRuntime is a Runtime implementation for lifecycle-only hosts. It exposes
// no devices and rejects audio opens, matching a helper-only voice package.
type NullRuntime struct{}

// Name returns the runtime's stable identifier.
func (NullRuntime) Name() string {
	return "null"
}

// Start marks the runtime active without acquiring native resources.
func (NullRuntime) Start(context.Context, SessionConfig) error {
	return nil
}

// Stop marks the runtime inactive.
func (NullRuntime) Stop() error {
	return nil
}

// ListInputDevices returns no devices.
func (NullRuntime) ListInputDevices(context.Context) ([]Device, error) {
	return []Device{}, nil
}

// ListOutputDevices returns no devices.
func (NullRuntime) ListOutputDevices(context.Context) ([]Device, error) {
	return []Device{}, nil
}

// OpenInput rejects all devices.
func (NullRuntime) OpenInput(context.Context, string) (AudioSource, error) {
	return nil, ErrRuntimeNotInitialized
}

// OpenOutput rejects all devices.
func (NullRuntime) OpenOutput(context.Context, string) (AudioSink, error) {
	return nil, ErrRuntimeNotInitialized
}

var _ Runtime = NullRuntime{}
