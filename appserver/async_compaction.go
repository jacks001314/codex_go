package appserver

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"codex_go/compact"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// asyncCompactionThresholdPercent is Rust's
// `async_compaction_threshold_percent`; token budgets take precedence over
// speculative compaction.
func asyncCompactionThresholdPercent(cfg *config.Config) (int, bool) {
	if cfg == nil {
		return 0, false
	}
	settings, err := cfg.AsyncCompactionConfig()
	if err != nil || settings == nil || !settings.Enabled {
		return 0, false
	}
	if features.Enabled(cfg.FeatureSettings(), "token_budget") {
		return 0, false
	}
	return settings.ModelAsyncCompactThresholdPercent, true
}

// asyncCompactionEligible mirrors Rust `async_compaction::maybe_start`'s
// preconditions. The background task is only started when the provider speaks
// remote compaction V2, the turn is below foreground token limits, and the
// active context crossed the configured percentage.
func asyncCompactionEligible(cfg *config.Config, support model.RemoteCompactionSupport, phase compact.Phase, status *compact.TokenStatus, usableWindow int64, hasTurnState bool) bool {
	threshold, ok := asyncCompactionThresholdPercent(cfg)
	if !ok || support != model.RemoteCompactionV2 || status == nil || status.ShouldCompact {
		return false
	}
	if phase == compact.PhasePreTurn && !hasTurnState {
		return false
	}
	if phase == compact.PhasePostTurn && turnEndCompactionThresholdReached(cfg, status, usableWindow) {
		return false
	}
	if usableWindow <= 0 {
		return false
	}
	return int64(status.ActiveContextTokens)*100 >= usableWindow*int64(threshold)
}

type compactionAttemptKey struct {
	HistoryVersion      uint64
	ModelSlug           string
	CompactionHash      string
	AuthOwnerGeneration uint64
}

type backgroundCompactionInput struct {
	Prefix              compact.CompactionPrefix
	History             []compact.Item
	TurnState           string
	Window              compactionResponseWindow
	BaseInstructions    string
	ModelSlug           string
	CompactionHash      string
	AuthOwnerGeneration uint64
}

type backgroundCompactionCandidate struct {
	Key      compactionAttemptKey
	Prefix   compact.CompactionPrefix
	Input    backgroundCompactionInput
	Result   *compact.Result
	Response *model.AgentResponse
}

// captureBackgroundCompactionInput snapshots the history a speculative request
// will summarize. Later history items remain in place when the candidate is
// installed.
func captureBackgroundCompactionInput(history []compact.Item, prefixLen int, turnState string, window compactionResponseWindow, baseInstructions string, modelSlug string, compactionHash string, authOwnerGeneration uint64, usableWindow int64) backgroundCompactionInput {
	generation := compact.HistoryVersion(history)
	prefixLen = max(0, min(prefixLen, len(history)))
	history = compact.TrimHistoryToContextWindow(history, int(usableWindow), baseInstructions)
	authOwner := authOwnerGeneration
	return backgroundCompactionInput{
		Prefix: compact.CompactionPrefix{
			Generation:          generation,
			Len:                 prefixLen,
			AuthOwnerGeneration: &authOwner,
		},
		History:             append([]compact.Item(nil), history...),
		TurnState:           turnState,
		Window:              window,
		BaseInstructions:    baseInstructions,
		ModelSlug:           modelSlug,
		CompactionHash:      compactionHash,
		AuthOwnerGeneration: authOwnerGeneration,
	}
}

type backgroundCompactionRunner func(context.Context, backgroundCompactionInput) (*backgroundCompactionCandidate, error)

// asyncCompactionState holds the newest candidate and cancels in-flight
// speculative requests when its session shuts down. The turn loop is wired in a
// later layer; this state provides capture, deduplication, and lifecycle.
type asyncCompactionState struct {
	mu           sync.Mutex
	lastAttempt  *compactionAttemptKey
	candidate    *backgroundCompactionCandidate
	resultCh     chan *backgroundCompactionCandidate
	taskCtx      context.Context
	cancel       context.CancelFunc
	tasks        sync.WaitGroup
	shuttingDown bool
}

func newAsyncCompactionState() *asyncCompactionState {
	return &asyncCompactionState{}
}

