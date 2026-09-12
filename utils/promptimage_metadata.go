package utils

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"io"
)

// Rust parity: codex-rs/utils/image `apply_image_metadata` plus the decoder's
// metadata extraction. Re-encoded prompt images keep the source's RGB ICC
// profile and EXIF payload (orientation); Go's stdlib encoders cannot write
// them, so the container chunks/segments are inserted after encoding.
type promptImageMetadata struct {
	iccProfile []byte
	exif       []byte
}

func (m promptImageMetadata) empty() bool {
	return len(m.iccProfile) == 0 && len(m.exif) == 0
}

// extractPromptImageMetadata reads the RGB ICC profile and EXIF payload from a
// decoded source container, mirroring Rust's decoder accessors.
func extractPromptImageMetadata(format promptImageFormat, payload []byte) promptImageMetadata {
	switch format {
	case promptImageFormatPNG:
		return extractPNGPromptImageMetadata(payload)
	case promptImageFormatJPEG:
		return extractJPEGPromptImageMetadata(payload)
	default:
		return promptImageMetadata{}
	}
}

// applyPromptImageMetadata writes the metadata into freshly encoded bytes.
func applyPromptImageMetadata(format promptImageFormat, encoded []byte, metadata promptImageMetadata) []byte {
	if metadata.empty() {
		return encoded
	}
	switch format {
	case promptImageFormatPNG:
		return insertPNGMetadata(encoded, metadata)
	case promptImageFormatJPEG:
		return insertJPEGMetadata(encoded, metadata)
	default:
		return encoded
	}
}

// rgbICCProfile mirrors Rust's filter: only RGB profiles are safe to copy across
// re-encoding paths (bytes 16..20 are the ICC data color space signature).
func rgbICCProfile(profile []byte) []byte {
	if len(profile) < 20 || string(profile[16:20]) != "RGB " {
		return nil
	}
	return profile
}

type pngChunk struct {
	kind string
	data []byte
}

func parsePNGChunks(payload []byte) ([]pngChunk, bool) {
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if len(payload) < len(signature) || !bytes.Equal(payload[:len(signature)], signature) {
		return nil, false
	}
	chunks := []pngChunk{}
	offset := len(signature)
	for offset+8 <= len(payload) {
		length := int(binary.BigEndian.Uint32(payload[offset : offset+4]))
		if length < 0 || offset+12+length > len(payload) {
			return nil, false
		}
		kind := string(payload[offset+4 : offset+8])
		data := payload[offset+8 : offset+8+length]
		chunks = append(chunks, pngChunk{kind: kind, data: data})
		offset += 12 + length
		if kind == "IEND" {
			break
		}
	}
	return chunks, true
}

func extractPNGPromptImageMetadata(payload []byte) promptImageMetadata {
	chunks, ok := parsePNGChunks(payload)
	if !ok {
		return promptImageMetadata{}
	}
	metadata := promptImageMetadata{}
	for _, chunk := range chunks {
		switch chunk.kind {
		case "iCCP":
			separator := bytes.IndexByte(chunk.data, 0)
			if separator < 0 || separator+2 > len(chunk.data) {
				continue
			}
			if method := chunk.data[separator+1]; method != 0 {
				continue
			}
			reader, err := zlib.NewReader(bytes.NewReader(chunk.data[separator+2:]))
			if err != nil {
				continue
			}
			profile, err := io.ReadAll(io.LimitReader(reader, MaxPromptImageInputBytes))
			_ = reader.Close()
			if err == nil {
				metadata.iccProfile = rgbICCProfile(profile)
			}
		case "eXIf":
			metadata.exif = append([]byte(nil), chunk.data...)
		}
	}
	return metadata
}

