package audioutil

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
)

// DurationSeconds decodes the audio container behind a data URL and returns its
// duration. It mirrors the Rust symphonia probe for the supported formats.
func DurationSeconds(audioURL string) (float64, bool) {
	metadata, payload, ok := strings.Cut(audioURL, ",")
	if !ok || !IsDataURL(audioURL) {
		return 0, false
	}
	parts := strings.Split(metadata[len("data:"):], ";")
	if len(parts) == 0 {
		return 0, false
	}
	canonicalMIME, ok := CanonicalAudioMIME(strings.TrimSpace(parts[0]))
	if !ok {
		return 0, false
	}
	base64Encoded := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			base64Encoded = true
		}
	}
	if !base64Encoded {
		return 0, false
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(data) == 0 {
		return 0, false
	}
	return durationForMIME(canonicalMIME, data)
}

func durationForMIME(mime string, data []byte) (float64, bool) {
	switch mime {
	case "audio/wav":
		return wavDuration(data)
	case "audio/mpeg":
		return mp3Duration(data)
	case "audio/mp4":
		return mp4Duration(data)
	case "audio/webm":
		return webmDuration(data)
	case "audio/ogg":
		return oggDuration(data)
	default:
		return 0, false
	}
}

// wavDuration reads the RIFF byte rate and the data chunk size.
func wavDuration(data []byte) (float64, bool) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, false
	}
	var byteRate uint32
	var dataSize uint32
	offset := 12
	for offset+8 <= len(data) {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		switch id {
		case "fmt ":
			if body+16 <= len(data) {
				byteRate = binary.LittleEndian.Uint32(data[body+8 : body+12])
			}
		case "data":
			if size > len(data)-body {
				size = len(data) - body
			}
			dataSize = uint32(size)
		}
		if size < 0 {
			return 0, false
		}
		offset = body + size
		if size%2 == 1 {
			offset++
		}
	}
	if byteRate == 0 || dataSize == 0 {
		return 0, false
	}
	return float64(dataSize) / float64(byteRate), true
}

var (
	mp3BitrateV1L3 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mp3BitrateV2L3 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
)

// mp3Duration finds the first MPEG frame, then uses the Xing/Info frame count
// when present, otherwise estimates a constant-bitrate stream.
func mp3Duration(data []byte) (float64, bool) {
	offset := 0
	for offset+4 <= len(data) {
		if data[offset] == 0xFF && data[offset+1]&0xE0 == 0xE0 && (data[offset+1]>>1)&0x03 == 0x01 {
			break
		}
		offset++
	}
	if offset+4 > len(data) {
		return 0, false
	}
	header := data[offset : offset+4]
	versionBits := (header[1] >> 3) & 0x03
	if versionBits == 0x01 {
		return 0, false // reserved
	}
	bitrateIndex := int(header[2] >> 4)
	sampleRateIndex := int(header[2] >> 2 & 0x03)
	channelMode := header[3] >> 6
	if bitrateIndex == 0 || bitrateIndex == 15 || sampleRateIndex == 3 {
		return 0, false
	}
	mpeg1 := versionBits == 0x03
	bitrate := mp3BitrateV2L3[bitrateIndex] * 1000
	sampleRate := 0
	samplesPerFrame := 576
	if mpeg1 {
		bitrate = mp3BitrateV1L3[bitrateIndex] * 1000
		samplesPerFrame = 1152
	}
	switch versionBits {
	case 0x03:
		sampleRate = []int{44100, 48000, 32000}[sampleRateIndex]
	case 0x02:
		sampleRate = []int{22050, 24000, 16000}[sampleRateIndex]
	default:
		sampleRate = []int{11025, 12000, 8000}[sampleRateIndex]
	}
	if bitrate == 0 || sampleRate == 0 {
		return 0, false
	}
	// Xing/Info sits after the side information.
	sideInfo := 32
	if mpeg1 {
		if channelMode == 3 {
			sideInfo = 17
		}
	} else if channelMode == 3 {
		sideInfo = 9
	}
	tagOffset := offset + 4 + sideInfo
	if tagOffset+12 <= len(data) {
		tag := string(data[tagOffset : tagOffset+4])
		if tag == "Xing" || tag == "Info" {
			frames := binary.BigEndian.Uint32(data[tagOffset+8 : tagOffset+12])
			if frames > 0 {
				return float64(frames) * float64(samplesPerFrame) / float64(sampleRate), true
			}
		}
	}
	audioBytes := len(data) - offset
	if audioBytes <= 0 {
		return 0, false
	}
	return float64(audioBytes) * 8 / float64(bitrate), true
}

// mp4Duration walks the box tree to the movie header (mvhd).
func mp4Duration(data []byte) (float64, bool) {
	timescale, duration, ok := findMovieHeader(data, 0, len(data), 0)
	if !ok || timescale == 0 {
		return 0, false
	}
	return float64(duration) / float64(timescale), true
}

func findMovieHeader(data []byte, start, end, depth int) (uint32, uint64, bool) {
	if depth > 4 {
		return 0, 0, false
	}
	offset := start
	for offset+8 <= end {
		size := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		boxType := string(data[offset+4 : offset+8])
		headerSize := uint64(8)
		if size == 1 {
			if offset+16 > end {
				return 0, 0, false
			}
			size = binary.BigEndian.Uint64(data[offset+8 : offset+16])
			headerSize = 16
		} else if size == 0 {
			size = uint64(end - offset)
		}
		if size < headerSize || uint64(offset)+size > uint64(end) {
			return 0, 0, false
		}
		body := offset + int(headerSize)
		boxEnd := offset + int(size)
		switch boxType {
		case "mvhd":
			return parseMovieHeader(data, body, boxEnd)
		case "moov", "trak", "mdia":
			if timescale, duration, ok := findMovieHeader(data, body, boxEnd, depth+1); ok {
				return timescale, duration, true
			}
		}
		offset = boxEnd
	}
	return 0, 0, false
}

