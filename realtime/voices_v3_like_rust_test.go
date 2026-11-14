package realtime

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Rust #52363: v3 has its own voice list containing the v1 voices plus the 16
// v3-only additions, while its default remains the v1 default.
func TestBuiltinVoicesExposeV3LikeRust(t *testing.T) {
	voices := BuiltinVoices()
	want := []Voice{
		VoiceJuniper, VoiceMaple, VoiceSpruce, VoiceEmber, VoiceVale,
		VoiceBreeze, VoiceArbor, VoiceSol, VoiceCove, VoiceMarin,
		VoiceAube, VoiceBubbie, VoiceHaetsal, VoiceHanul, VoiceHimari,
		VoiceLeher, VoiceMiko, VoiceNeer, VoicePorto, VoiceRindo, VoiceRio,
		VoiceRivage, VoiceSelva, VoiceSonna, VoiceTinta, VoiceViola,
	}
	if !reflect.DeepEqual(voices.V3, want) {
		t.Fatalf("V3 voices = %#v, want %#v", voices.V3, want)
	}
	if voices.DefaultForVersion(VersionV3) != VoiceCove {
		t.Fatalf("v3 default = %q, want cove", voices.DefaultForVersion(VersionV3))
	}
	if !voices.Supports(VersionV3, VoiceViola) {
		t.Fatal("a v3-only voice was rejected for v3")
	}
	if !voices.Supports(VersionV3, VoiceMarin) {
		t.Fatal("marin was rejected for v3")
	}
	if voices.Supports(VersionV1, VoiceViola) {
		t.Fatal("a v3-only voice was accepted for v1")
	}
}

func TestRealtimeV3VoiceValidationAndJSONLikeRust(t *testing.T) {
	params := &StartParams{ThreadID: "thread-1", OutputModality: OutputAudio}
	if _, err := params.Normalized("model", VersionV3, VoiceViola); err != nil {
		t.Fatalf("Normalized(v3, viola) error = %v", err)
	}
	if _, err := params.Normalized("model", VersionV3, VoiceMarin); err != nil {
		t.Fatalf("marin rejected: %v", err)
	}
	var decoded VoicesList
	if err := json.Unmarshal([]byte(`{"v1":[],"v2":[],"defaultV1":"cove","defaultV2":"marin"}`), &decoded); err != nil {
		t.Fatalf("Unmarshal(VoicesList) error = %v", err)
	}
	if decoded.V3 == nil || len(decoded.V3) != 0 {
		t.Fatalf("omitted v3 decoded as %#v, want an empty list", decoded.V3)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("Marshal(VoicesList) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"v3":[]`) {
		t.Fatalf("encoded voices = %s, want an empty v3 array", encoded)
	}
}

func TestKnownVoiceIncludesEntireV3Catalog(t *testing.T) {
	for _, voice := range BuiltinVoices().V3 {
		if !IsKnownVoice(voice) {
			t.Errorf("V3 voice %q rejected", voice)
		}
	}
}

func TestV3VoiceStartProtocolPreservesCatalogValues(t *testing.T) {
	for _, voice := range BuiltinVoices().V3 {
		t.Run(string(voice), func(t *testing.T) {
			wire, err := json.Marshal(map[string]any{"threadId": "thread-1", "outputModality": "audio", "version": "v3", "voice": voice})
			if err != nil {
				t.Fatal(err)
			}
			var params StartParams
			if err := json.Unmarshal(wire, &params); err != nil {
				t.Fatal(err)
			}
			cfg, err := params.Normalized("", VersionV3, "")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Voice != voice {
				t.Fatalf("voice=%q want=%q", cfg.Voice, voice)
			}
			session := realtimeSessionUpdate(cfg)["session"].(map[string]any)
			output := session["audio"].(map[string]any)["output"].(map[string]any)
			if output["voice"] != voice {
				t.Fatalf("wire voice=%v want=%q", output["voice"], voice)
			}
		})
	}
	for _, raw := range []string{`"future-voice"`, `" viola "`, `3`, `true`} {
		var params StartParams
		if err := json.Unmarshal([]byte(`{"threadId":"thread-1","outputModality":"audio","version":"v3","voice":`+raw+`}`), &params); err == nil {
			t.Fatalf("invalid wire voice %s accepted", raw)
		}
	}
}