// insertPNGMetadata inserts iCCP/eXIf chunks after IHDR, matching the encoder
// metadata placement the image crate produces.
func insertPNGMetadata(encoded []byte, metadata promptImageMetadata) []byte {
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if len(encoded) < len(signature) || !bytes.Equal(encoded[:len(signature)], signature) {
		return encoded
	}
	insertAt := len(signature)
	if insertAt+8 <= len(encoded) && string(encoded[insertAt+4:insertAt+8]) == "IHDR" {
		length := int(binary.BigEndian.Uint32(encoded[insertAt : insertAt+4]))
		if insertAt+12+length <= len(encoded) {
			insertAt += 12 + length
		}
	}
	var out bytes.Buffer
	out.Write(encoded[:insertAt])
	if len(metadata.iccProfile) > 0 {
		var compressed bytes.Buffer
		writer := zlib.NewWriter(&compressed)
		_, _ = writer.Write(metadata.iccProfile)
		_ = writer.Close()
		payload := append([]byte("icc\x00\x00"), compressed.Bytes()...)
		out.Write(encodePNGChunk("iCCP", payload))
	}
	if len(metadata.exif) > 0 {
		out.Write(encodePNGChunk("eXIf", metadata.exif))
	}
	out.Write(encoded[insertAt:])
	return out.Bytes()
}

func encodePNGChunk(kind string, data []byte) []byte {
	var buf bytes.Buffer
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	buf.Write(length[:])
	buf.WriteString(kind)
	buf.Write(data)
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte(kind))
	_, _ = crc.Write(data)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	buf.Write(sum[:])
	return buf.Bytes()
}

// jpegSegments walks the marker segments before the entropy-coded data.
func jpegSegments(payload []byte) ([]jpegSegment, bool) {
	if len(payload) < 2 || payload[0] != 0xff || payload[1] != 0xd8 {
		return nil, false
	}
	segments := []jpegSegment{}
	offset := 2
	for offset+4 <= len(payload) {
		if payload[offset] != 0xff {
			return segments, true
		}
		marker := payload[offset+1]
		if marker == 0xd9 || marker == 0xda { // EOI / start of scan
			return segments, true
		}
		length := int(binary.BigEndian.Uint16(payload[offset+2 : offset+4]))
		if length < 2 || offset+2+length > len(payload) {
			return segments, true
		}
		segments = append(segments, jpegSegment{marker: marker, data: payload[offset+4 : offset+2+length]})
		offset += 2 + length
	}
	return segments, true
}

type jpegSegment struct {
	marker byte
	data   []byte
}

func extractJPEGPromptImageMetadata(payload []byte) promptImageMetadata {
	segments, ok := jpegSegments(payload)
	if !ok {
		return promptImageMetadata{}
	}
	metadata := promptImageMetadata{}
	var icc []byte
	for _, segment := range segments {
		switch segment.marker {
		case 0xe1:
			if len(segment.data) > 6 && bytes.Equal(segment.data[:6], []byte("Exif\x00\x00")) {
				metadata.exif = append([]byte(nil), segment.data[6:]...)
			}
		case 0xe2:
			if len(segment.data) > 14 && bytes.Equal(segment.data[:12], []byte("ICC_PROFILE\x00")) {
				icc = append(icc, segment.data[14:]...)
			}
		}
	}
	metadata.iccProfile = rgbICCProfile(icc)
	return metadata
}

// insertJPEGMetadata writes APP1 (Exif) and APP2 (ICC) segments right after SOI.
func insertJPEGMetadata(encoded []byte, metadata promptImageMetadata) []byte {
	if len(encoded) < 2 || encoded[0] != 0xff || encoded[1] != 0xd8 {
		return encoded
	}
	var out bytes.Buffer
	out.Write(encoded[:2])
	if len(metadata.exif) > 0 {
		out.Write(encodeJPEGSegment(0xe1, append([]byte("Exif\x00\x00"), metadata.exif...)))
	}
	if len(metadata.iccProfile) > 0 {
		const maxChunk = 65519
		chunks := (len(metadata.iccProfile) + maxChunk - 1) / maxChunk
		if chunks == 0 {
			chunks = 1
		}
		for index := 0; index < chunks; index++ {
			start := index * maxChunk
			end := start + maxChunk
			if end > len(metadata.iccProfile) {
				end = len(metadata.iccProfile)
			}
			payload := []byte("ICC_PROFILE\x00")
			payload = append(payload, byte(index+1), byte(chunks))
			payload = append(payload, metadata.iccProfile[start:end]...)
			out.Write(encodeJPEGSegment(0xe2, payload))
		}
	}
	out.Write(encoded[2:])
	return out.Bytes()
}

func encodeJPEGSegment(marker byte, payload []byte) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0xff, marker})
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(payload)+2))
	buf.Write(length[:])
	buf.Write(payload)
	return buf.Bytes()
}
