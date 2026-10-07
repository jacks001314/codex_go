package appserver

import (
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// TestRuntimeRouterEmitsBwrapWarningLikeRust mirrors the Rust app-server startup
// (`codex-rs/app-server/src/lib.rs`): a profile that needs the platform sandbox
// adds the bubblewrap warning to the config warnings, and a profile that does
// not stays silent. Rust #51211 passes the effective permission profile and
// `config.cwd`, so the startup probe has to receive the router's cwd.
func TestRuntimeRouterEmitsBwrapWarningLikeRust(t *testing.T) {
	original := systemBwrapWarning
	t.Cleanup(func() { systemBwrapWarning = original })

	cwd := t.TempDir()
	seen := 0
	systemBwrapWarning = func(profile *sandbox.PermissionProfile, policyCWD string) string {
		seen++
		if profile == nil {
			t.Error("the startup warning was computed without a permission profile")
			return ""
		}
		if policyCWD != cwd {
			t.Errorf("startup warning cwd = %q, want the router cwd %q", policyCWD, cwd)
		}
		return "bubblewrap is unavailable for this test"
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir()), DefaultCWD: cwd})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	response := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-test", Version: "1.0.0"},
	}))
	if response.Error != nil {
		t.Fatalf("initialize = %+v", response)
	}
	if seen == 0 {
		t.Fatal("the router never consulted the bubblewrap warning")
	}
	notifications := sink.List()
	found := false
	for _, notification := range notifications {
		if notification.Method != NotificationConfigWarning {
			continue
		}
		payload, ok := notification.Params.(*config.ConfigWarningNotification)
		if ok && strings.Contains(payload.Summary, "bubblewrap is unavailable for this test") {
			found = true
		}
	}
	if !found {
		t.Fatalf("bubblewrap warning missing from %+v", notifications)
	}

	// A silent warning contributes nothing.
	systemBwrapWarning = func(*sandbox.PermissionProfile, string) string { return "" }
	router2 := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir()), DefaultCWD: cwd})
	silentSink := NewNotificationBuffer()
	router2.SetNotificationSink(silentSink)
	router2.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-test", Version: "1.0.0"},
	}))
	if got := len(silentSink.List()); got != 0 {
		t.Fatalf("notifications = %+v, want none", silentSink.List())
	}
}
