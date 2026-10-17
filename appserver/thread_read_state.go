package appserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"codex_go/session"
	"codex_go/state"
)

func threadReadStateEligible(thread *Thread) bool {
	if thread == nil || thread.Ephemeral {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(string(thread.Source)))
	if source == string(SessionSourceUnknown) || strings.HasPrefix(source, "subagent") || strings.HasPrefix(source, "internal_") {
		return false
	}
	return thread.ThreadSource == nil || *thread.ThreadSource == ThreadSourceUser
}

func apiThreadReadState(value state.ThreadReadState) ThreadReadState {
	out := ThreadReadState{Revision: value.Revision}
	if value.FirstUnreadTurn == nil {
		return out
	}
	if *value.FirstUnreadTurn == "" {
		out.FirstUnread = &ThreadUnreadPosition{Type: "threadStart"}
		return out
	}
	out.FirstUnread = &ThreadUnreadPosition{Type: "turn", TurnID: *value.FirstUnreadTurn}
	return out
}

// threadReadStatesForThreads returns receipts only for eligible durable local
// user threads. Unavailable or corrupt state omits the map rather than failing
// the thread listing (Rust request_processors/thread_read_state.rs::snapshots).
func threadReadStatesForThreads(database *state.StateRuntime, threads []*Thread) map[string]ThreadReadState {
	if database == nil || len(threads) == 0 {
		return nil
	}
	ids := make([]string, 0, len(threads))
	for _, thread := range threads {
		if thread != nil && threadReadStateEligible(thread) && strings.TrimSpace(thread.ID) != "" {
			ids = append(ids, thread.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	states, err := database.ThreadReadStates(context.Background(), ids)
	if err != nil {
		slog.Warn("thread read state unavailable", "error", err)
		return nil
	}
	out := make(map[string]ThreadReadState, len(states))
	for id, value := range states {
		out[id] = apiThreadReadState(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func threadReadStateForThread(database *state.StateRuntime, thread *Thread) *ThreadReadState {
	if thread == nil || !threadReadStateEligible(thread) {
		return nil
	}
	states := threadReadStatesForThreads(database, []*Thread{thread})
	if states == nil {
		return nil
	}
	value, ok := states[thread.ID]
	if !ok {
		return nil
	}
	return &value
}

type readStateConflictError struct {
	state ThreadReadState
}

func (e *readStateConflictError) Error() string {
	return "read state changed"
}

func (e *readStateConflictError) Unwrap() error {
	return ErrJSONRPCInvalidRequest
}

func (e *readStateConflictError) Is(target error) bool {
	return target == ErrJSONRPCInvalidRequest || target == ErrInvalidRequest
}

func (e *readStateConflictError) JSONRPCErrorData() map[string]any {
	if e == nil {
		return nil
	}
	return map[string]any{"reason": "readStateConflict", "readState": e.state}
}

func (r *Router) handleThreadReadStateUpdate(request *Request) (*ThreadReadStateUpdateResponse, error) {
	var params ThreadReadStateUpdateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if r.state == nil {
		return nil, jsonRPCInvalidRequest("durable thread read state unavailable")
	}
	record, err := r.readThreadRecord(session.ThreadID(params.ThreadID), true, false)
	if err != nil {
		record, err = r.readThreadRecordFromRollout(session.ThreadID(params.ThreadID), true, false)
		if err != nil {
			return nil, threadStoreMutationError("read state", err)
		}
	}
	thread := BuildThread(record, "", false)
	if !threadReadStateEligible(thread) {
		return nil, jsonRPCInvalidRequest("thread read state unavailable")
	}
	outcome, err := r.state.UpdateThreadReadState(
		context.Background(),
		params.ThreadID,
		params.ExpectedRevision,
		state.ReadStateOperation(params.Operation.Type),
	)
	if err != nil {
		return nil, fmt.Errorf("read state was not saved: %w", err)
	}
	switch outcome.Kind {
	case state.ReadStateUpdateApplied:
		return &ThreadReadStateUpdateResponse{ReadState: apiThreadReadState(outcome.State)}, nil
	case state.ReadStateUpdateConflict:
		return nil, &readStateConflictError{state: apiThreadReadState(outcome.State)}
	case state.ReadStateUpdateUnavailable:
		return nil, jsonRPCInvalidRequest("thread read state unavailable")
	default:
		return nil, fmt.Errorf("unknown read state update result %q", outcome.Kind)
	}
}

func attachThreadListReadStates(database *state.StateRuntime, response *ThreadListResponse) {
	if response == nil {
		return
	}
	threads := make([]*Thread, 0, len(response.Data))
	for i := range response.Data {
		threads = append(threads, &response.Data[i])
	}
	response.ReadStates = threadReadStatesForThreads(database, threads)
}

func attachThreadReadState(database *state.StateRuntime, response *ThreadReadResponse) {
	if response == nil || response.Thread == nil {
		return
	}
	response.ReadState = threadReadStateForThread(database, response.Thread)
}

func (r *RuntimeRouter) notifyThreadReadStateChanged(threadID string, readState ThreadReadState) {
	if r == nil {
		return
	}
	notification := &ThreadReadStateChangedNotification{ThreadID: threadID, ReadState: readState}
	for _, connectionID := range r.subscribedConnectionIDsForThread(threadID) {
		r.notifyToConnection(connectionID, NotificationThreadReadStateChanged, notification)
	}
}

// publishThreadReadAttention records visible terminal results before the
// turn/completed notification. Reads and history replay never call this path.
func (r *RuntimeRouter) publishThreadReadAttention(notification *TurnCompletedNotification) {
	if r == nil || notification == nil || r.services.StateRuntime == nil {
		return
	}
	threadID := strings.TrimSpace(notification.ThreadID)
	turnID := strings.TrimSpace(notification.Turn.ID)
	if threadID == "" || turnID == "" {
		return
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return
	}
	thread := BuildThread(record, "", false)
	if !threadReadStateEligible(thread) {
		return
	}
	finalText := lastAgentMessageFromThreadItems(notification.Turn.Items)
	if finalText == "" {
		finalText = lastAgentMessage(record.Items)
	}
	visible := false
	switch notification.Turn.Status {
	case TurnStatusFailed:
		visible = true
	case TurnStatusInterrupted:
		visible = notification.Turn.Error != nil || finalText != ""
	case TurnStatusCompleted:
		if finalText == "" || strings.Contains(finalText, "<decision>DONT_NOTIFY</decision>") {
			return
		}
		if goal, goalErr := r.services.StateRuntime.GetThreadGoal(context.Background(), threadID); goalErr != nil {
			slog.Warn("could not resolve terminal attention policy", "thread_id", threadID, "error", goalErr)
			return
		} else if goal != nil && goal.Status == state.ThreadGoalActive {
			return
		}
		visible = true
	}
	if !visible {
		return
	}
	changed, err := r.services.StateRuntime.PublishThreadAttention(context.Background(), threadID, turnID)
	if err != nil {
		slog.Warn("failed to publish terminal attention", "thread_id", threadID, "turn_id", turnID, "error", err)
		return
	}
	if !changed {
		return
	}
	states, err := r.services.StateRuntime.ThreadReadStates(context.Background(), []string{threadID})
	if err != nil {
		slog.Warn("could not project committed read state", "thread_id", threadID, "error", err)
		return
	}
	if value, ok := states[threadID]; ok {
		r.notifyThreadReadStateChanged(threadID, apiThreadReadState(value))
	}
}

// threadReadStateRevisionToRetire returns the receipt revision when the first
// unread turn belongs to the suffix removed by thread/revert. An explicit
// thread-start marker (empty turn id) and unread positions older than the
// boundary are retained.
func (r *RuntimeRouter) threadReadStateRevisionToRetire(threadID, beforeTurnID string) string {
	if r == nil || r.services.StateRuntime == nil {
		return ""
	}
	threadID = strings.TrimSpace(threadID)
	beforeTurnID = strings.TrimSpace(beforeTurnID)
	states, err := r.services.StateRuntime.ThreadReadStates(context.Background(), []string{threadID})
	if err != nil {
		slog.Warn("could not inspect read state before revert", "thread_id", threadID, "error", err)
		return ""
	}
	value, ok := states[threadID]
	if !ok || value.FirstUnreadTurn == nil || strings.TrimSpace(*value.FirstUnreadTurn) == "" {
		return ""
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		slog.Warn("could not inspect turns before revert", "thread_id", threadID, "error", err)
		return ""
	}
	turns := BuildThread(record, "", true).Turns
	for i := len(turns) - 1; i >= 0; i-- {
		turnID := strings.TrimSpace(turns[i].ID)
		if turnID == *value.FirstUnreadTurn {
			return value.Revision
		}
		if turnID == beforeTurnID {
			return ""
		}
	}
	return ""
}
