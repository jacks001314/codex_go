package appserver

import "strings"

// maxRetainedAnalyticsTurns bounds the turns whose analytics evidence the
// router retains when lifecycle facts are dropped, mirroring Rust's
// MAX_PENDING_TURNS (analytics/src/reducer.rs, #55847). Evicting the oldest
// turn may discard pending tool or review analytics for a turn that never
// completed, exactly like upstream.
const maxRetainedAnalyticsTurns = 256

// registerAnalyticsTurn records that one turn now retains analytics evidence
// and evicts the oldest retained turns past the bound.
func (r *RuntimeRouter) registerAnalyticsTurn(threadID string, turnID string) {
	if r == nil {
		return
	}
	key, ok := analyticsTurnKey(threadID, turnID)
	if !ok {
		return
	}
	r.retainedAnalyticsTurnsMu.Lock()
	if r.analyticsTurnKnown == nil {
		r.analyticsTurnKnown = map[string]struct{}{}
	}
	if _, known := r.analyticsTurnKnown[key]; !known {
		r.analyticsTurnKnown[key] = struct{}{}
		r.retainedAnalyticsTurns = append(r.retainedAnalyticsTurns, key)
	}
	var evicted []string
	for len(r.retainedAnalyticsTurns) > maxRetainedAnalyticsTurns {
		oldest := r.retainedAnalyticsTurns[0]
		r.retainedAnalyticsTurns = r.retainedAnalyticsTurns[1:]
		delete(r.analyticsTurnKnown, oldest)
		evicted = append(evicted, oldest)
	}
	r.retainedAnalyticsTurnsMu.Unlock()
	for _, evictedKey := range evicted {
		evictedThreadID, evictedTurnID, _ := strings.Cut(evictedKey, "\x00")
		r.discardAnalyticsTurn(evictedThreadID, evictedTurnID)
	}
}

// analyticsTurnKey names one (thread, turn) pair.
func analyticsTurnKey(threadID string, turnID string) (string, bool) {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return "", false
	}
	return threadID + "\x00" + turnID, true
}

// forgetThreadAnalyticsTurns releases a closing thread's ordering entries so a
// long-lived process does not accumulate dead turn keys.
func (r *RuntimeRouter) forgetThreadAnalyticsTurns(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	prefix := threadID + "\x00"
	r.retainedAnalyticsTurnsMu.Lock()
	defer r.retainedAnalyticsTurnsMu.Unlock()
	kept := r.retainedAnalyticsTurns[:0]
	for _, key := range r.retainedAnalyticsTurns {
		if strings.HasPrefix(key, prefix) {
			delete(r.analyticsTurnKnown, key)
			continue
		}
		kept = append(kept, key)
	}
	r.retainedAnalyticsTurns = kept
}

// discardAnalyticsTurn drops every analytics record retained for one turn,
// mirroring Rust's AnalyticsReducer::discard_turn.
func (r *RuntimeRouter) discardAnalyticsTurn(threadID string, turnID string) {
	if r == nil {
		return
	}
	key, ok := analyticsTurnKey(threadID, turnID)
	if !ok {
		return
	}
	turnPrefix := key + "\x00"
	r.sampledToolCallsMu.Lock()
	delete(r.sampledToolCalls, key)
	delete(r.codeModeChildCalls, key)
	delete(r.pendingToolEvents, key)
	r.sampledToolCallsMu.Unlock()

	r.clearToolItemReviewSummaries(threadID, turnID)

	r.toolItemModelContextsMu.Lock()
	for stored := range r.toolItemModelContexts {
		if strings.HasPrefix(stored, turnPrefix) {
			delete(r.toolItemModelContexts, stored)
		}
	}
	r.toolItemModelContextsMu.Unlock()

	r.unifiedExecAnalyticsMu.Lock()
	for stored := range r.unifiedExecAnalytics {
		if strings.HasPrefix(stored, turnPrefix) {
			delete(r.unifiedExecAnalytics, stored)
		}
	}
	r.unifiedExecAnalyticsMu.Unlock()

	r.forgetMCPToolCallElicitationTurn(threadID, turnID)
}

// discardThreadAnalyticsTurns drops a closing thread's retained analytics
// evidence, mirroring Rust's ThreadClosed cleanup (#55847).
func (r *RuntimeRouter) discardThreadAnalyticsTurns(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.forgetThreadAnalyticsTurns(threadID)
	r.clearThreadToolItemReviewSummaries(threadID)
	prefix := threadID + "\x00"
	r.toolItemModelContextsMu.Lock()
	for stored := range r.toolItemModelContexts {
		if strings.HasPrefix(stored, prefix) {
			delete(r.toolItemModelContexts, stored)
		}
	}
	r.toolItemModelContextsMu.Unlock()
	r.unifiedExecAnalyticsMu.Lock()
	for stored := range r.unifiedExecAnalytics {
		if strings.HasPrefix(stored, prefix) {
			delete(r.unifiedExecAnalytics, stored)
		}
	}
	r.unifiedExecAnalyticsMu.Unlock()
}

// forgetMCPToolCallElicitationTurn drops the queued classifications of one turn.
func (r *RuntimeRouter) forgetMCPToolCallElicitationTurn(threadID string, turnID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	r.mcpElicitationsMu.Lock()
	defer r.mcpElicitationsMu.Unlock()
	kept := r.pendingMCPElicitations[:0]
	for _, pending := range r.pendingMCPElicitations {
		if pending.threadID == threadID && pending.turnID == turnID {
			continue
		}
		kept = append(kept, pending)
	}
	r.pendingMCPElicitations = kept
}
