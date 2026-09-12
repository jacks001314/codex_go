package voicehost

// Packaged Opus codec. The Rust helper loads a private libopus through
// libloading; the Go helper does the same through a dynamic loader, so release
// builds stay CGO-free and the codec ships as a per-platform resource beside
// the helper.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

const (
	// opusApplicationVoip matches the Rust helper's encoder application.
	opusApplicationVoip = 2048
	// opusOK is the C API success code.
	opusOK = 0
)

// opusFrameSizes are the frame lengths the C encoder accepts at 48 kHz.
var opusFrameSizes = map[int]bool{120: true, 240: true, 480: true, 960: true, 1920: true, 2880: true}

// symbolResolver resolves one exported symbol from a loaded library.
type symbolResolver func(name string) (uintptr, error)

// OpusLibraryName is the packaged codec's file name for this platform.
func OpusLibraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "libopus.dll"
	case "darwin":
		return "libopus.0.dylib"
	default:
		return "libopus.so.0"
	}
}

// OpusLibraryPath returns the physical path of the packaged codec inside a
// package directory.
func OpusLibraryPath(packageDir string) string {
	return filepath.Join(packageDir, filepath.FromSlash(VoiceRuntimeDirectory), "lib", OpusLibraryName())
}

type opusEncoderCreateFunc func(fs int32, channels int32, application int32, err *int32) uintptr
type opusEncodeFloatFunc func(st uintptr, pcm *float32, frameSize int32, data *byte, maxBytes int32) int32
type opusDecoderCreateFunc func(fs int32, channels int32, err *int32) uintptr
type opusDecodeFloatFunc func(st uintptr, data *byte, length int32, pcm *float32, frameSize int32, decodeFEC int32) int32
type opusDestroyFunc func(st uintptr)

// opusCodec encodes and decodes monaural 48 kHz frames with a packaged libopus.
type opusCodec struct {
	handle uintptr

	encoderCreate  opusEncoderCreateFunc
	encodeFloat    opusEncodeFloatFunc
	decoderCreate  opusDecoderCreateFunc
	decodeFloat    opusDecodeFloatFunc
	encoderDestroy opusDestroyFunc
	decoderDestroy opusDestroyFunc

	mu       sync.Mutex
	encoder  uintptr
	decoder  uintptr
	scratch  []float32
	closed   bool
	initErr  error
	initOnce sync.Once
}

var _ VoiceCodec = (*opusCodec)(nil)

// OpenOpusCodec loads a codec from an explicit shared-library path.
func OpenOpusCodec(libraryPath string) (VoiceCodec, error) {
	if _, err := os.Stat(libraryPath); err != nil {
		return nil, fmt.Errorf("voice codec library is unavailable: %w", err)
	}
	handle, resolve, err := openDynamicLibrary(libraryPath)
	if err != nil {
		return nil, fmt.Errorf("load voice codec library: %w", err)
	}
	codec := &opusCodec{handle: handle, scratch: make([]float32, voiceFrameSamples)}
	bindings := []struct {
		target any
		name   string
	}{
		{&codec.encoderCreate, "opus_encoder_create"},
		{&codec.encodeFloat, "opus_encode_float"},
		{&codec.decoderCreate, "opus_decoder_create"},
		{&codec.decodeFloat, "opus_decode_float"},
		{&codec.encoderDestroy, "opus_encoder_destroy"},
		{&codec.decoderDestroy, "opus_decoder_destroy"},
	}
	for _, binding := range bindings {
		address, resolveErr := resolve(binding.name)
		if resolveErr != nil || address == 0 {
			return nil, fmt.Errorf("voice codec is missing %s", binding.name)
		}
		purego.RegisterFunc(binding.target, address)
	}
	return codec, nil
}

// LoadPackagedVoiceCodec loads the codec that ships beside the helper. It
// returns ErrVoiceCodecUnavailable when no codec is packaged, which leaves the
// control plane and device buffers running without media.
func LoadPackagedVoiceCodec(packageDir string) (VoiceCodec, error) {
	if packageDir == "" {
		return nil, ErrVoiceCodecUnavailable
	}
	codec, err := OpenOpusCodec(OpusLibraryPath(packageDir))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrVoiceCodecUnavailable, err)
	}
	return codec, nil
}

// Encoder reports the number of samples the last encode consumed.
func (c *opusCodec) ensureLocked() error {
	c.initOnce.Do(func() {
		var codecError int32
		c.encoder = c.encoderCreate(opusClockRate, 1, opusApplicationVoip, &codecError)
		if c.encoder == 0 || codecError != opusOK {
			c.initErr = fmt.Errorf("create voice encoder: code %d", codecError)
			return
		}
		c.decoder = c.decoderCreate(opusClockRate, 1, &codecError)
		if c.decoder == 0 || codecError != opusOK {
			c.initErr = fmt.Errorf("create voice decoder: code %d", codecError)
			return
		}
	})
	return c.initErr
}

// Encode converts one frame of monaural samples into an Opus payload.
func (c *opusCodec) Encode(samples []int16) ([]byte, error) {
	if c == nil {
		return nil, ErrVoiceCodecUnavailable
	}
	if !opusFrameSizes[len(samples)] {
		return nil, fmt.Errorf("voice codec received %d samples, which is not a valid frame size", len(samples))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrVoiceCodecUnavailable
	}
	if err := c.ensureLocked(); err != nil {
		return nil, err
	}
	if cap(c.scratch) < len(samples) {
		c.scratch = make([]float32, len(samples))
	}
	scratch := c.scratch[:len(samples)]
	for index, sample := range samples {
		scratch[index] = float32(sample) / 32768
	}
	payload := make([]byte, voiceSilenceBuffer)
	written := c.encodeFloat(c.encoder, &scratch[0], int32(len(samples)), &payload[0], int32(len(payload)))
	if written < 0 {
		return nil, fmt.Errorf("encode voice frame: code %d", written)
	}
	return payload[:written], nil
}

// Decode converts one Opus payload into monaural samples.
func (c *opusCodec) Decode(payload []byte) ([]int16, error) {
	if c == nil {
		return nil, ErrVoiceCodecUnavailable
	}
	if len(payload) == 0 {
		return nil, errors.New("voice codec received an empty payload")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrVoiceCodecUnavailable
	}
	if err := c.ensureLocked(); err != nil {
		return nil, err
	}
	scratch := make([]float32, voiceFrameSamples)
	decoded := c.decodeFloat(c.decoder, &payload[0], int32(len(payload)), &scratch[0], int32(voiceFrameSamples), 0)
	if decoded < 0 {
		return nil, fmt.Errorf("decode voice frame: code %d", decoded)
	}
	samples := make([]int16, decoded)
	for index, sample := range scratch[:decoded] {
		scaled := sample * 32768
		switch {
		case scaled > 32767:
			samples[index] = 32767
		case scaled < -32768:
			samples[index] = -32768
		default:
			samples[index] = int16(scaled)
		}
	}
	return samples, nil
}

// Close destroys the encoder and decoder.
func (c *opusCodec) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.encoder != 0 && c.encoderDestroy != nil {
		c.encoderDestroy(c.encoder)
		c.encoder = 0
	}
	if c.decoder != 0 && c.decoderDestroy != nil {
		c.decoderDestroy(c.decoder)
		c.decoder = 0
	}
	// Releasing the library keeps the codec file replaceable and drops the
	// mapping this process held.
	handle := c.handle
	c.handle = 0
	return closeDynamicLibrary(handle)
}