// MaybeStart launches one background attempt for a captured history. Repeated
// calls with the same history/model/hash/auth owner are ignored.
func (s *asyncCompactionState) MaybeStart(ctx context.Context, input backgroundCompactionInput, currentAuthOwner func() uint64, run backgroundCompactionRunner) bool {
	if s == nil || run == nil || len(input.History) == 0 {
		return false
	}
	if currentAuthOwner != nil && currentAuthOwner() != input.AuthOwnerGeneration {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key := compactionAttemptKey{
		HistoryVersion:      compact.HistoryVersion(input.History),
		ModelSlug:           input.ModelSlug,
		CompactionHash:      input.CompactionHash,
		AuthOwnerGeneration: input.AuthOwnerGeneration,
	}
	s.mu.Lock()
	if s.shuttingDown || (s.lastAttempt != nil && *s.lastAttempt == key) {
		s.mu.Unlock()
		return false
	}
	if s.cancel == nil {
		s.taskCtx, s.cancel = context.WithCancel(context.Background())
	}
	taskCtx := s.taskCtx
	s.lastAttempt = &key
	s.candidate = nil
	s.resultCh = make(chan *backgroundCompactionCandidate, 1)
	resultCh := s.resultCh
	s.tasks.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.tasks.Done()
		candidate, err := run(taskCtx, input)
		s.mu.Lock()
		if s.shuttingDown || s.lastAttempt == nil || *s.lastAttempt != key {
			s.mu.Unlock()
			return
		}
		if err != nil || candidate == nil {
			// Rust #55581: a failed attempt must still release the receiver.
			// Leaving the channel unsignalled would make the foreground wait
			// forever instead of falling back to blocking compaction.
			s.mu.Unlock()
			resultCh <- nil
			return
		}
		candidate.Key = key
		candidate.Prefix = input.Prefix
		candidate.Input = input
		s.candidate = candidate
		s.mu.Unlock()
		resultCh <- candidate
	}()
	return true
}

// TakeReady returns a completed candidate only when the captured history,
// model, compaction hash, and auth owner still match the foreground step.
func (s *asyncCompactionState) TakeReady(currentHistory []compact.Item, modelSlug string, compactionHash string, authOwnerGeneration uint64) *backgroundCompactionCandidate {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastAttempt == nil || s.resultCh == nil ||
		s.lastAttempt.HistoryVersion != compact.HistoryVersion(currentHistory) ||
		s.lastAttempt.ModelSlug != strings.TrimSpace(modelSlug) ||
		s.lastAttempt.CompactionHash != strings.TrimSpace(compactionHash) ||
		s.lastAttempt.AuthOwnerGeneration != authOwnerGeneration {
		return nil
	}
	select {
	case candidate := <-s.resultCh:
		s.resultCh = nil
		if candidate == nil || candidate.Prefix.Generation != s.lastAttempt.HistoryVersion {
			s.candidate = nil
			return nil
		}
		s.candidate = nil
		return candidate
	default:
		return nil
	}
}

// WaitForPending awaits the current speculative request when foreground
// compaction is due. It returns nil when the request no longer matches the
// foreground history/model/hash/auth owner.
func (s *asyncCompactionState) WaitForPending(ctx context.Context, currentHistory []compact.Item, modelSlug string, compactionHash string, authOwnerGeneration uint64) *backgroundCompactionCandidate {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.lastAttempt == nil || s.resultCh == nil ||
		s.lastAttempt.HistoryVersion != compact.HistoryVersion(currentHistory) ||
		s.lastAttempt.ModelSlug != strings.TrimSpace(modelSlug) ||
		s.lastAttempt.CompactionHash != strings.TrimSpace(compactionHash) ||
		s.lastAttempt.AuthOwnerGeneration != authOwnerGeneration {
		s.mu.Unlock()
		return nil
	}
	resultCh := s.resultCh
	s.resultCh = nil
	s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case candidate := <-resultCh:
		if candidate == nil || candidate.Prefix.Generation != compact.HistoryVersion(currentHistory) {
			return nil
		}
		s.mu.Lock()
		s.candidate = nil
		s.mu.Unlock()
		return candidate
	case <-ctx.Done():
		return nil
	}
}

// installBackgroundCompactionCandidate validates the captured prefix and
// installs the prepared replacement while preserving later history edits.
func installBackgroundCompactionCandidate(currentHistory []compact.Item, candidate *backgroundCompactionCandidate, initialContext []compact.Item, currentAuthOwnerGeneration uint64) ([]compact.Item, bool) {
	if candidate == nil || candidate.Result == nil || candidate.Result.NewHistory == nil {
		return nil, false
	}
	prefix := candidate.Prefix
	if prefix.Generation == 0 || prefix.Generation != compact.HistoryVersion(currentHistory) ||
		prefix.Len < 0 || prefix.Len > len(currentHistory) {
		return nil, false
	}
	if prefix.AuthOwnerGeneration != nil && *prefix.AuthOwnerGeneration != currentAuthOwnerGeneration {
		return nil, false
	}
	return compact.InstallCompactedHistoryPrefix(currentHistory, candidate.Result.NewHistory, prefix, nil, initialContext), true
}

