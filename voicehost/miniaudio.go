//go:build cgo

package voicehost

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

const (
	miniaudioChannels = 1
	miniaudioFormat   = malgo.FormatS16
)

// MiniAudioRuntime is the first Go-native audio runtime. It uses miniaudio for
// device enumeration, capture, and playback while the WebRTC transport remains
// independent.
type MiniAudioRuntime struct {
	mu      sync.Mutex
	context *malgo.AllocatedContext
	started bool
	devices map[*malgo.Device]struct{}
	// pipeline owns the bounded device buffers and the ordered mute epochs.
	pipeline *pcmPipeline
}

// NewMiniAudioRuntime returns an inactive miniaudio runtime.
func NewMiniAudioRuntime() *MiniAudioRuntime {
	return &MiniAudioRuntime{
		devices:  map[*malgo.Device]struct{}{},
		pipeline: newPCMPipeline(),
	}
}

// Name returns the runtime's stable identifier.
func (r *MiniAudioRuntime) Name() string {
	return "miniaudio"
}

// Start initializes the miniaudio context. It does not open a device.
func (r *MiniAudioRuntime) Start(ctx context.Context, config SessionConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return nil
	}
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("initialize miniaudio: %w", err)
	}
	r.context = context
	r.started = true
	r.mu.Unlock()
	r.resetPipeline()
	return nil
}

// Stop releases all open devices and the miniaudio context.
func (r *MiniAudioRuntime) Stop() error {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil
	}
	devices := make([]*malgo.Device, 0, len(r.devices))
	for device := range r.devices {
		devices = append(devices, device)
	}
	r.devices = map[*malgo.Device]struct{}{}
	context := r.context
	r.context = nil
	r.started = false
	r.mu.Unlock()

	for _, device := range devices {
		device.Uninit()
	}
	if context != nil {
		_ = context.Uninit()
		context.Free()
	}
	r.resetPipeline()
	return nil
}

// resetPipeline starts a fresh bounded buffer set so a later session cannot
// observe the previous session's audio or mute epochs.
func (r *MiniAudioRuntime) resetPipeline() {
	if r == nil {
		return
	}
	r.pipeline = newPCMPipeline()
}

// ListInputDevices returns capture devices visible to miniaudio.
func (r *MiniAudioRuntime) ListInputDevices(ctx context.Context) ([]Device, error) {
	return r.listDevices(ctx, malgo.Capture)
}

// ListOutputDevices returns playback devices visible to miniaudio.
func (r *MiniAudioRuntime) ListOutputDevices(ctx context.Context) ([]Device, error) {
	return r.listDevices(ctx, malgo.Playback)
}

// OpenInput opens a capture device and returns a blocking PCM source.
func (r *MiniAudioRuntime) OpenInput(ctx context.Context, deviceID string) (AudioSource, error) {
	return r.openSource(ctx, malgo.Capture, deviceID)
}

// OpenOutput opens a playback device and returns a PCM sink.
func (r *MiniAudioRuntime) OpenOutput(ctx context.Context, deviceID string) (AudioSink, error) {
	return r.openSink(ctx, malgo.Playback, deviceID)
}

// SetControls applies the ordered privacy snapshot by advancing the pipeline's
// mute epochs. Capture stops producing audible blocks while the microphone is
// muted; playback discards writes while the speaker is suppressed.
func (r *MiniAudioRuntime) SetControls(controls AudioControls) error {
	if r == nil || r.pipeline == nil {
		return errors.New("miniaudio runtime is required")
	}
	r.pipeline.setControls(controls)
	return nil
}

// AudioState returns the accumulated peaks and clears them, matching the Rust
// helper's take-on-read behaviour.
func (r *MiniAudioRuntime) AudioState() AudioState {
	if r == nil || r.pipeline == nil {
		return AudioState{}
	}
	return r.pipeline.takeState()
}

