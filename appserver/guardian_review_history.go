package appserver

import (
	"strings"

	"codex_go/rollout"
	"codex_go/session"
)

type guardianReviewHistoryState struct {
	items []session.Item
	seen  map[string]struct{}
}

func newGuardianReviewHistoryState(items []session.Item) *guardianReviewHistoryState {
	state := &guardianReviewHistoryState{seen: map[string]struct{}{}}
	state.append(items)
	return state
}

func (s *guardianReviewHistoryState) append(items []session.Item) {
	if s == nil {
		return
	}
	if s.seen == nil {
		s.seen = map[string]struct{}{}
	}
	for _, item := range items {
		key := guardianReviewItemKey(item)
		if _, ok := s.seen[key]; ok {
			continue
		}
		s.seen[key] = struct{}{}
		s.items = append(s.items, cloneRuntimeSessionItem(item))
	}
}

func (s *guardianReviewHistoryState) truncateBeforeTurn(turnID string) {
	if s == nil || strings.TrimSpace(turnID) == "" {
		return
	}
	for index := range s.items {
		if guardianReviewItemTurnID(&s.items[index], index) == strings.TrimSpace(turnID) {
			s.reset(s.items[:index])
			return
		}
	}
}

func (s *guardianReviewHistoryState) reset(items []session.Item) {
	if s == nil {
		return
	}
	s.items = nil
	s.seen = map[string]struct{}{}
	s.append(items)
}

func guardianReviewItemKey(item session.Item) string {
	if id := strings.TrimSpace(item.ID); id != "" {
		return id
	}
	return strings.Join([]string{
		item.Type,
		item.Role,
		item.CallID,
		item.Name,
		item.Text,
	}, "\x00")
}

func guardianReviewItemTurnID(item *session.Item, index int) string {
	if item == nil {
		return ""
	}
	for _, values := range []map[string]any{item.Metadata, item.Data} {
		for _, key := range []string{"turnId", "turn_id"} {
			if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return strings.TrimSpace(item.ID)
}

// captureGuardianReviewHistory merges the current model-facing items into the
// independent review history. Compaction calls this before replacing the
// record, so summarized-away user and tool evidence stays available.
func (r *RuntimeRouter) captureGuardianReviewHistory(threadID string, items []session.Item) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	r.guardianReviewHistoryMu.Lock()
	defer r.guardianReviewHistoryMu.Unlock()
	if r.guardianReviewHistory == nil {
		r.guardianReviewHistory = map[string]*guardianReviewHistoryState{}
	}
	state := r.guardianReviewHistory[threadID]
	if state == nil {
		state = newGuardianReviewHistoryState(nil)
		r.guardianReviewHistory[threadID] = state
	}
	state.append(items)
}

// recordGuardianReviewHistoryAppend mirrors ContextManager::record_item for
// the independent Guardian transcript. It only updates an already captured
// history; the first review seeds from the current record and rollout.
func (r *RuntimeRouter) recordGuardianReviewHistoryAppend(threadID string, items []session.Item) {
	if r == nil || strings.TrimSpace(threadID) == "" || len(items) == 0 {
		return
	}
	r.guardianReviewHistoryMu.Lock()
	defer r.guardianReviewHistoryMu.Unlock()
	if state := r.guardianReviewHistory[threadID]; state != nil {
		state.append(items)
	}
}

func (r *RuntimeRouter) truncateGuardianReviewHistoryBefore(threadID, turnID string) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	r.guardianReviewHistoryMu.Lock()
	defer r.guardianReviewHistoryMu.Unlock()
	if state := r.guardianReviewHistory[threadID]; state != nil {
		state.truncateBeforeTurn(turnID)
	}
}

func (r *RuntimeRouter) forgetGuardianReviewHistory(threadID string) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	r.guardianReviewHistoryMu.Lock()
	delete(r.guardianReviewHistory, threadID)
	r.guardianReviewHistoryMu.Unlock()
}

// guardianReviewHistoryItems returns the original conversation evidence used
// by Guardian. The rollout is consulted when a process has not captured the
// live transcript yet; current record items fill any gaps that paginated
// rollout turn items do not preserve.
func (r *RuntimeRouter) guardianReviewHistoryItems(threadID string, record *session.Record) []session.Item {
	if record == nil {
		return nil
	}
	if r != nil {
		r.guardianReviewHistoryMu.Lock()
		if state := r.guardianReviewHistory[threadID]; state != nil {
			items := append([]session.Item(nil), state.items...)
			r.guardianReviewHistoryMu.Unlock()
			return items
		}
		r.guardianReviewHistoryMu.Unlock()
	}
	seed := append([]session.Item(nil), record.Items...)
	if r != nil && r.services.ThreadRouter != nil {
		path := r.services.ThreadRouter.threadRolloutPath(record)
		if strings.TrimSpace(path) == "" {
			return seed
		}
		lines, _, err := rollout.Load(path)
		if err == nil {
			reviewItems := rollout.ReviewHistoryItems(lines, record.CreatedAt)
			if len(reviewItems) > 0 {
				seed = reviewItems
			}
		}
	}
	return seed
}
