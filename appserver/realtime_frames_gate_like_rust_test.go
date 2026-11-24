package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/state"
)

func TestRealtimeFramesFlagLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{name: "enabled", cfg: &config.Config{Values: map[string]any{"features": map[string]any{"realtime_frames_local": true}}}, want: true},
		{name: "default off", cfg: &config.Config{Values: map[string]any{}}, want: false},
		{name: "explicitly off", cfg: &config.Config{Values: map[string]any{"features": map[string]any{"realtime_frames_local": false}}}, want: false},
		{name: "nil config", cfg: nil, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := realtimeFramesFlagEnabled(testCase.cfg); got != testCase.want {
				t.Fatalf("realtimeFramesFlagEnabled = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestPaginatedHistoryModeLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		mode  string
		deflt ThreadHistoryMode
		want  bool
	}{
		{name: "paginated record", mode: "paginated", deflt: ThreadHistoryLegacy, want: true},
		{name: "paginated case and spaces", mode: " Paginated ", deflt: ThreadHistoryLegacy, want: true},
		{name: "missing mode follows paginated store", mode: "", deflt: ThreadHistoryPaginated, want: true},
		{name: "default keyword follows paginated store", mode: "default", deflt: ThreadHistoryPaginated, want: true},
		{name: "missing mode follows legacy store", mode: "", deflt: ThreadHistoryLegacy, want: false},
		{name: "legacy record stays legacy", mode: "legacy", deflt: ThreadHistoryPaginated, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := paginatedHistoryMode(testCase.mode, testCase.deflt); got != testCase.want {
				t.Fatalf("paginatedHistoryMode(%q, %q) = %v, want %v", testCase.mode, testCase.deflt, got, testCase.want)
			}
		})
	}
}

func TestRealtimeFramesEligibleLikeRust(t *testing.T) {
	enabled := &config.Config{Values: map[string]any{"features": map[string]any{"realtime_frames_local": true}}}
	for _, testCase := range []struct {
		name        string
		cfg         *config.Config
		mode        string
		defaultMode ThreadHistoryMode
		want        bool
	}{
		{name: "flag and paginated", cfg: enabled, mode: "paginated", defaultMode: ThreadHistoryLegacy, want: true},
		{name: "flag and paginated default", cfg: enabled, mode: "", defaultMode: ThreadHistoryPaginated, want: true},
		{name: "flag off", cfg: &config.Config{Values: map[string]any{}}, mode: "paginated", defaultMode: ThreadHistoryPaginated, want: false},
		{name: "legacy thread", cfg: enabled, mode: "legacy", defaultMode: ThreadHistoryPaginated, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := realtimeFramesEligible(testCase.cfg, testCase.mode, testCase.defaultMode); got != testCase.want {
				t.Fatalf("realtimeFramesEligible = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestRealtimeFramesBackingReadyLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		stateRuntime *state.StateRuntime
		threadStore  bool
		want         bool
	}{
		{name: "state and store", stateRuntime: &state.StateRuntime{}, threadStore: true, want: true},
		{name: "missing state", stateRuntime: nil, threadStore: true, want: false},
		{name: "missing store", stateRuntime: &state.StateRuntime{}, threadStore: false, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := realtimeFramesBackingReady(testCase.stateRuntime, testCase.threadStore); got != testCase.want {
				t.Fatalf("realtimeFramesBackingReady = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestRealtimeFramesEnabledRequiresStateAndThreadStoreLikeRust(t *testing.T) {
	if (&RuntimeRouter{}).realtimeFramesEnabled("thread-1") {
		t.Fatal("a router without state enrolled the thread")
	}
	router := &RuntimeRouter{services: RuntimeServices{StateRuntime: &state.StateRuntime{}}}
	if router.realtimeFramesEnabled("thread-1") {
		t.Fatal("a router without a thread store enrolled the thread")
	}
}
