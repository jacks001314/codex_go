package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/turn"
)

func utcEnvironmentSection(date string, isUTC bool) environmentWorldStateSection {
	return environmentWorldStateSection{
		Environments: map[string]environmentWorldStateEnvironment{
			"local": {CWD: "/repo", Status: environmentStatusAvailable, Shell: "zsh"},
		},
		CurrentDate:      date,
		CurrentDateIsUTC: isUTC,
		Timezone:         "UTC",
	}
}

// TestEnvironmentContextUTCDateLikeRust pins Rust #61897: with
// utc_environment_date enabled the model-visible date is computed in UTC and
// rendered as <current_date_utc>; disabling the feature keeps the local date
// under <current_date>, and a basis switch restates an unchanged date.
func TestEnvironmentContextUTCDateLikeRust(t *testing.T) {
	t.Run("basis switch restates the unchanged date", func(t *testing.T) {
		previous := utcEnvironmentSection("2026-06-20", false)
		current := utcEnvironmentSection("2026-06-20", true)
		_, fragment := current.renderDiff(previous, true, nil)
		if !strings.Contains(fragment, "<current_date_utc>2026-06-20</current_date_utc>") {
			t.Fatalf("fragment = %q, want the UTC date element", fragment)
		}
		if strings.Contains(fragment, "<current_date>") {
			t.Fatalf("fragment = %q, want no local date element", fragment)
		}
	})

	t.Run("removal marker keeps the previous basis", func(t *testing.T) {
		previous := utcEnvironmentSection("2026-06-20", true)
		current := utcEnvironmentSection("", false)
		_, fragment := current.renderDiff(previous, true, nil)
		if !strings.Contains(fragment, `  <current_date_utc status="unavailable" />`) {
			t.Fatalf("fragment = %q, want the UTC unavailable marker", fragment)
		}
	})

	t.Run("full render uses the UTC date element", func(t *testing.T) {
		body := utcEnvironmentSection("2026-06-20", true).renderFull(environmentWorldStateRemovals{}, "")
		if !strings.Contains(body, "<current_date_utc>2026-06-20</current_date_utc>") {
			t.Fatalf("body = %q, want the UTC date element", body)
		}
	})

	t.Run("section formats the date in UTC when enabled", func(t *testing.T) {
		manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
		router := NewRuntimeRouter(RuntimeServices{Environment: manager})
		defer router.Close()
		instant := time.Date(2026, time.June, 20, 0, 30, 0, 0, time.Local)
		wantLocal := instant.In(time.Local).Format("2006-01-02")
		wantUTC := instant.UTC().Format("2006-01-02")
		params := &turn.TurnStartParams{}
		local := router.turnEnvironmentContextTextWithOptions(context.Background(), params, &instant, "UTC", "", true, false, environmentWorldStateRemovals{}, false, false, nil)
		if !strings.Contains(local, "<current_date>"+wantLocal+"</current_date>") {
			t.Fatalf("local context = %q, want the local date %s", local, wantLocal)
		}
		utc := router.turnEnvironmentContextTextWithOptions(context.Background(), params, &instant, "UTC", "", true, true, environmentWorldStateRemovals{}, false, false, nil)
		if !strings.Contains(utc, "<current_date_utc>"+wantUTC+"</current_date_utc>") {
			t.Fatalf("utc context = %q, want the UTC date %s", utc, wantUTC)
		}
		if wantUTC != wantLocal && strings.Contains(local, "<current_date_utc>") {
			t.Fatalf("local context = %q, want no UTC date element", local)
		}
	})

	t.Run("snapshot records the clock basis only in UTC mode", func(t *testing.T) {
		raw, err := json.Marshal(utcEnvironmentSection("2026-06-20", false))
		if err != nil || strings.Contains(string(raw), "current_date_is_utc") {
			t.Fatalf("local snapshot = %s (err %v), want no clock basis", raw, err)
		}
		raw, err = json.Marshal(utcEnvironmentSection("2026-06-20", true))
		if err != nil || !strings.Contains(string(raw), `"current_date_is_utc":true`) {
			t.Fatalf("utc snapshot = %s (err %v), want the UTC clock basis", raw, err)
		}
	})
}