// renderPlayback fills one device output callback from the playback queue.
// Absent audio renders as silence so the device clock keeps advancing.
func renderPlayback(output []byte, pipeline *pcmPipeline) {
	if len(output) < 2 {
		clear(output)
		return
	}
	var state audioBlock
	for offset := 0; offset+1 < len(output); offset += 2 {
		sample, ok := pipeline.nextPlaybackSample(&state)
		if !ok {
			clear(output[offset:])
			return
		}
		binary.LittleEndian.PutUint16(output[offset:], uint16(sample))
	}
}

func (r *MiniAudioRuntime) listDevices(ctx context.Context, kind malgo.DeviceType) ([]Device, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	context := r.context
	if context == nil {
		return nil, ErrRuntimeNotInitialized
	}
	infos, err := context.Context.Devices(kind)
	if err != nil {
		return nil, fmt.Errorf("list miniaudio devices: %w", err)
	}
	devices := make([]Device, 0, len(infos))
	for _, info := range infos {
		devices = append(devices, Device{
			ID:        info.ID.String(),
			Name:      info.Name(),
			IsDefault: info.IsDefault != 0,
		})
	}
	return devices, nil
}

func (r *MiniAudioRuntime) openSource(ctx context.Context, kind malgo.DeviceType, deviceID string) (AudioSource, error) {
	device, buffer, format, err := r.openDevice(ctx, kind, deviceID, defaultSessionFormat)
	if err != nil {
		return nil, err
	}
	return &miniaudioSource{device: device, buffer: buffer, format: format, runtime: r, pipeline: r.currentPipeline()}, nil
}

func (r *MiniAudioRuntime) openSink(ctx context.Context, kind malgo.DeviceType, deviceID string) (AudioSink, error) {
	device, buffer, format, err := r.openDevice(ctx, kind, deviceID, defaultSessionFormat)
	if err != nil {
		return nil, err
	}
	return &miniaudioSink{device: device, buffer: buffer, format: format, runtime: r, pipeline: r.currentPipeline()}, nil
}

// currentPipeline returns the pipeline a device handle should bind to. The
// handle keeps that pointer for its whole lifetime so a later session reset
// cannot redirect in-flight callbacks.
func (r *MiniAudioRuntime) currentPipeline() *pcmPipeline {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pipeline
}

