package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/compact"
	"codex_go/config"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust's `sess.previous_turn_settings()`: a thread answers with the
// model and compaction hash its last turn-context record carried, and a brand
// new thread has none.
func TestRuntimePreviousTurnSettingsFollowRecordedTurnContextLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "gpt-previous", ContextWindow: 200000, CompHash: "hash-previous"},
		}})),
	})
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: home}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}

	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	if _, _, _, ok := router.runtimePreviousTurnSettings(threadID, nil); ok {
		t.Fatal("a thread with no recorded turn reported previous settings")
	}
	record, err := store.Read(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("read started thread error: %v", err)
	}
	router.recordRuntimeTurnContext(threadID, "turn-1", "turn-1", &appTurnRunConfig{
		Model:              "gpt-previous",
		ApprovalPolicy:     "on-request",
		ReasoningEffort:    "high",
		Personality:        "friendly",
		CyberAccessProgram: "daybreak_blue",
	}, record)

	previousModel, previousHash, previousProgram, ok := router.runtimePreviousTurnSettings(threadID, nil)
	if !ok || previousModel != "gpt-previous" || previousHash != "hash-previous" || previousProgram != "daybreak_blue" {
		t.Fatalf("previous turn settings = %q, %q, %q, %v; want gpt-previous, hash-previous, daybreak_blue, true", previousModel, previousHash, previousProgram, ok)
	}

	// The record is durable: a different router reading the same rollout
	// recovers the same previous-turn settings (Rust rollout reconstruction).
	rolloutPath := router.services.ThreadRouter.threadRolloutPath(record)
	cold, err := rollout.RecordFromPath(rolloutPath, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	coldModel, coldHash, coldProgram, coldOK := rollout.TurnContextSettings(cold.Metadata.TurnContext)
	if !coldOK || coldModel != "gpt-previous" || coldHash != "hash-previous" || coldProgram != "daybreak_blue" {
		t.Fatalf("cold previous settings = %q, %q, %q, %v", coldModel, coldHash, coldProgram, coldOK)
	}
	raw, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatalf("ReadFile(rollout) error = %v", err)
	}
	if !strings.Contains(string(raw), `"turn_context"`) || !strings.Contains(string(raw), `"hash-previous"`) || !strings.Contains(string(raw), `"cyber_access_program":"daybreak_blue"`) {
		t.Fatalf("rollout is missing the turn-context record:\n%s", raw)
	}
}

