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
	return &cloned
}
