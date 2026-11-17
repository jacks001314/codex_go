package app

import (
	"crypto/rand"
	"encoding/hex"
)

// newVoiceSessionID returns a random RFC 4122 version 4 UUID for one realtime
// voice call (Rust #61908 `Uuid::new_v4`). The value is distinct from the
// thread ID: the thread keeps `realtimeSessionId`/`x-session-id`, while this id
// rides the `voice-session-id` header and `voiceSessionId` in
// `thread/realtime/start`.
func newVoiceSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	var out [36]byte
	hex.Encode(out[0:8], raw[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], raw[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], raw[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], raw[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], raw[10:16])
	return string(out[:]), nil
}
