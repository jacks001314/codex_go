package app

import (
	"sort"

	"codex_go/appserver"
)

// Rust parity subset: codex-rs/tui/src/app/thread_routing.rs.

type ThreadRollbackOrigin string

const (
	ThreadRollbackOriginBacktrack            ThreadRollbackOrigin = "backtrack"
	ThreadRollbackOriginSafetyBufferingRetry ThreadRollbackOrigin = "safety_buffering_retry"
)

type ThreadRoute struct {
	From string
	To   string
}

type ThreadEventStore struct {
	Session                  *ThreadSessionState
	Turns                    []appserver.Turn
	InputState               *ThreadInputState
	Active                   bool
	ActiveTurnID             string
	PendingInterruptTurnID   string
	Buffer                   []ThreadBufferedEvent
	Capacity                 int
	PendingInteractiveReplay *PendingInteractiveReplayState
}

func NewThreadEventStore(capacity int) *ThreadEventStore {
	if capacity < 1 {
		capacity = 1
	}
	return &ThreadEventStore{
		Capacity:                 capacity,
		PendingInteractiveReplay: NewPendingInteractiveReplayState(),
	}
}

// TurnsIncludingBuffered reconstructs buffered turns from turn and item
// notifications before prompt-edit lookups, mirroring Rust 266c6920d9: newer
// live turns may exist only in the replay buffer and must be visible to the
// selected-prompt search. Completion metadata is preserved and turns/items
// already present in the snapshot are not duplicated.
func (s *ThreadEventStore) TurnsIncludingBuffered() []appserver.Turn {
	if s == nil {
		return nil
	}
	turns := cloneAppTurns(s.Turns)
	for _, event := range s.Buffer {
		if event.Type != ThreadBufferedEventNotification || event.Notification == nil {
			continue
		}
		notification := event.Notification
		switch notification.Name {
		case ServerNotificationTurnStarted:
			turn := notification.Turn
			if turn == nil || appTurnsContain(turns, turn.ID) {
				continue
			}
			cloned := *turn
			cloned.Items = append([]appserver.ThreadItem(nil), turn.Items...)
			turns = append(turns, cloned)
		case ServerNotificationItemCompleted:
			if notification.Item == nil || !bufferedPromptEditItem(notification.Item) {
				continue
			}
			idx := appTurnsIndex(turns, notification.TurnID)
			if idx < 0 || threadItemsContain(turns[idx].Items, notification.Item.ID) {
				continue
			}
			turns[idx].Items = append(turns[idx].Items, cloneAppThreadItem(notification.Item))
		case ServerNotificationTurnCompleted:
			turn := notification.Turn
			if turn == nil {
				continue
			}
			idx := appTurnsIndex(turns, turn.ID)
			if idx < 0 {
				continue
			}
			turns[idx].Status = turn.Status
			turns[idx].Error = cloneTurnError(turn.Error)
			turns[idx].StartedAt = turn.StartedAt
			turns[idx].CompletedAt = turn.CompletedAt
			turns[idx].DurationMS = turn.DurationMS
		}
	}
	return turns
}

func bufferedPromptEditItem(item *appserver.ThreadItem) bool {
	if item == nil {
		return false
	}
	switch item.Type {
	case "message", "user_message", "userMessage":
		return true
	case "entered_review_mode", "exited_review_mode":
		return true
	default:
		return false
	}
}

func appTurnsContain(turns []appserver.Turn, id string) bool {
	return appTurnsIndex(turns, id) >= 0
}

func appTurnsIndex(turns []appserver.Turn, id string) int {
	for i := range turns {
		if turns[i].ID == id {
			return i
		}
	}
	return -1
}

func threadItemsContain(items []appserver.ThreadItem, id string) bool {
	for i := range items {
		if items[i].ID == id {
			return true
		}
	}
	return false
}

func cloneAppThreadItem(item *appserver.ThreadItem) appserver.ThreadItem {
	if item == nil {
		return appserver.ThreadItem{}
	}
	out := *item
	out.Content = append([]appserver.ThreadItemContent(nil), item.Content...)
	out.Data = cloneThreadItemData(item.Data)
	return out
}

func cloneThreadItemData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}

func cloneTurnError(err *appserver.TurnError) *appserver.TurnError {
	if err == nil {
		return nil
	}
	out := *err
	return &out
}

func NewThreadEventStoreWithSession(capacity int, session ThreadSessionState, turns []appserver.Turn) *ThreadEventStore {
	store := NewThreadEventStore(capacity)
	store.SetSession(session, turns)
	return store
}