// Mirrors Rust's `maybe_run_previous_model_inline_compact` decision: compact
// with the previous model when its compaction hash changed, or when switching
// to a smaller context-window model whose limit is already reached.
func TestPreviousModelCompactionDecisionLikeRust(t *testing.T) {
	const (
		threadID     = "thread-previous-model"
		previous     = "gpt-previous"
		current      = "gpt-current"
		largerModel  = "gpt-larger"
		smallerModel = "gpt-smaller"
	)
	models := []model.ModelInfo{
		{Slug: previous, ContextWindow: 200000, CompHash: "hash-previous"},
		{Slug: current, ContextWindow: 100, CompHash: "hash-current"},
		{Slug: largerModel, ContextWindow: 400000, CompHash: "hash-previous"},
		{Slug: smallerModel, ContextWindow: 50, CompHash: "hash-current"},
	}
	cases := []struct {
		name          string
		previousModel string
		previousHash  string
		currentModel  string
		tokens        int
		wantRun       bool
		wantReason    compact.Reason
	}{
		{name: "hash changed", previousModel: previous, previousHash: "hash-old", currentModel: current, wantRun: true, wantReason: compact.ReasonCompHashChanged},
		{name: "hash missing on one side", previousModel: previous, previousHash: "", currentModel: current, tokens: 500, wantRun: true, wantReason: compact.ReasonModelSwitch},
		{name: "same model same hash", previousModel: current, previousHash: "hash-current", currentModel: current, tokens: 500},
		{name: "previous window not larger", previousModel: smallerModel, previousHash: "hash-current", currentModel: current, tokens: 500},
		{name: "downsized model under its limit", previousModel: previous, previousHash: "hash-current", currentModel: current, tokens: 10},
		{name: "model downshift at the limit", previousModel: previous, previousHash: "hash-current", currentModel: current, tokens: 500, wantRun: true, wantReason: compact.ReasonModelSwitch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			store := session.NewStore(filepath.Join(home, "sessions"))
			router := NewRuntimeRouter(RuntimeServices{
				ThreadRouter: NewRouter(store),
				Config:       config.NewConfigService(home),
				Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: models})),
			})
			router.registerThreadForTest(t, threadID, tc.tokens)
			payload, err := json.Marshal(rollout.TurnContextRecord{Model: tc.previousModel, CompHash: tc.previousHash})
			if err != nil {
				t.Fatal(err)
			}
			router.setRuntimeTurnContext(threadID, payload)
			record, err := store.Read(session.ThreadID(threadID), true, true)
			if err != nil {
				t.Fatalf("read thread error: %v", err)
			}
			decision := router.previousModelCompactionForTurn(threadID, record, &appTurnRunConfig{Model: tc.currentModel}, &turn.TurnStartParams{ThreadID: threadID})
			if decision.Run != tc.wantRun {
				t.Fatalf("decision.Run = %v, want %v (decision=%+v)", decision.Run, tc.wantRun, decision)
			}
			if tc.wantRun && (decision.Reason != tc.wantReason || decision.PreviousModel != tc.previousModel) {
				t.Fatalf("decision = %+v, want reason %q and previous model %q", decision, tc.wantReason, tc.previousModel)
			}
		})
	}
}

// Mirrors Rust's `TurnContext::model_context_window`: an explicit
// `model_context_window` config override wins over the catalog value.
func TestRuntimeModelContextWindowPrefersConfigOverrideLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       config.NewConfigService(home),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "gpt-catalog", ContextWindow: 200000},
		}})),
	})
	if got := router.runtimeModelContextWindow("gpt-catalog", &turn.TurnStartParams{CWD: home}); got != 200000 {
		t.Fatalf("catalog window = %d, want 200000", got)
	}
	params := &turn.TurnStartParams{CWD: home, Config: map[string]any{"model_context_window": 4096}}
	if got := router.runtimeModelContextWindow("gpt-catalog", params); got != 4096 {
		t.Fatalf("configured window = %d, want 4096", got)
	}
	if got := router.runtimeModelContextWindow("missing-model", params); got != 4096 {
		t.Fatalf("configured window for an unknown model = %d, want 4096", got)
	}
}

// A thread with no recorded context never triggers the previous-model
// compaction (Rust returns early without `previous_turn_settings`).
func TestPreviousModelCompactionSkipsThreadsWithoutRecordedContext(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "gpt-current", ContextWindow: 100, CompHash: "hash-current"},
		}})),
	})
	router.registerThreadForTest(t, "thread-no-context", 500)
	record, err := store.Read("thread-no-context", true, true)
	if err != nil {
		t.Fatalf("read thread error: %v", err)
	}
	decision := router.previousModelCompactionForTurn("thread-no-context", record, &appTurnRunConfig{Model: "gpt-current"}, &turn.TurnStartParams{ThreadID: "thread-no-context"})
	if decision.Run {
		t.Fatalf("decision = %+v, want no run", decision)
	}
}

