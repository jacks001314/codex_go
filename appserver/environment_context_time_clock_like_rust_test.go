package appserver

import (
	"context"
	"strings"
	"testing"

	"codex_go/config"
)

// Rust #52164 (core/src/session/world_state.rs): with
// include_environment_context_time = false the current date is None without a
// clock read. A stalled external provider therefore cannot fail the context or
// emit the CurrentTimeUnavailable notice - the flag is checked before the read.
func TestEnvironmentContextTimeDisabledSkipsClockReadLikeRust(t *testing.T) {
	router, params := environmentContextTimeRouter(t, t.TempDir())
	router.subscribeThreadConnection(params.ThreadID, "conn-clock")
	values := map[string]any{
		"include_environment_context_time": false,
		"features": map[string]any{
			"current_time_reminder": map[string]any{
				"enabled":      true,
				"clock_source": "external",
			},
		},
	}
	cfg := &config.Config{Values: values}

	items, sessionItems, err := router.turnEnvironmentContextInputItemForTurn(
		context.Background(), params.ThreadID, "turn-clock", params, cfg)
	if err != nil {
		t.Fatalf("disabled environment time must not read the clock: %v", err)
	}
	if len(items) != 1 || len(sessionItems) != 0 {
		t.Fatalf("items = %#v, session items = %#v; want only the environment context", items, sessionItems)
	}
	texts := messageInputTextsForRole(items, "user")
	if len(texts) != 1 || strings.Contains(texts[0], "<current_date>") || strings.Contains(texts[0], "<timezone>") {
		t.Fatalf("environment context = %#v, want no date or timezone", texts)
	}

	// With the flag on, the same unconfigured external provider is read and the
	// default (fatal) clock-read error surfaces, proving the read was skipped
	// above rather than silently failing closed.
	values["include_environment_context_time"] = true
	if _, _, err := router.turnEnvironmentContextInputItemForTurn(
		context.Background(), params.ThreadID, "turn-clock", params, cfg); err == nil {
		t.Fatal("enabled environment time must read the external clock")
	}
}
