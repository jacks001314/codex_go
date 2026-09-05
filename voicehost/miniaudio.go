package voicehost

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
}

// NewMiniAudioRuntime returns an inactive miniaudio runtime.
func NewMiniAudioRuntime() *MiniAudioRuntime {
	return &MiniAudioRuntime{devices: map[*malgo.Device]struct{}{}}
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
	defer r.mu.Unlock()
	if r.started {
		return nil
	}
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("initialize miniaudio: %w", err)
	}
	r.context = context
	r.started = true
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
	return nil
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
	return &miniaudioSource{device: device, buffer: buffer, format: format, runtime: r}, nil
}

func (r *MiniAudioRuntime) openSink(ctx context.Context, kind malgo.DeviceType, deviceID string) (AudioSink, error) {
	device, buffer, format, err := r.openDevice(ctx, kind, deviceID, defaultSessionFormat)
	if err != nil {
		return nil, err
	}
	return &miniaudioSink{device: device, buffer: buffer, format: format, runtime: r}, nil
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
	if strings.TrimSpace(deviceID) != "" {
		decoded, err := decodeDeviceID(deviceID)
		if err != nil {
			return nil, nil, AudioFormat{}, err
		}
		nativeID = &decoded
	}
	buffer := newPCMBuffer(pcmBufferBytes(format, 2*time.Second))
	switch kind {
	case malgo.Capture:
		config.Capture.Format = miniaudioFormat
		config.Capture.Channels = uint32(format.Channels)
		if nativeID != nil {
			config.Capture.DeviceID = unsafe.Pointer(nativeID)
		}
		device, err := malgo.InitDevice(context.Context, config, malgo.DeviceCallbacks{
			Data: func(_, input []byte, _ uint32) {
				buffer.Append(input)
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
		device, err := malgo.InitDevice(context.Context, config, malgo.DeviceCallbacks{
			Data: func(output, _ []byte, _ uint32) {
				buffer.Drain(output)
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
	closeOnce sync.Once
}

// Read returns the next available PCM frame, blocking until data arrives.
func (s *miniaudioSource) Read(ctx context.Context) (Frame, error) {
	if s == nil {
		return Frame{}, io.EOF
	}
	if ctx == nil {
		ctx = context.Background()
	}
	frameBytes := s.format.SampleRate / 50 * s.format.Channels * 2
	if frameBytes <= 0 {
		frameBytes = 960
	}
	data := make([]byte, frameBytes)
	count, err := s.buffer.Read(ctx, data)
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Data:       data[:count],
		Format:     s.format,
		Samples:    count / (s.format.Channels * 2),
		CapturedAt: time.Now().UTC(),
	}, nil
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
	closeOnce sync.Once
}

// Write queues PCM for playback.
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
	s.buffer.Append(frame.Data)
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
