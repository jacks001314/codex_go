package tui

import "bytes"

// Rust parity: codex-rs/tui/src/terminal_probe/terminal_identity.rs. Apple
// Terminal answers the primary device attributes query with `1;2` and the
// secondary query with `1;95;0`; both replies are required, and an incomplete
// pair leaves the identity unknown. Signatures inside a bracketed paste are
// pasted text, never a terminal reply.

// ParseTerminalAppOverSSH reports whether the buffered startup probe replies
// identify Apple Terminal. The second result is false when either reply is
// still missing, matching Rust's `Option<bool>`.
func ParseTerminalAppOverSSH(input []byte) (bool, bool) {
	primary, ok := probeDeviceAttributes(input, []byte("[?"))
	if !ok {
		return false, false
	}
	secondary, ok := probeDeviceAttributes(input, []byte("[>"))
	if !ok {
		return false, false
	}
	return string(primary) == "1;2" && string(secondary) == "1;95;0", true
}

// probeDeviceAttributes returns the payload of the first complete device
// attribute reply with prefix (`[?` for DA1, `[>` for DA2) that is not inside
// a bracketed paste. A reply is complete at the first final byte (0x40-0x7e).
func probeDeviceAttributes(input []byte, prefix []byte) ([]byte, bool) {
	insidePaste := false
	sequences := bytes.Split(input, []byte{0x1b})
	for _, sequence := range sequences[1:] {
		if hasBytePrefix(sequence, []byte("[200~")) {
			insidePaste = true
			continue
		}
		if hasBytePrefix(sequence, []byte("[201~")) {
			insidePaste = false
			continue
		}
		if insidePaste {
			continue
		}
		if payload, ok := deviceAttributePayload(sequence, prefix); ok {
			return payload, true
		}
	}
	return nil, false
}

func deviceAttributePayload(sequence []byte, prefix []byte) ([]byte, bool) {
	if len(sequence) < len(prefix) || string(sequence[:len(prefix)]) != string(prefix) {
		return nil, false
	}
	payload := sequence[len(prefix):]
	limit := len(payload)
	if limit > 64 {
		limit = 64
	}
	for index := 0; index < limit; index++ {
		if payload[index] >= 0x40 && payload[index] <= 0x7e {
			if payload[index] != 'c' {
				return nil, false
			}
			return payload[:index], true
		}
	}
	return nil, false
}

func hasBytePrefix(value []byte, prefix []byte) bool {
	if len(value) < len(prefix) {
		return false
	}
	return string(value[:len(prefix)]) == string(prefix)
}
