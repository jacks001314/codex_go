package appserver

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

type guardianReviewHistoryState struct {
	items []session.Item
	seen  map[string]struct{}
}

const (
	maxGuardianReviewItemsPerKind = 128
	maxGuardianReviewBytesPerKind = 4 * 1024 * 1024
)

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
		encoded, err := json.Marshal(item)
		if err != nil {
			continue
		}
		userKind := guardianReviewHistoryUserKind(item)
		if len(encoded) > maxGuardianReviewBytesPerKind {
			if !userKind {
				continue
			}
			item.Text = truncateGuardianReviewHistoryText(item.Text, maxGuardianReviewBytesPerKind/2)
		}
		s.seen[key] = struct{}{}
		s.items = append(s.items, cloneRuntimeSessionItem(item))
		s.evictKind(userKind)
	}
}

func (s *guardianReviewHistoryState) evictKind(userKind bool) {
	if s == nil {
		return
	}
	indices := make([]int, 0, len(s.items))
	totalBytes := 0
	for index := range s.items {
		if guardianReviewHistoryUserKind(s.items[index]) != userKind {
			continue
		}
		indices = append(indices, index)
		if encoded, err := json.Marshal(s.items[index]); err == nil {
			totalBytes += len(encoded)
		}
	}
	removeCount := 0
	if len(indices) > maxGuardianReviewItemsPerKind {
		removeCount = (len(indices) + 1) / 2
	}
	for _, index := range indices[:removeCount] {
		if encoded, err := json.Marshal(s.items[index]); err == nil {
			totalBytes -= len(encoded)
		}
	}
	for removeCount < len(indices) && totalBytes > maxGuardianReviewBytesPerKind {
		if encoded, err := json.Marshal(s.items[indices[removeCount]]); err == nil {
			totalBytes -= len(encoded)
		}
		removeCount++
	}
	if removeCount == 0 {
		return
	}
	drop := make(map[int]struct{}, removeCount)
	for _, index := range indices[:removeCount] {
		drop[index] = struct{}{}
	}
	kept := make([]session.Item, 0, len(s.items)-removeCount)
	for index := range s.items {
		if _, ok := drop[index]; !ok {
			kept = append(kept, s.items[index])
		}
	}
	s.reset(kept)
}

func guardianReviewHistoryUserKind(item session.Item) bool {
	return strings.EqualFold(strings.TrimSpace(item.Role), "user")
}

