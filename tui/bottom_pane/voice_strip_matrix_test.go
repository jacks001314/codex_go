package bottompane

import (
	"strings"
	"testing"
	"time"
)

// TestVoiceStripStateMatrixMatchesGolden freezes the rendered strip for the
// main voice conversation states, mirroring the Rust voice-footer snapshot.
// Reduced motion drops the sample-history row (Rust #46835), so only the
// animated states carry a meter line.
func TestVoiceStripStateMatrixMatchesGolden(t *testing.T) {
	started := time.Unix(0, 0)
	states := []struct {
		name  string
		state VoiceStripState
	}{
		{"inactive", VoiceStripState{Activity: "ready", Animations: true}},
		{"connecting-animated", VoiceStripState{Phase: VoiceStripConnecting, Animations: true, StartedAt: started, Now: started.Add(150 * time.Millisecond), Activity: "connecting"}},
		{"connecting-reduced", VoiceStripState{Phase: VoiceStripConnecting, StartedAt: started, Now: started, Activity: "connecting"}},
		{"listening", VoiceStripState{Phase: VoiceStripActive, MicrophoneLive: true, Activity: "listening", Animations: true}},
		{"muted-hint", VoiceStripState{Phase: VoiceStripActive, MicrophoneLive: true, MicrophoneMuted: true, MuteHint: "ctrl + m", Activity: "listening", Animations: true}},
		{"speaking", VoiceStripState{Phase: VoiceStripActive, MicrophoneLive: true, MicrophoneHistory: []uint8{32, 32, 32, 32, 32, 32}, SpeakerHistory: []uint8{0, 64, 128, 192, 255, 255}, Activity: "speaking", Animations: true}},
		{"retrying", VoiceStripState{Phase: VoiceStripConnecting, Animations: true, StartedAt: started, Now: started.Add(450 * time.Millisecond), MicrophoneLive: true, Activity: "retrying"}},
	}
	golden := map[string]string{
		"inactive":            "voice ◌ ready                                        /voice mute   /voice stop\n  mic ▁▁▁▁▁▁  codex ▁▁▁▁▁▁",
		"connecting-animated": "voice ⠙ connecting                                                 /voice stop\n  mic ▁▁▁▁▁▁  codex ▁▁▁▁▁▁",
		"connecting-reduced":  "voice ◌ connecting                                                 /voice stop",
		"listening":           "voice ● listening                                    /voice mute   /voice stop\n  mic ▁▁▁▁▁▁  codex ▁▁▁▁▁▁",
		"muted-hint":          "voice ◌ listening                                  ctrl+m unmute   /voice stop\n  mic ▁▁▁▁▁▁  codex ▁▁▁▁▁▁",
		"speaking":            "voice ● speaking                                     /voice mute   /voice stop\n  mic ▂▂▂▂▂▂  codex ▁▃▅▇██",
		"retrying":            "voice ⠼ retrying                                     /voice mute   /voice stop\n  mic ▁▁▁▁▁▁  codex ▁▁▁▁▁▁",
	}
	for _, entry := range states {
		want, ok := golden[entry.name]
		if !ok {
			t.Fatalf("missing golden for %q", entry.name)
		}
		if got := strings.Join(VoiceStripLines(80, entry.state), "\n"); got != want {
			t.Fatalf("%s strip drift\n got: %q\nwant: %q", entry.name, got, want)
		}
	}
}