// Mirrors Rust #51117
// (`compact_model_fallback::should_retry_with_current_model`): a failed
// previous-model compaction retries with the selected model only when the error
// is not an aborted/interrupted turn or a budget stop, the model/access-program
// pair actually changes, and the selected provider is OpenAI with a cached
// authentication the Codex backend or an API key can serve.
func TestShouldRetryCompactionWithCurrentModelLikeRust(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv("CODEX_ACCESS_TOKEN", "")

	home := t.TempDir()
	// A configured provider that is not the built-in OpenAI one, so the
	// provider arm can be observed.
	configBody := "[model_providers.mock_provider]\nname = \"Mock\"\nbase_url = \"https://mock.example/v1\"\nwire_api = \"responses\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	account := auth.NewAccountManager()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Account: account})

	previous := compactionAttemptIdentity{Model: "gpt-previous", CyberAccessProgram: "standard"}
	current := compactionAttemptIdentity{Model: "gpt-current", CyberAccessProgram: "standard", ProviderID: model.OpenAIProviderID}
	// The same model and program retried with a different provider still cannot
	// change the outcome of the attempt.
	currentSamePair := current
	currentSamePair.Model = "gpt-previous"

	cases := []struct {
		name     string
		err      error
		previous compactionAttemptIdentity
		current  compactionAttemptIdentity
		auth     *auth.AuthDotJSON
		want     bool
	}{
		{name: "context canceled", err: context.Canceled, previous: previous, current: current, auth: apiKeyAuth(), want: false},
		{name: "wrapped cancellation", err: errors.Join(errors.New("compact failed"), context.Canceled), previous: previous, current: current, auth: apiKeyAuth(), want: false},
		{name: "session budget exceeded", err: ErrSessionBudgetExceeded, previous: previous, current: current, auth: apiKeyAuth(), want: false},
		{name: "nil error", err: nil, previous: previous, current: current, auth: apiKeyAuth(), want: false},
		{name: "same model and program", err: errors.New("server overloaded"), previous: previous, current: currentSamePair, auth: apiKeyAuth(), want: false},
		{name: "same model with a changed program", err: errors.New("server overloaded"), previous: previous, current: func() compactionAttemptIdentity {
			identity := currentSamePair
			identity.CyberAccessProgram = "daybreak_blue"
			return identity
		}(), auth: apiKeyAuth(), want: true},
		{name: "changed model with an api key", err: errors.New("server overloaded"), previous: previous, current: current, auth: apiKeyAuth(), want: true},
		{name: "changed model with a codex backend session", err: errors.New("server overloaded"), previous: previous, current: current, auth: chatGPTAuth(), want: true},
		{name: "changed model without cached auth", err: errors.New("server overloaded"), previous: previous, current: current, want: false},
		{name: "changed model against another provider", err: errors.New("server overloaded"), previous: previous, current: func() compactionAttemptIdentity {
			identity := current
			identity.ProviderID = "mock_provider"
			return identity
		}(), auth: apiKeyAuth(), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account.ApplyAuthSnapshot(tc.auth)
			if got := router.shouldRetryCompactionWithCurrentModel(tc.err, tc.previous, tc.current); got != tc.want {
				t.Fatalf("shouldRetryCompactionWithCurrentModel(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// apiKeyAuth is an OpenAI API-key credential, which Rust accepts for a
// current-model compaction retry (`is_api_key_auth`).
func apiKeyAuth() *auth.AuthDotJSON {
	return &auth.AuthDotJSON{AuthMode: "apikey", OpenAIAPIKey: "sk-test"}
}

// chatGPTAuth is a Codex-backend session, which Rust accepts for a
// current-model compaction retry (`uses_codex_backend`).
func chatGPTAuth() *auth.AuthDotJSON {
	snapshot := auth.FromChatGPTAuthTokens("chatgpt-token", "account-1", nil)
	return &snapshot
}

// registerThreadForTest creates a store-backed thread whose history estimates to
// roughly `tokens` tokens, so the auto-compact limit checks have real input.
func (r *RuntimeRouter) registerThreadForTest(t *testing.T, threadID string, tokens int) {
	t.Helper()
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		t.Fatal("router store is not configured")
	}
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Metadata:  session.Metadata{CWD: t.TempDir()},
	}
	if tokens > 0 {
		record.Items = []session.Item{{
			ID:   "item-1",
			Type: "message",
			Text: strings.Repeat("x", tokens*4),
		}}
	}
	if err := r.services.ThreadRouter.store.Create(record); err != nil {
		t.Fatalf("create thread error: %v", err)
	}
}

// failingFirstCompactRunner fails the previous-model attempt and lets the retry
// succeed, so a test can observe both attempts.
type failingFirstCompactRunner struct {
	err      error
	attempts int
	inner    *recordingCompactRunner
}

func (r *failingFirstCompactRunner) Compact(ctx context.Context, request *compact.Request) (*compact.Result, error) {
	r.attempts++
	if r.attempts == 1 {
		return nil, r.err
	}
	return r.inner.Compact(ctx, request)
}

// Mirrors Rust's previous-model compaction fallback: the previous-model attempt
// runs with a remote-only compaction, and a failure retries with the selected
// model only when #51117's predicate allows it, while an interrupted turn or a
// missing cached auth stops after the first attempt.
func TestRunPreviousModelInlineCompactFallsBackLikeRust(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv("CODEX_ACCESS_TOKEN", "")

	cases := []struct {
		name         string
		firstErr     error
		auth         *auth.AuthDotJSON
		wantAttempts int
		wantErr      bool
	}{
		{name: "retryable failure retries with the current model", firstErr: errors.New("server overloaded"), auth: apiKeyAuth(), wantAttempts: 2},
		{name: "interrupted turn does not retry", firstErr: context.Canceled, auth: apiKeyAuth(), wantAttempts: 1, wantErr: true},
		{name: "budget stop does not retry", firstErr: ErrSessionBudgetExceeded, auth: apiKeyAuth(), wantAttempts: 1, wantErr: true},
		// Rust #51117: without a cached Codex-backend or API-key credential the
		// selected model cannot be retried.
		{name: "retryable failure without cached auth does not retry", firstErr: errors.New("server overloaded"), wantAttempts: 1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const threadID = "thread-previous-model-fallback"
			home := t.TempDir()
			store := session.NewStore(filepath.Join(home, "sessions"))
			runner := &failingFirstCompactRunner{err: tc.firstErr, inner: &recordingCompactRunner{summary: "fallback summary"}}
			account := auth.NewAccountManager()
			account.ApplyAuthSnapshot(tc.auth)
			router := NewRuntimeRouter(RuntimeServices{
				ThreadRouter:  NewRouter(store),
				Config:        config.NewConfigService(home),
				Account:       account,
				CompactRunner: runner,
				Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
					{Slug: "gpt-previous", ContextWindow: 200000, CompHash: "hash-previous"},
					{Slug: "gpt-current", ContextWindow: 200000, CompHash: "hash-current"},
				}})),
			})
			router.registerThreadForTest(t, threadID, 0)
			payload, err := json.Marshal(rollout.TurnContextRecord{Model: "gpt-previous", CompHash: "hash-old"})
			if err != nil {
				t.Fatal(err)
			}
			router.setRuntimeTurnContext(threadID, payload)
			record, err := store.Read(threadID, true, true)
			if err != nil {
				t.Fatalf("read thread error: %v", err)
			}
			ran, runErr := router.runPreviousModelInlineCompact(
				context.Background(), threadID, "turn-1", "",
				&turn.TurnStartParams{ThreadID: threadID},
				&appTurnRunConfig{Model: "gpt-current"}, record,
			)
			if !ran {
				t.Fatal("previous-model compaction did not run for a changed compaction hash")
			}
			if runner.attempts != tc.wantAttempts {
				t.Fatalf("compaction attempts = %d, want %d", runner.attempts, tc.wantAttempts)
			}
			if (runErr != nil) != tc.wantErr {
				t.Fatalf("runPreviousModelInlineCompact() error = %v, wantErr %v", runErr, tc.wantErr)
			}
		})
	}
}
