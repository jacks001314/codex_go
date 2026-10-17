package appserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/compact"
	"codex_go/config"
	"codex_go/model"
)

func asyncCompactionTestConfig(threshold int, tokenBudget bool, postTurnThreshold int) *config.Config {
	values := map[string]any{
		"features": map[string]any{
			"async_compaction": map[string]any{
				"enabled":                               true,
				"model_async_compact_threshold_percent": threshold,
			},
		},
	}
	if tokenBudget {
		values["features"].(map[string]any)["token_budget"] = true
	}
	if postTurnThreshold > 0 {
		values["model_post_turn_compact_threshold_percent"] = postTurnThreshold
	}
	return &config.Config{Values: values}
}

func TestAsyncCompactionEligibilityLikeRust(t *testing.T) {
	status := &compact.TokenStatus{ActiveContextTokens: 70}
	cases := []struct {
		name         string
		cfg          *config.Config
		support      model.RemoteCompactionSupport
		phase        compact.Phase
		status       *compact.TokenStatus
		window       int64
		hasTurnState bool
		want         bool
	}{
		{name: "eligible", cfg: asyncCompactionTestConfig(65, false, 0), support: model.RemoteCompactionV2, phase: compact.PhaseMidTurn, status: status, window: 100, want: true},
		{name: "feature off", cfg: asyncCompactionTestConfig(65, false, 0), support: model.RemoteCompactionUnsupported, phase: compact.PhaseMidTurn, status: status, window: 100},
		{name: "token budget precedence", cfg: asyncCompactionTestConfig(65, true, 0), support: model.RemoteCompactionV2, phase: compact.PhaseMidTurn, status: status, window: 100},
		{name: "foreground token limit", cfg: asyncCompactionTestConfig(65, false, 0), support: model.RemoteCompactionV2, phase: compact.PhaseMidTurn, status: &compact.TokenStatus{ActiveContextTokens: 70, ShouldCompact: true}, window: 100},
		{name: "under threshold", cfg: asyncCompactionTestConfig(65, false, 0), support: model.RemoteCompactionV2, phase: compact.PhaseMidTurn, status: &compact.TokenStatus{ActiveContextTokens: 60}, window: 100},
		{name: "preturn needs turn state", cfg: asyncCompactionTestConfig(65, false, 0), support: model.RemoteCompactionV2, phase: compact.PhasePreTurn, status: status, window: 100},
		{name: "postturn threshold reached", cfg: asyncCompactionTestConfig(65, false, 60), support: model.RemoteCompactionV2, phase: compact.PhasePostTurn, status: status, window: 100},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := asyncCompactionEligible(testCase.cfg, testCase.support, testCase.phase, testCase.status, testCase.window, testCase.hasTurnState); got != testCase.want {
				t.Fatalf("asyncCompactionEligible() = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestCaptureBackgroundCompactionInputKeepsOriginalPrefixLikeRust(t *testing.T) {
	history := []compact.Item{
		{ID: "one", Type: "message", Role: "user", Text: "first"},
		{ID: "two", Type: "message", Role: "assistant", Text: "second"},
	}
	input := captureBackgroundCompactionInput(history, 2, "turn-state", compactionResponseWindow{WindowNumber: 3}, "instructions", "gpt-test", "hash", 7, 100_000)
	if input.Prefix.Len != 2 || input.Prefix.AuthOwnerGeneration == nil || *input.Prefix.AuthOwnerGeneration != 7 {
		t.Fatalf("captured prefix = %#v", input.Prefix)
	}
	if input.Window.WindowNumber != 3 || input.TurnState != "turn-state" || input.AuthOwnerGeneration != 7 {
		t.Fatalf("captured routing state = %#v", input)
	}
	trimmed := captureBackgroundCompactionInput(history, 2, "", compactionResponseWindow{}, "instructions", "gpt-test", "hash", 7, 1)
	if trimmed.Prefix.Len != 2 || len(trimmed.History) >= len(history) {
		t.Fatalf("trimmed capture = prefix %#v history %#v", trimmed.Prefix, trimmed.History)
	}
}

func TestAsyncCompactionStateDeduplicatesAndRetainsLatestCandidateLikeRust(t *testing.T) {
	state := newAsyncCompactionState()
	input := backgroundCompactionInput{
		History:             []compact.Item{{ID: "one", Type: "message", Role: "user", Text: "first"}},
		ModelSlug:           "gpt-test",
		CompactionHash:      "hash",
		AuthOwnerGeneration: 1,
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	start := func(ctx context.Context, got backgroundCompactionInput) (*backgroundCompactionCandidate, error) {
		started <- struct{}{}
		select {
		case <-release:
			return &backgroundCompactionCandidate{Result: &compact.Result{Status: compact.StatusCompleted, Summary: "candidate"}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	owner := func() uint64 { return 1 }
	if !state.MaybeStart(context.Background(), input, owner, start) {
		t.Fatal("first attempt did not start")
	}
	if state.MaybeStart(context.Background(), input, owner, start) {
		t.Fatal("same history/model/hash/auth owner started twice")
	}
	<-started
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	var candidate *backgroundCompactionCandidate
	for candidate == nil && time.Now().Before(deadline) {
		candidate = state.TakeCandidate()
		if candidate == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if candidate == nil || candidate.Result == nil || candidate.Result.Summary != "candidate" {
		t.Fatalf("candidate = %#v", candidate)
	}
	if state.TakeCandidate() != nil {
		t.Fatal("candidate was not consumed")
	}

	changed := input
	changed.History = []compact.Item{{ID: "two", Type: "message", Role: "user", Text: "changed"}}
	if !state.MaybeStart(context.Background(), changed, owner, start) {
		t.Fatal("changed history did not start a new attempt")
	}
	<-started
}

func TestAsyncCompactionShutdownCancelsAndDrainsLikeRust(t *testing.T) {
	state := newAsyncCompactionState()
	canceled := make(chan struct{})
	input := backgroundCompactionInput{
		History: []compact.Item{{ID: "one", Type: "message", Role: "user", Text: "first"}},
	}
	if !state.MaybeStart(context.Background(), input, nil, func(ctx context.Context, _ backgroundCompactionInput) (*backgroundCompactionCandidate, error) {
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}) {
		t.Fatal("attempt did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := state.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("shutdown did not cancel the background request")
	}
	if state.MaybeStart(context.Background(), input, nil, func(context.Context, backgroundCompactionInput) (*backgroundCompactionCandidate, error) {
		return nil, errors.New("must not start after shutdown")
	}) {
		t.Fatal("attempt started after shutdown")
	}
}

func TestAsyncCompactionAuthOwnerDeduplicatesAttemptsLikeRust(t *testing.T) {
	state := newAsyncCompactionState()
	input := backgroundCompactionInput{
		History:             []compact.Item{{ID: "one", Type: "message", Role: "user", Text: "first"}},
		AuthOwnerGeneration: 1,
	}
	var calls atomic.Int64
	owner := func() uint64 { return 2 }
	if state.MaybeStart(context.Background(), input, owner, func(context.Context, backgroundCompactionInput) (*backgroundCompactionCandidate, error) {
		calls.Add(1)
		return nil, nil
	}) {
		t.Fatal("attempt started after the auth owner changed")
	}
	if calls.Load() != 0 {
		t.Fatal("runner called with a stale auth owner")
	}
}
