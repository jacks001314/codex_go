package realtime

import (
	"bytes"
	"strings"
	"testing"
)

func TestV3ProviderVoiceDefaultsLikeRust(t *testing.T) {
	params := &StartParams{ThreadID: "voice-default", OutputModality: OutputAudio}
	cfg, err := params.Normalized("", VersionV3, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Voice != "" {
		t.Fatalf("unset voice eagerly resolved to %q", cfg.Voice)
	}
	if cfg.Model != "gpt-live-1.1-codex" {
		t.Fatalf("model=%q", cfg.Model)
	}
	cfg.Transport = *WebRTCTransport("offer")
	for _, tc := range []struct {
		base  string
		voice Voice
	}{
		{"https://api.openai.com/v1", VoiceMarin},
		{"https://chatgpt.com/backend-api/codex", VoiceCove},
	} {
		body, _, err := realtimeCallBody(tc.base, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte(`"voice":"`+string(tc.voice)+`"`)) {
			t.Fatalf("%s: %s", tc.base, body)
		}
		if cfg.Voice != "" {
			t.Fatal("serialization mutated config")
		}
		explicit := *cfg
		explicit.Voice = VoiceViola
		body, _, err = realtimeCallBody(tc.base, &explicit)
		if err != nil || !strings.Contains(string(body), `"voice":"viola"`) {
			t.Fatalf("explicit voice: %s %v", body, err)
		}
	}
	update := realtimeSessionUpdate(cfg)
	session := update["session"].(map[string]any)
	output := session["audio"].(map[string]any)["output"].(map[string]any)
	if output["voice"] != VoiceMarin {
		t.Fatalf("websocket voice=%v", output["voice"])
	}
}
