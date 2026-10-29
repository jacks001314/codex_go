package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #40942 defaults the current-time reminder and the interruptible
// clock.sleep tool on for persistent-reasoning turns, while explicit config and
// managed requirements win.
func TestResolvedClockReminderConfigLikeRust(t *testing.T) {
	persistent := &turn.TurnStartParams{Effort: stringPtrAppserver("persistent")}
	ordinary := &turn.TurnStartParams{Effort: stringPtrAppserver("medium")}
	for _, tc := range []struct {
		name        string
		cfg         *config.Config
		params      *turn.TurnStartParams
		wantNil     bool
		wantEnabled bool
		wantSleep   bool
	}{
		{
			name:        "persistent defaults on",
			cfg:         &config.Config{Values: map[string]any{"features": map[string]any{}}},
			params:      persistent,
			wantEnabled: true,
			wantSleep:   true,
		},
		{
			name:    "ordinary effort stays off",
			cfg:     &config.Config{Values: map[string]any{"features": map[string]any{}}},
			params:  ordinary,
			wantNil: true,
		},
		{
			name:        "explicit config wins",
			cfg:         &config.Config{Values: map[string]any{"features": map[string]any{"current_time_reminder": map[string]any{"enabled": true}}}},
			params:      persistent,
			wantEnabled: true,
			wantSleep:   false,
		},
		{
			name:        "explicitly disabled stays disabled",
			cfg:         &config.Config{Values: map[string]any{"features": map[string]any{"current_time_reminder": false}}},
			params:      persistent,
			wantEnabled: false,
		},
		{
			name: "managed requirement disables the default",
			cfg: &config.Config{
				Values:       map[string]any{"features": map[string]any{}},
				Requirements: &config.ConfigRequirements{FeatureRequirements: map[string]bool{"current_time_reminder": false}},
			},
			params:  persistent,
			wantNil: true,
		},
		{
			name:        "configured model effort",
			cfg:         &config.Config{Values: map[string]any{"features": map[string]any{}, "model_reasoning_effort": "persistent"}},
			params:      &turn.TurnStartParams{},
			wantEnabled: true,
			wantSleep:   true,
		},
		{
			name:    "review turns stay off",
			cfg:     &config.Config{Values: map[string]any{"features": map[string]any{}}},
			params:  &turn.TurnStartParams{Effort: stringPtrAppserver("persistent"), Originator: "review"},
			wantNil: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reminder := resolvedClockReminderConfig(tc.cfg, tc.params)
			if tc.wantNil {
				if reminder != nil {
					t.Fatalf("resolvedClockReminderConfig() = %+v, want nil", reminder)
				}
				return
			}
			if reminder == nil {
				t.Fatal("resolvedClockReminderConfig() = nil, want a reminder config")
			}
			if reminder.Enabled != tc.wantEnabled || reminder.SleepTool != tc.wantSleep {
				t.Fatalf("resolvedClockReminderConfig() = %+v, want enabled=%v sleep=%v", reminder, tc.wantEnabled, tc.wantSleep)
			}
			if tc.wantSleep && (reminder.ReminderIntervalSeconds != 1 ||
				reminder.ClockSource != config.CurrentTimeSourceSystem ||
				reminder.DeliveryMode != config.CurrentTimeReminderAnyInference) {
				t.Fatalf("persistent default = %+v, want interval 1, system clock, any-inference delivery", reminder)
			}
		})
	}
}

func TestPersistentTurnDeliversCurrentTimeReminderLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{}}}
	createdAt := time.Now().UTC()
	inputItems, sessionItems, err := router.currentTimeReminderInputItems(
		context.Background(), "thread", "turn",
		cfg, &turn.TurnStartParams{Effort: stringPtrAppserver("persistent")},
		createdAt, router.newCurrentTimeReminderTurnState("thread"),
	)
	if err != nil {
		t.Fatalf("currentTimeReminderInputItems() error = %v", err)
	}
	if len(inputItems) != 1 || len(sessionItems) != 1 {
		t.Fatalf("persistent turn produced %d inputs and %d session items, want 1 and 1", len(inputItems), len(sessionItems))
	}
	if !strings.Contains(strings.Join(sessionItemTextsForTest(sessionItems), "\n"), "<current_time_reminder>") {
		t.Fatalf("session items = %#v, want a current_time_reminder item", sessionItems)
	}

	inputItems, sessionItems, err = router.currentTimeReminderInputItems(
		context.Background(), "thread", "turn",
		cfg, &turn.TurnStartParams{Effort: stringPtrAppserver("medium")},
		createdAt, router.newCurrentTimeReminderTurnState("thread"),
	)
	if err != nil {
		t.Fatalf("currentTimeReminderInputItems() error = %v", err)
	}
	if len(inputItems) != 0 || len(sessionItems) != 0 {
		t.Fatalf("ordinary turn produced %d inputs and %d session items, want none", len(inputItems), len(sessionItems))
	}
}

func sessionItemTextsForTest(items []session.Item) []string {
	texts := make([]string, 0, len(items))
	for _, item := range items {
		texts = append(texts, item.Text)
	}
	return texts
}
