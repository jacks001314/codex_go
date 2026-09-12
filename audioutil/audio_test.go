package audioutil

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
)

func encodeAudioURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func TestPrepareAudioURLCanonicalizesAndRejectsRemote(t *testing.T) {
	canonical, err := PrepareAudioURL("data:audio/x-wav;base64,YXVkaW8=")
	if err != nil {
		t.Fatalf("canonical wav: %v", err)
	}
	if canonical != "data:audio/wav;base64,YXVkaW8=" {
		t.Fatalf("canonical wav = %q", canonical)
	}
	unchanged, err := PrepareAudioURL("data:audio/ogg;base64,YXVkaW8=")
	if err != nil {
		t.Fatalf("canonical ogg: %v", err)
	}
	if unchanged != "data:audio/ogg;base64,YXVkaW8=" {
		t.Fatalf("canonical ogg = %q", unchanged)
	}
	if _, err := PrepareAudioURL("https://example.com/audio.mp3"); !errors.Is(err, ErrInvalidDataURL) {
		t.Fatalf("remote URL error = %v, want invalid data URL", err)
	}
	if Placeholder(errors.New("x")) != PlaceholderProcessingError {
		t.Fatalf("unknown error placeholder = %q", Placeholder(errors.New("x")))
	}
}

func TestPrepareAudioURLPlaceholders(t *testing.T) {
	_, err := PrepareAudioURL("data:audio/wav;base64,%%%")
	if !errors.Is(err, ErrInvalidDataURL) || Placeholder(err) != PlaceholderProcessingError {
		t.Fatalf("invalid base64 -> %v / %q", err, Placeholder(err))
	}
	_, err = PrepareAudioURL("data:audio/flac;base64,YXVkaW8=")
	if !errors.Is(err, ErrUnsupportedFormat) || Placeholder(err) != PlaceholderUnsupported {
		t.Fatalf("unsupported format -> %v / %q", err, Placeholder(err))
	}
	_, err = PrepareAudioURL("data:audio/wav;base64,")
	if !errors.Is(err, ErrInvalidDataURL) {
		t.Fatalf("empty payload -> %v, want invalid", err)
	}
	oversized := "data:audio/wav;base64," + strings.Repeat("A", maxPromptAudioBase64Bytes+4)
	_, err = PrepareAudioURL(oversized)
	if !errors.Is(err, ErrAudioTooLarge) || Placeholder(err) != PlaceholderTooLarge {
		t.Fatalf("oversized -> %v / %q", err, Placeholder(err))
	}
	_, err = PrepareAudioURL("data:audio/wav,YXVkaW8=")
	if !errors.Is(err, ErrInvalidDataURL) {
		t.Fatalf("non-base64 payload -> %v, want invalid", err)
	}
}

func TestEstimateAudioTokenCountUsesDuration(t *testing.T) {
	// 2 seconds of 8 kHz mono 16-bit PCM.
	url := encodeAudioURL("audio/wav", wavBytes(16000, 32000))
	got := EstimateAudioTokenCount(url)
	if got != 20 {
		t.Fatalf("token estimate = %d, want 20 (2 s * 10 tokens/s)", got)
	}
}

func TestEstimateAudioTokenCountFallsBackToByteSize(t *testing.T) {
	url := encodeAudioURL("audio/wav", []byte("not really audio"))
	got := EstimateAudioTokenCount(url)
	want := (len(url) + 3) / 4
	if got != want {
		t.Fatalf("fallback estimate = %d, want %d", got, want)
	}
}

func TestDurationSecondsPerFormat(t *testing.T) {
	cases := []struct {
		name string
		mime string
		data []byte
		want float64
	}{
		{"wav", "audio/wav", wavBytes(16000, 32000), 2.0},
		{"mp3", "audio/mpeg", mp3Bytes(16000), 1.0},
		{"mp4", "audio/mp4", mp4Bytes(1000, 2500), 2.5},
		{"ogg", "audio/ogg", oggBytes(), 2.0},
		{"webm", "audio/webm", webmBytes(1_000_000, 2000), 2.0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			url := encodeAudioURL(testCase.mime, testCase.data)
			got, ok := DurationSeconds(url)
			if !ok {
				t.Fatalf("%s duration not detected", testCase.name)
			}
			if math.Abs(got-testCase.want) > 0.01 {
				t.Fatalf("%s duration = %.4f, want %.4f", testCase.name, got, testCase.want)
			}
		})
	}
}

func TestDurationSecondsRejectsMalformed(t *testing.T) {
	if _, ok := DurationSeconds("data:audio/wav;base64,!!!"); ok {
		t.Fatal("malformed base64 reported a duration")
	}
	if _, ok := DurationSeconds("https://example.com/a.wav"); ok {
		t.Fatal("remote URL reported a duration")
	}
}