func (r *MiniAudioRuntime) openDevice(ctx context.Context, kind malgo.DeviceType, deviceID string, format AudioFormat) (*malgo.Device, *pcmBuffer, AudioFormat, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if format.SampleRate <= 0 || format.Channels <= 0 || format.Encoding != AudioEncodingS16LE {
		return nil, nil, AudioFormat{}, fmt.Errorf("unsupported miniaudio format %+v", format)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	context := r.context
	if context == nil {
		return nil, nil, AudioFormat{}, ErrRuntimeNotInitialized
	}

	config := malgo.DefaultDeviceConfig(kind)
	config.SampleRate = uint32(format.SampleRate)
	var nativeID *malgo.DeviceID
	// A selected device id is Go memory referenced from the C device config, so
	// it must stay pinned across ma_device_init; the cgo pointer check rejects
	// an unpinned Go pointer reachable from the config. The zero value of a
	// Pinner is ready to use, and Unpin is a no-op before any Pin.
	var pinner runtime.Pinner
	defer pinner.Unpin()
	if strings.TrimSpace(deviceID) != "" {
		decoded, err := decodeDeviceID(deviceID)
		if err != nil {
			return nil, nil, AudioFormat{}, err
		}
		nativeID = &decoded
		pinner.Pin(nativeID)
	}
	buffer := newPCMBuffer(pcmBufferBytes(format, 2*time.Second))
	// Bind the callback to the pipeline this device opened against, so a later
	// session reset cannot redirect in-flight callbacks.
	pipeline := r.pipeline
	switch kind {
	case malgo.Capture:
		config.Capture.Format = miniaudioFormat
		config.Capture.Channels = uint32(format.Channels)
		if nativeID != nil {
			config.Capture.DeviceID = unsafe.Pointer(nativeID)
		}
		device, err := initMiniAudioDevice(context.Context, config, malgo.DeviceCallbacks{
			Data: func(_, input []byte, _ uint32) {
				// Capture admits callbacks through the packer so only complete
				// blocks with a live generation reach the encoder.
				pipeline.pushCapture(s16leSamples(input), time.Now())
			},
		})
		if err != nil {
			return nil, nil, AudioFormat{}, fmt.Errorf("initialize miniaudio capture: %w", err)
		}
		if err := device.Start(); err != nil {
			device.Uninit()
			return nil, nil, AudioFormat{}, fmt.Errorf("start miniaudio capture: %w", err)
		}
		r.devices[device] = struct{}{}
		return device, buffer, format, nil
	case malgo.Playback:
		config.Playback.Format = miniaudioFormat
		config.Playback.Channels = uint32(format.Channels)
		if nativeID != nil {
			config.Playback.DeviceID = unsafe.Pointer(nativeID)
		}
		device, err := initMiniAudioDevice(context.Context, config, malgo.DeviceCallbacks{
			Data: func(output, _ []byte, _ uint32) {
				renderPlayback(output, pipeline)
			},
		})
		if err != nil {
			return nil, nil, AudioFormat{}, fmt.Errorf("initialize miniaudio playback: %w", err)
		}
		if err := device.Start(); err != nil {
			device.Uninit()
			return nil, nil, AudioFormat{}, fmt.Errorf("start miniaudio playback: %w", err)
		}
		r.devices[device] = struct{}{}
		return device, buffer, format, nil
	default:
		return nil, nil, AudioFormat{}, fmt.Errorf("unsupported miniaudio device type %d", kind)
	}
}

// initMiniAudioDevice wraps malgo.InitDevice so a native backend panic becomes a
// typed error. Some endpoint IDs make a backend panic inside device
// initialization; the helper must fail closed with an exit stage instead of
// crashing the process.
func initMiniAudioDevice(
	malgoContext malgo.Context,
	config malgo.DeviceConfig,
	callbacks malgo.DeviceCallbacks,
) (device *malgo.Device, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			device = nil
			err = fmt.Errorf("miniaudio device initialization panicked: %v", recovered)
		}
	}()
	return malgo.InitDevice(malgoContext, config, callbacks)
}

func (r *MiniAudioRuntime) releaseDevice(device *malgo.Device) {
	r.mu.Lock()
	_, ok := r.devices[device]
	delete(r.devices, device)
	r.mu.Unlock()
	if ok {
		device.Uninit()
	}
}

func decodeDeviceID(value string) (malgo.DeviceID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return malgo.DeviceID{}, nil
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return malgo.DeviceID{}, fmt.Errorf("invalid miniaudio device id %q: %w", value, err)
	}
	var deviceID malgo.DeviceID
	if len(decoded) > len(deviceID) {
		return malgo.DeviceID{}, errors.New("miniaudio device id is too long")
	}
	copy(deviceID[:], decoded)
	return deviceID, nil
}

func pcmBufferBytes(format AudioFormat, duration time.Duration) int {
	bytesPerSecond := format.SampleRate * format.Channels * 2
	bytes := int(duration.Seconds() * float64(bytesPerSecond))
	if bytes < 64*1024 {
		return 64 * 1024
	}
	return bytes
}

type pcmBuffer struct {
	mu     sync.Mutex
	data   []byte
	max    int
	notify chan struct{}
	closed bool
}

func newPCMBuffer(max int) *pcmBuffer {
	if max <= 0 {
		max = 64 * 1024
	}
	return &pcmBuffer{max: max, notify: make(chan struct{}, 1)}
}

