package appserver

import "testing"

// Mirrors Rust #46029: the remote browser cleanup hook is allowlisted for
// interrupts too, while the registered-connector and empty-input requirements
// that guard raw Apps tool names still apply.
func TestAllowlistedBundledCleanupHookInterruptLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name          string
		handler       hookJSONHandlerConfigWire
		appConnector  *string
		wantAllowlist bool
	}{
		{
			name:          "registered interrupted app",
			handler:       bundledHookHandler("codex_apps", "browser.turn_ended", nil),
			appConnector:  stringPointerForHooks("connector_openai_browser"),
			wantAllowlist: true,
		},
		{
			name:         "colliding interrupt tool name",
			handler:      bundledHookHandler("codex_apps", "browser.turn_ended", nil),
			appConnector: stringPointerForHooks("connector_other_browser"),
		},
		{
			name:         "interrupt manifest arguments",
			handler:      bundledHookHandler("codex_apps", "browser.turn_ended", map[string]any{"untrusted": "manifest-provided input"}),
			appConnector: stringPointerForHooks("connector_openai_browser"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := isAllowlistedBundledCleanupHook("browser@openai-curated-remote", HookEventInterrupt, nil, tc.handler, tc.appConnector)
			if got != tc.wantAllowlist {
				t.Fatalf("interrupt allowlist = %v, want %v", got, tc.wantAllowlist)
			}
		})
	}
}