func (s *ThreadEventStore) SetSession(session ThreadSessionState, turns []appserver.Turn) {
	if s == nil {
		return
	}
	cloned := session.Clone()
	s.Session = &cloned
	s.SetTurns(turns)
}

func (s *ThreadEventStore) SetTurns(turns []appserver.Turn) {
	if s == nil {
		return
	}
	s.Turns = append([]appserver.Turn(nil), turns...)
	s.ActiveTurnID = ""
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Status == appserver.TurnStatusInProgress {
			s.ActiveTurnID = turns[i].ID
			break
		}
	}
}

func (s *ThreadEventStore) SetInputState(input *ThreadInputState) {
	if s == nil {
		return
	}
	if input == nil {
		s.InputState = nil
		return
	}
	cloned := *input
	s.InputState = &cloned
}

func (s *ThreadEventStore) EnqueueNotification(notification ServerEvent) {
	if s == nil {
		return
	}
	s.ensure()
	s.PendingInteractiveReplay.NoteServerNotification(notification)
	s.updateActiveTurnFromNotification(notification)
	s.appendEvent(ThreadBufferedEvent{
		Type:         ThreadBufferedEventNotification,
		Notification: cloneServerEventForRouting(&notification),
	})
}

func (s *ThreadEventStore) EnqueueRequest(request ServerRequest) {
	if s == nil {
		return
	}
	s.ensure()
	s.PendingInteractiveReplay.NoteServerRequest(request)
	s.appendEvent(ThreadBufferedEvent{
		Type:    ThreadBufferedEventRequest,
		Request: cloneServerRequestForRouting(&request),
	})
}

func (s *ThreadEventStore) Snapshot() ThreadEventSnapshot {
	if s == nil {
		return ThreadEventSnapshot{}
	}
	s.ensure()
	events := make([]ThreadBufferedEvent, 0, len(s.Buffer))
	for _, event := range s.Buffer {
		if event.Type == ThreadBufferedEventRequest && event.Request != nil && !s.PendingInteractiveReplay.ShouldReplaySnapshotRequest(*event.Request) {
			continue
		}
		events = append(events, cloneThreadBufferedEventForRouting(event))
	}
	snapshot := ThreadEventSnapshot{
		Turns:  append([]appserver.Turn(nil), s.Turns...),
		Events: events,
	}
	if s.Session != nil {
		cloned := s.Session.Clone()
		snapshot.Session = &cloned
	}
	if s.InputState != nil {
		cloned := *s.InputState
		snapshot.InputState = &cloned
	}
	return snapshot
}

func (s *ThreadEventStore) PendingReplayRequests() []ServerRequest {
	if s == nil {
		return nil
	}
	s.ensure()
	out := []ServerRequest{}
	for _, event := range s.Buffer {
		if event.Type != ThreadBufferedEventRequest || event.Request == nil {
			continue
		}
		if s.PendingInteractiveReplay.ShouldReplaySnapshotRequest(*event.Request) {
			out = append(out, *event.Request)
		}
	}
	return out
}

func (s *ThreadEventStore) HasPendingThreadApprovals() bool {
	return s != nil && s.PendingInteractiveReplay != nil && s.PendingInteractiveReplay.HasPendingThreadApprovals()
}

func (s *ThreadEventStore) HasPendingThreadUserInput() bool {
	return s != nil && s.PendingInteractiveReplay != nil && s.PendingInteractiveReplay.HasPendingThreadUserInput()
}

func (s *ThreadEventStore) ActiveTurn() string {
	if s == nil {
		return ""
	}
	return s.ActiveTurnID
}

func (s *ThreadEventStore) ClearActiveTurn() {
	if s == nil {
		return
	}
	s.ActiveTurnID = ""
}

func (s *ThreadEventStore) BeginInterrupt(turnID string) bool {
	if s == nil || turnID == "" || s.PendingInterruptTurnID == turnID {
		return false
	}
	s.PendingInterruptTurnID = turnID
	return true
}

func (s *ThreadEventStore) PendingInterrupt() string {
	if s == nil {
		return ""
	}
	return s.PendingInterruptTurnID
}

func (s *ThreadEventStore) RebaseBufferAfterSessionRefresh() {
	if s == nil {
		return
	}
	s.ensure()
	rebased := s.Buffer[:0]
	for _, event := range s.Buffer {
		if ThreadEventSurvivesSessionRefresh(event) {
			rebased = append(rebased, event)
		} else if event.Type == ThreadBufferedEventRequest && event.Request != nil {
			s.PendingInteractiveReplay.NoteEvictedServerRequest(*event.Request)
		}
	}
	s.Buffer = rebased
}

