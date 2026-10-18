package appserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/telemetry"
)

const (
	turnCostRequestTimeout = 15 * time.Second
	// Rust polls an unsettled turn immediately, then every 150 seconds up to
	// five stalled attempts.
	turnCostDefaultPollInterval = 150 * time.Second
	turnCostDefaultMaxAttempts  = 5
	maxTrackedTurnCostTurns     = 4096
	maxTrackedTurnCostResponses = 4096
)

type turnCostObservation struct {
	ThreadID    string
	TurnID      string
	ResponseIDs []string
	Interrupted bool
}

type turnCostEntry struct {
	threadID    string
	turnID      string
	responseIDs map[string]struct{}
}

// turnCostTracker records completed response IDs while a turn is running.
// Finish removes the entry before handing an immutable observation to the
// best-effort cost worker.
type turnCostTracker struct {
	mu           sync.Mutex
	turns        map[string]*turnCostEntry
	pollInterval time.Duration
	maxAttempts  int
}

func newTurnCostTracker() *turnCostTracker {
	return &turnCostTracker{
		turns:        map[string]*turnCostEntry{},
		pollInterval: turnCostDefaultPollInterval,
		maxAttempts:  turnCostDefaultMaxAttempts,
	}
}

func turnCostEntryKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func (t *turnCostTracker) recordResponse(threadID string, turnID string, responseID string) {
	if t == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	responseID = strings.TrimSpace(responseID)
	if threadID == "" || turnID == "" || responseID == "" {
		return
	}
	key := turnCostEntryKey(threadID, turnID)
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.turns[key]
	if entry == nil {
		if len(t.turns) >= maxTrackedTurnCostTurns {
			return
		}
		entry = &turnCostEntry{
			threadID:    threadID,
			turnID:      turnID,
			responseIDs: map[string]struct{}{},
		}
		t.turns[key] = entry
	}
	if len(entry.responseIDs) >= maxTrackedTurnCostResponses {
		delete(t.turns, key)
		return
	}
	entry.responseIDs[responseID] = struct{}{}
}

func (t *turnCostTracker) finish(threadID string, turnID string, interrupted bool) *turnCostObservation {
	if t == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return nil
	}
	key := turnCostEntryKey(threadID, turnID)
	t.mu.Lock()
	entry := t.turns[key]
	delete(t.turns, key)
	t.mu.Unlock()
	if entry == nil || len(entry.responseIDs) == 0 {
		return nil
	}
	responseIDs := make([]string, 0, len(entry.responseIDs))
	for responseID := range entry.responseIDs {
		responseIDs = append(responseIDs, responseID)
	}
	sort.Strings(responseIDs)
	return &turnCostObservation{
		ThreadID:    entry.threadID,
		TurnID:      entry.turnID,
		ResponseIDs: responseIDs,
		Interrupted: interrupted,
	}
}

func (r *RuntimeRouter) turnCostTracker() *turnCostTracker {
	if r == nil {
		return nil
	}
	r.turnCostMu.Lock()
	defer r.turnCostMu.Unlock()
	if r.turnCosts == nil {
		r.turnCosts = newTurnCostTracker()
	}
	return r.turnCosts
}

func (r *RuntimeRouter) recordTurnCostResponse(threadID string, turnID string, responseID string) {
	r.turnCostTracker().recordResponse(threadID, turnID, responseID)
}

func (r *RuntimeRouter) observeTurnCostCompletion(threadID string, turnID string, interrupted bool) {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	observation := r.turnCostTracker().finish(threadID, turnID, interrupted)
	if observation == nil {
		return
	}
	go r.runTurnCostObservation(observation)
}

func (r *RuntimeRouter) runTurnCostObservation(observation *turnCostObservation) {
	if r == nil || observation == nil || r.services.TurnMetrics == nil {
		return
	}
	tracker := r.turnCostTracker()
	tracker.mu.Lock()
	pollInterval := tracker.pollInterval
	maxAttempts := tracker.maxAttempts
	tracker.mu.Unlock()
	if pollInterval <= 0 {
		pollInterval = time.Millisecond
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	turnIDs := []string{observation.TurnID}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), turnCostRequestTimeout)
		costs, err := r.queryTurnCosts(ctx, observation.ThreadID, turnIDs)
		cancel()
		if err == nil && r.emitSettledTurnCost(observation, costs) {
			return
		}
		if err != nil {
			// Never log the backend body: turn-cost errors can contain arbitrary
			// response text.
			slog.Debug("turn cost query failed", "status", turnCostHTTPStatus(err))
		}
		if attempt+1 < maxAttempts {
			time.Sleep(pollInterval)
		}
	}
}

func (r *RuntimeRouter) queryTurnCosts(ctx context.Context, threadID string, turnIDs []string) ([]chatgptapi.ChatgptThreadTurnCosts, error) {
	if r == nil {
		return nil, nil
	}
	if r.turnCostQuery != nil {
		return r.turnCostQuery(ctx, threadID, turnIDs)
	}
	snapshot := r.requireAccount().AuthSnapshot()
	if !turnCostAuthUsesChatGPT(snapshot) {
		return nil, nil
	}
	client, err := r.accountBackendClient(snapshot)
	if err != nil {
		return nil, err
	}
	return client.GetTurnCosts(ctx, map[string][]string{threadID: turnIDs})
}

func turnCostAuthUsesChatGPT(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens":
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) emitSettledTurnCost(observation *turnCostObservation, costs []chatgptapi.ChatgptThreadTurnCosts) bool {
	if r == nil || observation == nil || len(observation.ResponseIDs) == 0 || r.services.TurnMetrics == nil {
		return false
	}
	observed := make(map[string]struct{}, len(observation.ResponseIDs))
	for _, responseID := range observation.ResponseIDs {
		observed[strings.TrimSpace(responseID)] = struct{}{}
	}
	for _, thread := range costs {
		if strings.TrimSpace(thread.ThreadID) != observation.ThreadID {
			continue
		}
		for _, cost := range thread.Turns {
			if strings.TrimSpace(cost.TurnID) != observation.TurnID {
				continue
			}
			if cost.EstimatedUsageUSDMicros == nil || *cost.EstimatedUsageUSDMicros < 0 || len(cost.SettledResponseIDs) == 0 {
				return false
			}
			settled := make(map[string]struct{}, len(cost.SettledResponseIDs))
			for _, responseID := range cost.SettledResponseIDs {
				settled[strings.TrimSpace(responseID)] = struct{}{}
			}
			for responseID := range observed {
				if _, ok := settled[responseID]; !ok {
					return false
				}
			}
			tags := map[string]string{
				"turn.id":          observation.TurnID,
				"conversation.id":  observation.ThreadID,
				"turn.interrupted": strconv.FormatBool(observation.Interrupted),
				"auth_mode":        "Chatgpt",
			}
			r.services.TurnMetrics.Counter(telemetry.TurnCostMicroUSDMetric, int(*cost.EstimatedUsageUSDMicros), tags)
			return true
		}
	}
	return false
}

func turnCostHTTPStatus(err error) int {
	var statusErr *chatgptapi.HTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusRequestTimeout
	}
	return 0
}