func wavBytes(byteRate, dataSize uint32) []byte {
	buffer := make([]byte, 0, 44+int(dataSize))
	buffer = append(buffer, "RIFF"...)
	buffer = appendUint32LE(buffer, 36+dataSize)
	buffer = append(buffer, "WAVE"...)
	buffer = append(buffer, "fmt "...)
	buffer = appendUint32LE(buffer, 16)
	buffer = appendUint16LE(buffer, 1)
	buffer = appendUint16LE(buffer, 1)
	buffer = appendUint32LE(buffer, 8000)
	buffer = appendUint32LE(buffer, byteRate)
	buffer = appendUint16LE(buffer, 2)
	buffer = appendUint16LE(buffer, 16)
	buffer = append(buffer, "data"...)
	buffer = appendUint32LE(buffer, dataSize)
	buffer = append(buffer, make([]byte, dataSize)...)
	return buffer
}

// mp3Bytes builds a single MPEG1 Layer3 128 kbps 44100 Hz stereo frame header
// followed by padding; the parser falls back to the constant-bitrate estimate.
func mp3Bytes(total int) []byte {
	buffer := make([]byte, total)
	copy(buffer, []byte{0xFF, 0xFB, 0x90, 0x00})
	return buffer
}

func mp4Bytes(timescale, duration uint32) []byte {
	mvhdPayload := make([]byte, 0, 20)
	mvhdPayload = appendUint32BE(mvhdPayload, 0) // version + flags
	mvhdPayload = appendUint32BE(mvhdPayload, 0) // creation
	mvhdPayload = appendUint32BE(mvhdPayload, 0) // modification
	mvhdPayload = appendUint32BE(mvhdPayload, timescale)
	mvhdPayload = appendUint32BE(mvhdPayload, duration)
	mvhd := box("mvhd", mvhdPayload)
	moov := box("moov", mvhd)
	ftyp := box("ftyp", append([]byte("isom"), 0, 0, 0, 0))
	return append(ftyp, moov...)
}

func box(boxType string, payload []byte) []byte {
	out := make([]byte, 0, 8+len(payload))
	out = appendUint32BE(out, uint32(8+len(payload)))
	out = append(out, boxType...)
	return append(out, payload...)
}

func oggBytes() []byte {
	opusHead := []byte{'O', 'p', 'u', 's', 'H', 'e', 'a', 'd', 1, 1, 0, 0, 0x80, 0xBB, 0, 0, 0, 0, 0}
	first := oggPage(0x02, 0, opusHead)
	second := oggPage(0x04, 96000, nil)
	return append(first, second...)
}

func oggPage(headerType byte, granule uint64, payload []byte) []byte {
	segments := 0
	if len(payload) > 0 {
		segments = 1
	}
	page := make([]byte, 0, 27+segments+len(payload))
	page = append(page, 'O', 'g', 'g', 'S', 0, headerType)
	granuleBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(granuleBytes, granule)
	page = append(page, granuleBytes...)
	page = appendUint32LE(page, 1) // serial
	page = appendUint32LE(page, 0) // sequence
	page = appendUint32LE(page, 0) // crc
	page = append(page, byte(segments))
	if segments > 0 {
		page = append(page, byte(len(payload)))
	}
	return append(page, payload...)
}

func webmBytes(timecodeScale uint64, duration float32) []byte {
	scale := encodeEBMLUint(0x2AD7B1, timecodeScale)
	durationBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(durationBytes, math.Float32bits(duration))
	durationElement := encodeEBMLRaw(0x4489, durationBytes)
	info := encodeEBMLRaw(0x1549A966, append(scale, durationElement...))
	segment := encodeEBMLRaw(0x18538067, info)
	return segment
}

func encodeEBMLUint(id uint64, value uint64) []byte {
	body := make([]byte, 0, 8)
	for shift := 56; shift >= 0; shift -= 8 {
		current := byte(value >> uint(shift))
		if len(body) == 0 && current == 0 {
			continue
		}
		body = append(body, current)
	}
	if len(body) == 0 {
		body = []byte{0}
	}
	return encodeEBMLRaw(id, body)
}

func encodeEBMLRaw(id uint64, body []byte) []byte {
	encoded := encodeEBMLID(id)
	// Single-byte size marker for bodies below 128 bytes; tests stay small.
	return append(append(encoded, byte(0x80|len(body))), body...)
}

func encodeEBMLID(id uint64) []byte {
	width := 1
	for id >= (uint64(1) << (8 * width)) {
		width++
	}
	out := make([]byte, width)
	for index := width - 1; index >= 0; index-- {
		out[index] = byte(id & 0xFF)
		id >>= 8
	}
	return out
}

func appendUint32LE(buffer []byte, value uint32) []byte {
	encoded := make([]byte, 4)
	binary.LittleEndian.PutUint32(encoded, value)
	return append(buffer, encoded...)
}

func appendUint32BE(buffer []byte, value uint32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, value)
	return append(buffer, encoded...)
}

func appendUint16LE(buffer []byte, value uint16) []byte {
	encoded := make([]byte, 2)
	binary.LittleEndian.PutUint16(encoded, value)
	return append(buffer, encoded...)
}