func (b *pcmBuffer) Append(data []byte) {
	if b == nil || len(data) == 0 {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.data = append(b.data, data...)
	if len(b.data) > b.max {
		start := len(b.data) - b.max
		b.data = append([]byte(nil), b.data[start:]...)
	}
	b.mu.Unlock()
	b.signal()
}

func (b *pcmBuffer) Read(ctx context.Context, output []byte) (int, error) {
	if b == nil {
		return 0, io.EOF
	}
	if len(output) == 0 {
		return 0, nil
	}
	for {
		b.mu.Lock()
		if len(b.data) > 0 {
			count := copy(output, b.data)
			b.data = b.data[count:]
			b.mu.Unlock()
			return count, nil
		}
		if b.closed {
			b.mu.Unlock()
			return 0, io.EOF
		}
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-b.notify:
		}
	}
}

func (b *pcmBuffer) Drain(output []byte) int {
	if b == nil {
		clear(output)
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data) == 0 {
		clear(output)
		return 0
	}
	count := copy(output, b.data)
	b.data = b.data[count:]
	if count < len(output) {
		clear(output[count:])
	}
	return count
}

func (b *pcmBuffer) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		b.signal()
	}
	b.mu.Unlock()
}

func (b *pcmBuffer) signal() {
	select {
	case b.notify <- struct{}{}:
	default:
	}
}

type miniaudioSource struct {
	device    *malgo.Device
	buffer    *pcmBuffer
	format    AudioFormat
	runtime   *MiniAudioRuntime
	pipeline  *pcmPipeline
	closeOnce sync.Once
}

// Read returns the next packed capture block, blocking until one is available.
// While the microphone is muted no audible block is produced, so the caller
// keeps its own clock alive by sending generated silence.
func (s *miniaudioSource) Read(ctx context.Context) (Frame, error) {
	if s == nil {
		return Frame{}, io.EOF
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if block, ok := s.pipeline.readCapture(time.Now()); ok {
			return Frame{
				Data:       samplesS16LE(block.samples[:block.length]),
				Format:     s.format,
				Samples:    block.length,
				CapturedAt: block.at.UTC(),
			}, nil
		}
		if s.device == nil {
			return Frame{}, io.EOF
		}
		select {
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// Close releases the capture device.
func (s *miniaudioSource) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		s.buffer.Close()
		if s.device != nil {
			s.runtime.releaseDevice(s.device)
			s.device = nil
		}
	})
	return err
}

type miniaudioSink struct {
	device    *malgo.Device
	buffer    *pcmBuffer
	format    AudioFormat
	runtime   *MiniAudioRuntime
	pipeline  *pcmPipeline
	closeOnce sync.Once
}

// Write queues PCM for playback. Audio written while the speaker is suppressed
// is discarded so a later unmute cannot replay stale output.
func (s *miniaudioSink) Write(ctx context.Context, frame Frame) error {
	if s == nil {
		return io.EOF
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if !s.pipeline.sessionServiced() {
		// No media session is being serviced yet, so rendered output stays
		// silent rather than draining an unowned buffer.
		return nil
	}
	samples := s16leSamples(frame.Data)
	if len(samples) == 0 {
		return nil
	}
	at := frame.CapturedAt
	if at.IsZero() {
		at = time.Now()
	}
	for offset := 0; offset < len(samples); offset += audioBlockSamples {
		end := min(offset+audioBlockSamples, len(samples))
		var block audioBlock
		block.length = copy(block.samples[:], samples[offset:end])
		block.at = at.Add(time.Duration(int64(offset) * int64(time.Second) / 48000))
		s.pipeline.pushPlayback(block)
	}
	return nil
}

// Close releases the playback device.
func (s *miniaudioSink) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		s.buffer.Close()
		if s.device != nil {
			s.runtime.releaseDevice(s.device)
			s.device = nil
		}
	})
	return err
}

var _ Runtime = (*MiniAudioRuntime)(nil)
var _ ControlRuntime = (*MiniAudioRuntime)(nil)

// Pipeline exposes the bounded buffer set the opened devices were bound to.
func (r *MiniAudioRuntime) Pipeline() *pcmPipeline {
	return r.currentPipeline()
}

var _ MediaRuntime = (*MiniAudioRuntime)(nil)