func parseMovieHeader(data []byte, body, end int) (uint32, uint64, bool) {
	if body+4 > end {
		return 0, 0, false
	}
	version := data[body]
	if version == 1 {
		if body+32 > end {
			return 0, 0, false
		}
		timescale := binary.BigEndian.Uint32(data[body+20 : body+24])
		duration := binary.BigEndian.Uint64(data[body+24 : body+32])
		return timescale, duration, true
	}
	if body+20 > end {
		return 0, 0, false
	}
	timescale := binary.BigEndian.Uint32(data[body+12 : body+16])
	duration := uint64(binary.BigEndian.Uint32(data[body+16 : body+20]))
	return timescale, duration, true
}

// webmDuration reads the EBML Segment/Info duration and timecode scale.
func webmDuration(data []byte) (float64, bool) {
	timecodeScale := uint64(1_000_000)
	var duration float64
	found := false
	iterateEBML(data, 0, len(data), func(id uint64, body []byte) bool {
		switch id {
		case 0x18538067: // Segment
			iterateEBML(body, 0, len(body), func(childID uint64, childBody []byte) bool {
				if childID != 0x1549A966 { // Info
					return true
				}
				iterateEBML(childBody, 0, len(childBody), func(infoID uint64, infoBody []byte) bool {
					switch infoID {
					case 0x2AD7B1: // TimecodeScale
						timecodeScale = readUint(infoBody)
					case 0x4489: // Duration (float)
						duration = readFloat(infoBody)
						found = true
					}
					return true
				})
				return true
			})
			return false
		default:
			return true
		}
	})
	if !found || timecodeScale == 0 {
		return 0, false
	}
	seconds := duration * float64(timecodeScale) / 1e9
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return 0, false
	}
	return seconds, true
}

// iterateEBML walks EBML elements in a byte range, calling visit for each. When
// visit returns false the walk stops.
func iterateEBML(data []byte, start, end int, visit func(id uint64, body []byte) bool) {
	offset := start
	for offset < end {
		id, idLength, ok := readEBMLID(data[offset:end])
		if !ok {
			return
		}
		size, sizeLength, ok := readEBMLSize(data[offset+idLength : end])
		if !ok {
			return
		}
		bodyStart := offset + idLength + sizeLength
		bodyEnd := bodyStart + int(size)
		if bodyEnd > end {
			return
		}
		if !visit(id, data[bodyStart:bodyEnd]) {
			return
		}
		offset = bodyEnd
	}
}

func readEBMLID(data []byte) (uint64, int, bool) {
	if len(data) == 0 {
		return 0, 0, false
	}
	length := ebmlLength(data[0])
	if length == 0 || length > len(data) {
		return 0, 0, false
	}
	var value uint64
	for index := 0; index < length; index++ {
		value = value<<8 | uint64(data[index])
	}
	return value, length, true
}

func readEBMLSize(data []byte) (uint64, int, bool) {
	if len(data) == 0 {
		return 0, 0, false
	}
	length := ebmlLength(data[0])
	if length == 0 || length > len(data) {
		return 0, 0, false
	}
	value := uint64(data[0]) & ((1 << (8 - length)) - 1)
	for index := 1; index < length; index++ {
		value = value<<8 | uint64(data[index])
	}
	return value, length, true
}

func ebmlLength(first byte) int {
	for index := 0; index < 8; index++ {
		if first&(0x80>>index) != 0 {
			return index + 1
		}
	}
	return 0
}

func readUint(data []byte) uint64 {
	var value uint64
	for _, b := range data {
		value = value<<8 | uint64(b)
	}
	return value
}

func readFloat(data []byte) float64 {
	switch len(data) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(data)))
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(data))
	default:
		return 0
	}
}

// oggDuration reads the last page's granule position and the codec sample rate.
func oggDuration(data []byte) (float64, bool) {
	sampleRate := 0.0
	preSkip := 0.0
	lastGranule := uint64(0)
	seenPage := false
	offset := 0
	for offset+27 <= len(data) {
		if string(data[offset:offset+4]) != "OggS" {
			break
		}
		granule := binary.LittleEndian.Uint64(data[offset+6 : offset+14])
		segments := int(data[offset+26])
		if offset+27+segments > len(data) {
			break
		}
		bodySize := 0
		for index := 0; index < segments; index++ {
			bodySize += int(data[offset+27+index])
		}
		body := offset + 27 + segments
		if body+bodySize > len(data) {
			break
		}
		if !seenPage {
			payload := data[body : body+bodySize]
			switch {
			case len(payload) >= 12 && string(payload[0:8]) == "OpusHead":
				sampleRate = 48000
				preSkip = float64(binary.LittleEndian.Uint16(payload[10:12]))
			case len(payload) >= 16 && payload[0] == 1 && string(payload[1:7]) == "vorbis":
				sampleRate = float64(binary.LittleEndian.Uint32(payload[12:16]))
			}
			seenPage = true
		}
		if granule != ^uint64(0) {
			lastGranule = granule
		}
		offset = body + bodySize
	}
	if sampleRate == 0 || lastGranule == 0 {
		return 0, false
	}
	seconds := (float64(lastGranule) - preSkip) / sampleRate
	if seconds < 0 {
		return 0, false
	}
	return seconds, true
}