func (r *RuntimeRouter) maybeStartBackgroundCompaction(ctx context.Context, threadID string, turnID string, params *turn.TurnStartParams, runConfig *appTurnRunConfig, history []compact.Item, phase compact.Phase, status *compact.TokenStatus) bool {
	if r == nil || r.threads == nil || runConfig == nil || runConfig.Config == nil || status == nil || len(history) == 0 {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	support := model.RemoteCompactionUnsupported
	if providerID := strings.TrimSpace(runConfig.ProviderID); r.providerSupportsRemoteCompact(providerID) {
		support = model.RemoteCompactionV2
	}
	info := r.modelInfoForRuntimeWithConfig(runConfig.Model, runConfig.Config)
	if info == nil {
		return false
	}
	usableWindow, ok := info.UsableContextWindow()
	if !ok || !asyncCompactionEligible(runConfig.Config, support, phase, status, usableWindow, true) {
		return false
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return false
	}
	authOwner := r.authOwnerRevisionSnapshot()
	input := captureBackgroundCompactionInput(
		history,
		len(history),
		"", // Go's HTTP runner has no turn WebSocket routing token to replay.
		r.compactionWindowForThread(threadID),
		runConfig.Instructions,
		strings.TrimSpace(runConfig.Model),
		r.modelCompHash(runConfig.Model),
		authOwner,
		usableWindow,
	)
	state := r.threads.asyncCompactionState(threadID)
	return state.MaybeStart(ctx, input, func() uint64 { return r.authOwnerRevisionSnapshot() }, func(runCtx context.Context, captured backgroundCompactionInput) (*backgroundCompactionCandidate, error) {
		request := &compact.Request{
			ThreadID:     threadID,
			TurnID:       turnID,
			Trigger:      compact.TriggerAuto,
			Reason:       compactReasonFromStatus(status),
			Phase:        phase,
			History:      append([]compact.Item(nil), captured.History...),
			StartedAt:    time.Now().UTC(),
			WindowNumber: captured.Window.WindowNumber,
		}
		request.MaxHistoryTokens = compactionHistoryTokenBudget(info)
		if strings.TrimSpace(request.Prompt) == "" {
			prompt, promptErr := r.compactPromptForRecord(record)
			if promptErr != nil {
				return nil, promptErr
			}
			request.Prompt = prompt
		}
		cyberProgram := ""
		if program := appCyberAccessProgramForTurnPointer(params, runConfig.ProviderID); program != nil {
			cyberProgram = *program
		}
		baseRunner := r.compactRunnerForRecord(record, request, runConfig.Model, cyberProgram)
		if baseRunner == nil {
			return nil, errors.New("background compaction runner is unavailable")
		}
		runner := baseRunner
		if agentRunner, ok := baseRunner.(*agentCompactRunner); ok {
			clone := *agentRunner
			clone.clientMetadata = r.compactResponsesClientMetadataForWindow(record, request, runConfig.Model, captured.Window)
			runner = &clone
		}
		result, err := compact.CompactRemotely(runCtx, request, &compact.RemoteOptions{
			Runner:          runner,
			MaxSummaryChars: 4000,
			FallbackToLocal: false,
		})
		if err != nil || result == nil || !result.Succeeded() {
			slog.Warn("background compaction failed", "thread_id", threadID, "phase", phase, "error", err)
			return nil, err
		}
		return &backgroundCompactionCandidate{Result: result}, nil
	})
}

// TakeCandidate returns and clears the newest completed candidate.
func (s *asyncCompactionState) TakeCandidate() *backgroundCompactionCandidate {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := s.candidate
	s.candidate = nil
	return candidate
}

// Shutdown cancels network work and waits for completed usage accounting to
// drain. The caller's deadline bounds the wait.
func (s *asyncCompactionState) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.shuttingDown = true
	s.candidate = nil
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.tasks.Wait()
		close(done)
	}()
	if ctx == nil {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *asyncCompactionState) waitForBackgroundCompactionForTest(ctx context.Context) error {
	if s == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		s.tasks.Wait()
		close(done)
	}()
	if ctx == nil {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *ThreadManager) asyncCompactionState(threadID string) *asyncCompactionState {
	if m == nil || threadID == "" {
		return nil
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	if m.asyncCompaction == nil {
		m.asyncCompaction = map[string]*asyncCompactionState{}
	}
	state := m.asyncCompaction[threadID]
	if state == nil {
		state = newAsyncCompactionState()
		m.asyncCompaction[threadID] = state
	}
	return state
}

func (m *ThreadManager) existingAsyncCompactionState(threadID string) *asyncCompactionState {
	if m == nil || threadID == "" {
		return nil
	}
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	return m.asyncCompaction[threadID]
}

func (m *ThreadManager) shutdownAsyncCompaction(threadID string) {
	if m == nil || threadID == "" {
		return
	}
	m.turnsMu.Lock()
	state := m.asyncCompaction[threadID]
	delete(m.asyncCompaction, threadID)
	m.turnsMu.Unlock()
	if state == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = state.Shutdown(ctx)
}
