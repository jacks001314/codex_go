package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/realtime"
	"codex_go/session"
)

func TestRealtimeVoiceReloadsChangedAndClearedPreferenceLikeRust(t *testing.T) {
	home := t.TempDir()
	write := func(voice string) {
		t.Helper()
		body := "[realtime]\nversion = \"v3\"\n"
		if voice != "" {
			body += "voice = \"" + voice + "\"\n"
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("cove")
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions")))})
	t.Cleanup(func() { _ = router.Close() })
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: t.TempDir(), Config: map[string]any{"realtime.version": "v3"},
	}))
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	id := response.Result.(*ThreadStartResponse).Thread.ID
	for _, voice := range []string{"marin", "", "viola"} {
		write(voice)
		version := realtime.VersionV3
		cfg, _, err := router.effectiveRealtimeThreadConfig(&realtime.StartParams{ThreadID: id, Version: &version})
		if err != nil {
			t.Fatal(err)
		}
		if got := realtimeVoiceFromConfig(cfg); got != realtime.Voice(voice) {
			t.Fatalf("saved %q got %q", voice, got)
		}
	}
}

func TestRealtimeVoiceReloadRequestBoundaryLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[realtime]\nvoice = \"marin\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions")))})
	t.Cleanup(func() { _ = router.Close() })
	v1, v3 := realtime.VersionV1, realtime.VersionV3
	explicit := realtime.VoiceViola
	for _, tc := range []struct {
		name          string
		threadVersion string
		version       *realtime.Version
		voice         *realtime.Voice
		transport     *realtime.StartTransport
		want          realtime.Voice
	}{
		{"request v3 preserves session voice over latest file", "v1", &v3, nil, nil, realtime.VoiceCove},
		{"request v1 preserves thread voice", "v3", &v1, nil, nil, realtime.VoiceCove},
		{"omitted version preserves thread voice", "v3", nil, nil, nil, realtime.VoiceCove},
		{"explicit voice skips reload", "v3", &v3, &explicit, nil, realtime.VoiceCove},
		{"existing call skips reload", "v3", &v3, nil, realtime.ExistingCallTransport("call-1"), realtime.VoiceCove},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Config: map[string]any{"realtime.voice": "cove", "realtime.version": tc.threadVersion}}))
			if response.Error != nil {
				t.Fatal(response.Error)
			}
			id := response.Result.(*ThreadStartResponse).Thread.ID
			cfg, _, err := router.effectiveRealtimeThreadConfig(&realtime.StartParams{ThreadID: id, Version: tc.version, Voice: tc.voice, Transport: tc.transport})
			if err != nil {
				t.Fatal(err)
			}
			if got := realtimeVoiceFromConfig(cfg); got != tc.want {
				t.Fatalf("voice=%q, want %q", got, tc.want)
			}
		})
	}
}
