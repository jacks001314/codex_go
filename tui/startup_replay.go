package tui

import (
	"bytes"
	"strconv"
)

// Rust parity: codex-rs/tui/src/terminal_probe/startup_replay.rs.
//
// The startup probe reads the terminal's replies directly from stdin, so any
// keystroke that arrives during the probe window must be handed back to the
// TUI's input loop (Rust hands the leftover bytes to crossterm's event parser).
// Completed OSC replies are dropped; everything else is preserved, including
// bracketed paste framing and the probe's own DA1 reply, which stays an
// unrecognized CSI sequence the input parser consumes without emitting a key.
const maxReplayedOSCBytes = 1024

// StartupReplayInput returns the terminal input that arrived during the
// startup probe minus the completed OSC replies the probe consumed. A bounded
// incomplete OSC prefix is preserved so an unread continuation stays
// parser-framed.
func StartupReplayInput(input []byte) []byte {
	pasteStart := []byte("\x1b[200~")
	pasteEnd := []byte("\x1b[201~")
	replay := make([]byte, 0, len(input))
	cursor := 0
	insidePaste := false
	for cursor < len(input) {
		if insidePaste {
			if bytes.HasPrefix(input[cursor:], pasteEnd) {
				replay = append(replay, pasteEnd...)
				cursor += len(pasteEnd)
				insidePaste = false
			} else if skip, ok := colorReplyLen(input[cursor:]); ok {
				cursor += skip
			} else {
				replay = append(replay, input[cursor])
				cursor++
			}
			continue
		}
		if bytes.HasPrefix(input[cursor:], pasteStart) {
			replay = append(replay, pasteStart...)
			cursor += len(pasteStart)
			insidePaste = true
			continue
		}
		if bytes.HasPrefix(input[cursor:], []byte("\x1b]")) {
			if end, terminatorLen, ok := oscReplyLen(input[cursor+2:]); ok {
				cursor += 2 + end + terminatorLen
				continue
			}
			end := min(len(input), cursor+maxReplayedOSCBytes)
			replay = append(replay, input[cursor:end]...)
			break
		}
		replay = append(replay, input[cursor])
		cursor++
	}
	return replay
}

// oscReplyLen measures a complete OSC payload that starts just after `ESC ]`
// and reports how many bytes its terminator occupies (BEL or ST).
func oscReplyLen(buffer []byte) (int, int, bool) {
	end, ok := oscPayloadEnd(buffer)
	if !ok {
		return 0, 0, false
	}
	if buffer[end] == 0x1b {
		return end, 2, true
	}
	return end, 1, true
}

// colorReplyLen recognizes a completed OSC 10/11 reply inside a bracketed
// paste, where only the probe's own color replies are dropped. Rust bounds the
// scan to MAX_REPLAYED_OSC_BYTES counted from the start of the sequence.
func colorReplyLen(buffer []byte) (int, bool) {
	for _, slot := range []byte{10, 11} {
		prefix := []byte("\x1b]" + strconv.Itoa(int(slot)) + ";")
		if !bytes.HasPrefix(buffer, prefix) {
			continue
		}
		scan := buffer[2:]
		if len(scan) > maxReplayedOSCBytes-2 {
			scan = scan[:maxReplayedOSCBytes-2]
		}
		end, terminatorLen, ok := oscReplyLen(scan)
		if !ok {
			continue
		}
		response := buffer[:2+end+terminatorLen]
		if bytes.Contains(response, []byte("\x1b[201~")) {
			continue
		}
		if _, ok := ParseOSCColor(response, slot); ok {
			return len(response), true
		}
	}
	return 0, false
}
