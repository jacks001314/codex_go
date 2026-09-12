package tui

import "bytes"

// Rust parity: codex-rs/tui/src/terminal_probe/windows_replay.rs. Terminal color
// replies are identified as byte ranges so the Windows probe can map them back
// to their console records and replay everything else untouched.
const maxTerminalColorResponseBytes = 1024

var (
	terminalPasteStart = []byte("\x1b[200~")
	terminalPasteEnd   = []byte("\x1b[201~")
)

// terminalColorResponseRanges returns the byte ranges occupied by complete,
// valid OSC 10/11 color responses, skipping bracketed-paste payloads.
func terminalColorResponseRanges(input []byte) [][2]int {
	ranges := [][2]int{}
	cursor := 0
	for cursor < len(input) {
		if bytes.HasPrefix(input[cursor:], terminalPasteStart) {
			payloadStart := cursor + len(terminalPasteStart)
			offset := bytes.Index(input[payloadStart:], terminalPasteEnd)
			if offset < 0 {
				break
			}
			cursor = payloadStart + offset + len(terminalPasteEnd)
			continue
		}
		if !bytes.HasPrefix(input[cursor:], []byte("\x1b]")) {
			cursor++
			continue
		}
		start := cursor
		prefix, _, ok := terminalColorResponsePrefix(input[start:])
		if !ok {
			cursor = start + 2
			continue
		}
		payloadStart := start + len(prefix)
		boundedEnd := len(input)
		if limit := start + maxTerminalColorResponseBytes; limit < boundedEnd {
			boundedEnd = limit
		}
		payloadLen, ok := oscPayloadEnd(input[payloadStart:boundedEnd])
		if !ok {
			cursor = payloadStart
			continue
		}
		terminatorLen := 1
		if input[payloadStart+payloadLen] == 0x1b {
			terminatorLen = 2
		}
		end := payloadStart + payloadLen + terminatorLen
		if _, ok := ParseOSCRGB(string(input[payloadStart : payloadStart+payloadLen])); ok {
			ranges = append(ranges, [2]int{start, end})
		}
		cursor = end
	}
	return ranges
}

func terminalColorResponsePrefix(response []byte) ([]byte, byte, bool) {
	for _, candidate := range []struct {
		prefix []byte
		slot   byte
	}{
		{[]byte("\x1b]10;"), 10},
		{[]byte("\x1b]11;"), 11},
	} {
		if bytes.HasPrefix(response, candidate.prefix) {
			return candidate.prefix, candidate.slot, true
		}
	}
	return nil, 0, false
}

// terminalDefaultColorsFromResponses mirrors Rust's windows_replay
// terminal_default_colors: both the foreground and background replies must be
// present.
func terminalDefaultColorsFromResponses(input []byte) (DefaultColors, bool) {
	var foreground *RGBColor
	var background *RGBColor
	for _, span := range terminalColorResponseRanges(input) {
		response := input[span[0]:span[1]]
		prefix, slot, ok := terminalColorResponsePrefix(response)
		if !ok {
			continue
		}
		payloadEnd, ok := oscPayloadEnd(response[len(prefix):])
		if !ok {
			continue
		}
		color, ok := ParseOSCRGB(string(response[len(prefix) : len(prefix)+payloadEnd]))
		if !ok {
			continue
		}
		switch slot {
		case 10:
			if foreground == nil {
				value := color
				foreground = &value
			}
		case 11:
			if background == nil {
				value := color
				background = &value
			}
		}
	}
	if foreground == nil || background == nil {
		return DefaultColors{}, false
	}
	return DefaultColors{FG: *foreground, BG: *background}, true
}