func truncateGuardianReviewHistoryText(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && (text[cut]&0xc0) == 0x80 {
		cut--
	}
	return text[:cut]
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

func (s *guardianReviewHistoryState) retainLastTurns(turnCount int) {
	if s == nil || turnCount <= 0 || len(s.items) == 0 {
		return
	}
	turnIDs := make([]string, 0)
	seen := map[string]struct{}{}
	for index := range s.items {
		turnID := guardianReviewItemTurnID(&s.items[index], index)
		if turnID == "" {
			continue
		}
		if _, ok := seen[turnID]; ok {
			continue
		}
		seen[turnID] = struct{}{}
		turnIDs = append(turnIDs, turnID)
	}
	if len(turnIDs) == 0 || turnCount >= len(turnIDs) {
		return
	}
	keep := make(map[string]struct{}, turnCount)
	for _, turnID := range turnIDs[len(turnIDs)-turnCount:] {
		keep[turnID] = struct{}{}
	}
	retained := make([]session.Item, 0, len(s.items))
	for index := range s.items {
		if _, ok := keep[guardianReviewItemTurnID(&s.items[index], index)]; ok {
			retained = append(retained, s.items[index])
		}
	}
	s.reset(retained)
}

func (s *guardianReviewHistoryState) retainThroughTurn(lastTurnID string) {
	if s == nil || strings.TrimSpace(lastTurnID) == "" || len(s.items) == 0 {
		return
	}
	target := strings.TrimSpace(lastTurnID)
	turnIDs := make([]string, 0)
	seen := map[string]struct{}{}
	for index := range s.items {
		turnID := guardianReviewItemTurnID(&s.items[index], index)
		if turnID == "" {
			continue
		}
		if _, ok := seen[turnID]; ok {
			continue
		}
		seen[turnID] = struct{}{}
		turnIDs = append(turnIDs, turnID)
	}
	keep := map[string]struct{}{}
	for _, turnID := range turnIDs {
		keep[turnID] = struct{}{}
		if turnID == target {
			break
		}
	}
	if len(keep) == 0 {
		return
	}
	retained := make([]session.Item, 0, len(s.items))
	for index := range s.items {
		if _, ok := keep[guardianReviewItemTurnID(&s.items[index], index)]; ok {
			retained = append(retained, s.items[index])
		}
	}
	s.reset(retained)
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

// forkGuardianReviewHistory preserves user-visible fork evidence. Subagent
// spawn paths never call this, so parent-local authorization cannot leak into a
// child thread (Rust #42065 spawn.rs).
func (r *RuntimeRouter) forkGuardianReviewHistory(sourceRecord *session.Record, newThreadID, beforeTurnID, lastTurnID string, lastN int) {
	if r == nil || sourceRecord == nil || strings.TrimSpace(newThreadID) == "" {
		return
	}
	items := r.guardianReviewHistoryItems(string(sourceRecord.ID), sourceRecord)
	state := newGuardianReviewHistoryState(items)
	state.retainThroughTurn(lastTurnID)
	state.truncateBeforeTurn(beforeTurnID)
	state.retainLastTurns(lastN)
	r.guardianReviewHistoryMu.Lock()
	if r.guardianReviewHistory == nil {
		r.guardianReviewHistory = map[string]*guardianReviewHistoryState{}
	}
	r.guardianReviewHistory[newThreadID] = state
	r.guardianReviewHistoryMu.Unlock()
}

// guardianReviewHistoryCheckpointWire serializes the bounded review history in
// Rust's transparent `guardian_history: Option<Vec<ResponseItem>>` wire shape.
func (r *RuntimeRouter) guardianReviewHistoryCheckpointWire(threadID string, now time.Time) json.RawMessage {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	r.guardianReviewHistoryMu.Lock()
	var items []session.Item
	if state := r.guardianReviewHistory[threadID]; state != nil {
		items = append(items, state.items...)
	}
	r.guardianReviewHistoryMu.Unlock()
	if len(items) == 0 {
		return nil
	}
	rawItems := make([]json.RawMessage, 0, len(items))
	for index := range items {
		item := rollout.ItemFromSessionItem(&items[index])
		if item == nil {
			continue
		}
		line, err := rollout.LineFromItem(item, now)
		if err != nil || len(line.Item) == 0 {
			continue
		}
		rawItems = append(rawItems, append(json.RawMessage(nil), line.Item...))
	}
	if len(rawItems) == 0 {
		return nil
	}
	encoded, err := json.Marshal(rawItems)
	if err != nil {
		return nil
	}
	return encoded
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
	checkpointItems := []session.Item(nil)
	if r != nil && r.services.ThreadRouter != nil {
		path := r.services.ThreadRouter.threadRolloutPath(record)
		if strings.TrimSpace(path) == "" {
			return seed
		}
		lines, _, err := rollout.Load(path)
		if err == nil {
			checkpointItems = rollout.CompactedGuardianHistory(lines, record.CreatedAt)
			if len(checkpointItems) == 0 {
				checkpointItems = rollout.ReviewHistoryItems(lines, record.CreatedAt)
			}
			if len(checkpointItems) > 0 {
				seed = checkpointItems
			}
		}
	}
	if r != nil {
		r.guardianReviewHistoryMu.Lock()
		if r.guardianReviewHistory == nil {
			r.guardianReviewHistory = map[string]*guardianReviewHistoryState{}
		}
		state := newGuardianReviewHistoryState(checkpointItems)
		state.append(record.Items)
		r.guardianReviewHistory[threadID] = state
		seed = append([]session.Item(nil), state.items...)
		r.guardianReviewHistoryMu.Unlock()
	}
	return seed
}