func (s *ThreadEventStore) ApplyThreadRollback(turns []appserver.Turn) {
	if s == nil {
		return
	}
	s.Turns = append([]appserver.Turn(nil), turns...)
	s.Buffer = nil
	s.PendingInteractiveReplay = NewPendingInteractiveReplayState()
	s.ActiveTurnID = ""
}

func (s *ThreadEventStore) SideParentPendingStatus() (SideParentStatus, bool) {
	if s == nil || s.PendingInteractiveReplay == nil {
		return "", false
	}
	if s.PendingInteractiveReplay.HasPendingThreadUserInput() {
		return SideParentStatusNeedsInput, true
	}
	if s.PendingInteractiveReplay.HasPendingThreadApprovals() {
		return SideParentStatusNeedsApproval, true
	}
	return "", false
}

func PendingInactiveThreadRequests(activeThreadID string, stores map[string]*ThreadEventStore) []ServerRequest {
	threadIDs := make([]string, 0, len(stores))
	for threadID := range stores {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)

	var requests []ServerRequest
	for _, threadID := range threadIDs {
		if threadID == activeThreadID {
			continue
		}
		store := stores[threadID]
		if _, ok := store.SideParentPendingStatus(); !ok {
			continue
		}
		requests = append(requests, store.PendingReplayRequests()...)
	}
	return requests
}

func (s *ThreadEventStore) appendEvent(event ThreadBufferedEvent) {
	s.Buffer = append(s.Buffer, cloneThreadBufferedEventForRouting(event))
	for len(s.Buffer) > s.Capacity {
		evicted := s.Buffer[0]
		copy(s.Buffer, s.Buffer[1:])
		s.Buffer = s.Buffer[:len(s.Buffer)-1]
		if evicted.Type == ThreadBufferedEventRequest && evicted.Request != nil {
			s.PendingInteractiveReplay.NoteEvictedServerRequest(*evicted.Request)
		}
	}
}

func (s *ThreadEventStore) updateActiveTurnFromNotification(notification ServerEvent) {
	target := EventTargetFromServerEvent(notification)
	switch notification.Name {
	case ServerNotificationTurnStarted:
		s.ActiveTurnID = target.TurnID
	case ServerNotificationTurnCompleted:
		if s.ActiveTurnID == target.TurnID {
			s.ActiveTurnID = ""
		}
		if s.PendingInterruptTurnID == target.TurnID {
			s.PendingInterruptTurnID = ""
		}
	case ServerNotificationThreadClosed:
		s.ActiveTurnID = ""
		s.PendingInterruptTurnID = ""
	}
}

func (s *ThreadEventStore) ensure() {
	if s.Capacity < 1 {
		s.Capacity = 1
	}
	if s.PendingInteractiveReplay == nil {
		s.PendingInteractiveReplay = NewPendingInteractiveReplayState()
	}
}

func cloneThreadBufferedEventForRouting(event ThreadBufferedEvent) ThreadBufferedEvent {
	out := event
	if event.Request != nil {
		out.Request = cloneServerRequestForRouting(event.Request)
	}
	if event.Notification != nil {
		out.Notification = cloneServerEventForRouting(event.Notification)
	}
	return out
}

func cloneServerRequestForRouting(request *ServerRequest) *ServerRequest {
	if request == nil {
		return nil
	}
	cloned := *request
	return &cloned
}

func cloneServerEventForRouting(event *ServerEvent) *ServerEvent {
	if event == nil {
		return nil
	}
	cloned := *event
	if event.Item != nil {
		item := *event.Item
		if event.Item.Content != nil {
			item.Content = append([]appserver.ThreadItemContent(nil), event.Item.Content...)
		}
		if event.Item.Data != nil {
			item.Data = make(map[string]any, len(event.Item.Data))
			for key, value := range event.Item.Data {
				item.Data[key] = value
			}
		}
		if event.Item.Raw != nil {
			item.Raw = append([]byte(nil), event.Item.Raw...)
		}
		cloned.Item = &item
	}
	if event.Turn != nil {
		turn := *event.Turn
		if event.Turn.Items != nil {
			turn.Items = make([]appserver.ThreadItem, len(event.Turn.Items))
			for i := range event.Turn.Items {
				turn.Items[i] = cloneAppThreadItem(&event.Turn.Items[i])
			}
		}
		if event.Turn.Error != nil {
			clonedError := *event.Turn.Error
			turn.Error = &clonedError
		}
		cloned.Turn = &turn
	}
	return &cloned
}
