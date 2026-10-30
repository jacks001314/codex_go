package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/turn"
)

// TestCodexTurnAnalyticsReportsTriggerAndEffectiveSourceLikeRust covers Rust
// #42003: turn analytics reports the caller-provided trigger and the effective
// Responses `source`, where configured responses_api_metadata wins over client
// metadata and values above the 128-byte metadata limit are omitted.
func TestCodexTurnAnalyticsReportsTriggerAndEffectiveSourceLikeRust(t *testing.T) {
	pointer := func(value string) *string { return &value }

	cases := []struct {
		name             string
		trigger          string
		clientSource     *string
		configuredSource *string
		wantTrigger      *string
		wantSource       *string
	}{
		{
			name:             "configured source wins over client source",
			trigger:          "user",
			clientSource:     pointer("client-source"),
			configuredSource: pointer("configured-source"),
			wantTrigger:      pointer("user"),
			wantSource:       pointer("configured-source"),
		},
		{
			name:         "client source is reported when not configured",
			trigger:      "user",
			clientSource: pointer("client-source"),
			wantTrigger:  pointer("user"),
			wantSource:   pointer("client-source"),
		},
		{
			name:         "explicitly empty client source stays empty",
			trigger:      "composer",
			clientSource: pointer(""),
			wantTrigger:  pointer("composer"),
			wantSource:   pointer(""),
		},
		{
			name:         "source at the byte limit is reported",
			trigger:      "automation",
			clientSource: pointer(strings.Repeat("é", 64)),
			wantTrigger:  pointer("automation"),
			wantSource:   pointer(strings.Repeat("é", 64)),
		},
		{
			name:         "source above the byte limit is omitted",
			trigger:      "automation",
			clientSource: pointer(strings.Repeat("é", 65)),
			wantTrigger:  pointer("automation"),
		},
		{
			name:        "missing trigger and source stay absent",
			wantTrigger: nil,
			wantSource:  nil,
		},
		{
			name:        "whitespace-only trigger is not recorded",
			trigger:     "   ",
			wantTrigger: nil,
		},
		{
			name:        "trigger keeps its raw value",
			trigger:     "  goal  ",
			wantTrigger: pointer("  goal  "),
		},
	}

	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router, sink := newModelAttributionRouter(t)
			threadID := "thread-turn-analytics-" + strings.ReplaceAll(testCase.name, " ", "-")
			turnID := threadID + "-" + strings.Repeat("x", index+1)
			params := &turn.TurnStartParams{ThreadID: threadID, TurnTrigger: testCase.trigger}
			if err := router.threads.RegisterTurn(threadID, turnID, nil, time.Now().UTC().UnixMilli(), params); err != nil {
				t.Fatalf("RegisterTurn() error = %v", err)
			}

			extra := map[string]string{}
			if testCase.clientSource != nil {
				extra["source"] = *testCase.clientSource
			}
			configured := map[string]string{}
			if testCase.configuredSource != nil {
				configured["source"] = *testCase.configuredSource
			}
			runConfig := &appTurnRunConfig{
				SessionID: threadID,
				ClientMetadata: turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
					ThreadID:             threadID,
					TurnID:               turnID,
					TurnTrigger:          testCase.trigger,
					Extra:                extra,
					ResponsesAPIMetadata: configured,
				}),
			}

			emitCodexTurnEventForTest(t, router, params, runConfig, turnID)
			event := waitForCodexTurnEvent(t, sink, turnID)
			if got := event.EventParams.TurnTrigger; !equalAnalyticsOptionalString(got, testCase.wantTrigger) {
				t.Fatalf("turn_trigger = %#v, want %#v", analyticsTestStringValue(got), analyticsTestStringValue(testCase.wantTrigger))
			}
			if got := event.EventParams.CodexTurnSource; !equalAnalyticsOptionalString(got, testCase.wantSource) {
				t.Fatalf("codex_turn_source = %#v, want %#v", analyticsTestStringValue(got), analyticsTestStringValue(testCase.wantSource))
			}
		})
	}
}

// TestCodexTurnAnalyticsReflectsAcceptedSteerSourceLikeRust covers the upstream
// steer case: an accepted steer updates the turn's effective Responses source,
// and the turn event emitted at completion reports the steered value.
func TestCodexTurnAnalyticsReflectsAcceptedSteerSourceLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-steer-analytics"
		turnID       = "turn-steer-analytics"
	)
	params := &turn.TurnStartParams{ThreadID: threadID, TurnTrigger: "user"}
	if err := router.threads.RegisterTurn(threadID, turnID, nil, time.Now().UTC().UnixMilli(), params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	runConfig := &appTurnRunConfig{
		SessionID: threadID,
		ClientMetadata: turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
			ThreadID:    threadID,
			TurnID:      turnID,
			TurnTrigger: "user",
			Extra:       map[string]string{"source": "initial-source"},
		}),
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, connectionID, runConfig)

	steerParams := &turn.TurnSteerParams{
		ThreadID:                  threadID,
		ExpectedTurnID:            turnID,
		ResponsesAPIMetadataPatch: map[string]string{"source": "steer-source"},
	}
	steerMetadata := router.steerClientMetadata(steerParams)
	if len(steerMetadata) == 0 {
		t.Fatal("steerClientMetadata() returned no metadata")
	}
	router.rememberTurnSteerMetadata(threadID, turnID, steerMetadata)

	emitCodexTurnEventForTest(t, router, params, runConfig, turnID)
	event := waitForCodexTurnEvent(t, sink, turnID)
	if source := analyticsTestStringValue(event.EventParams.CodexTurnSource); source != "steer-source" {
		t.Fatalf("codex_turn_source = %q, want steer-source", source)
	}
	if trigger := analyticsTestStringValue(event.EventParams.TurnTrigger); trigger != "user" {
		t.Fatalf("turn_trigger = %q, want user", trigger)
	}
}

func emitCodexTurnEventForTest(t *testing.T, router *RuntimeRouter, params *turn.TurnStartParams, runConfig *appTurnRunConfig, turnID string) {
	t.Helper()
	startedAt := time.Now().UTC()
	record := &turn.TurnRecord{ID: turnID, ThreadID: params.ThreadID, StartedAt: startedAt.Unix()}
	router.emitCodexTurnAnalyticsEvent(
		context.Background(),
		"conn-model-attribution",
		params,
		record,
		runConfig,
		nil,
		TurnStatusCompleted,
		startedAt,
		startedAt.Add(time.Second),
		1000,
		0,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}

func equalAnalyticsOptionalString(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func analyticsTestStringValue(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}
