package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/telemetry"
)

// enabledAnalyticsSink satisfies telemetry.TurnEventSink and exposes the
// analytics state accessor used by turn metadata.
type enabledAnalyticsSink struct {
	enabled bool
}

func (s enabledAnalyticsSink) TrackCodexTurnEvent(context.Context, telemetry.CodexTurnEventRequest) {}

func (s enabledAnalyticsSink) Enabled() bool { return s.enabled }

func TestAnalyticsDisabledByConfigLikeRust(t *testing.T) {
	if analyticsDisabledByConfig(nil) || analyticsDisabledByConfig(map[string]any{}) {
		t.Fatal("empty config must not disable analytics")
	}
	if !analyticsDisabledByConfig(map[string]any{"analytics_enabled": false}) ||
		!analyticsDisabledByConfig(map[string]any{"analyticsEnabled": false}) {
		t.Fatal("explicit false must disable analytics")
	}
	if analyticsDisabledByConfig(map[string]any{"analytics_enabled": true}) {
		t.Fatal("explicit true must not disable analytics")
	}
	if analyticsDisabledByConfig(map[string]any{"analytics_enabled": "no"}) {
		t.Fatal("non-boolean values must not disable analytics")
	}
}

func TestThreadAnalyticsDisabledLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	optedOut := &session.Record{
		ID: "opted-out", SessionID: "opted-out", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Extra: map[string]any{"config": map[string]any{"analytics_enabled": false}}},
	}
	sibling := &session.Record{
		ID: "sibling", SessionID: "sibling", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir()},
	}
	for _, record := range []*session.Record{optedOut, sibling} {
		if err := store.Create(record); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Analytics: enabledAnalyticsSink{enabled: true}})
	if !router.threadAnalyticsDisabled("opted-out") {
		t.Fatal("thread with analytics_enabled=false must be disabled")
	}
	if router.threadAnalyticsDisabled("sibling") {
		t.Fatal("sibling thread analytics must stay enabled")
	}
	if router.threadAnalyticsDisabled("missing-thread") {
		t.Fatal("unknown thread must default to enabled")
	}

	// A disabled host client suppresses analytics for every thread.
	disabledHost := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	if !disabledHost.threadAnalyticsDisabled("sibling") {
		t.Fatal("missing host analytics client must disable analytics")
	}
}

func TestAnalyticsEnabledOptionForThreadLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{
		ID: "opted-out", SessionID: "opted-out", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Extra: map[string]any{"config": map[string]any{"analytics_enabled": false}}},
	}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Analytics: enabledAnalyticsSink{enabled: true}})
	if option := router.analyticsEnabledOptionForThread("opted-out"); option == nil || *option {
		t.Fatalf("opted-out thread option = %#v, want false", option)
	}
	if option := router.analyticsEnabledOptionForThread("no-override"); option == nil || !*option {
		t.Fatalf("enabled thread option = %#v, want true", option)
	}

	disabledClient := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Analytics: enabledAnalyticsSink{enabled: false}})
	if option := disabledClient.analyticsEnabledOptionForThread("no-override"); option == nil || *option {
		t.Fatalf("disabled client option = %#v, want false", option)
	}

	// Sinks without an analytics-state accessor omit the field, and no client
	// means no session analytics context.
	customSink := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Analytics: enabledAnalyticsSinkWithoutState{}})
	if option := customSink.analyticsEnabledOptionForThread("no-override"); option != nil {
		t.Fatalf("custom sink option = %#v, want nil", option)
	}
	if option := NewRuntimeRouter(RuntimeServices{}).analyticsEnabledOptionForThread("no-override"); option != nil {
		t.Fatalf("missing analytics option = %#v, want nil", option)
	}
}

type enabledAnalyticsSinkWithoutState struct{}

func (enabledAnalyticsSinkWithoutState) TrackCodexTurnEvent(context.Context, telemetry.CodexTurnEventRequest) {
}
