package app

type PendingInteractiveReplay struct {
	ThreadID string
	Items    int
}

const (
	ServerRequestCommandExecutionApproval = "command_execution_request_approval"
	ServerRequestFileChangeApproval       = "file_change_request_approval"
	ServerRequestMcpElicitation           = "mcp_server_elicitation_request"
	ServerRequestPermissionsApproval      = "permissions_request_approval"
	ServerRequestUserInput                = "tool_request_user_input"

	ServerNotificationItemStarted           = "item_started"
	ServerNotificationItemCompleted         = "item_completed"
	ServerNotificationTurnCompleted         = "turn_completed"
	ServerNotificationServerRequestResolved = "server_request_resolved"
	ServerNotificationThreadClosed          = "thread_closed"

	ThreadItemCommandExecution = "command_execution"
	ThreadItemFileChange       = "file_change"
)

type ReplayCommandKind string

const (
	ReplayCommandExecApproval               ReplayCommandKind = "exec_approval"
	ReplayCommandPatchApproval              ReplayCommandKind = "patch_approval"
	ReplayCommandResolveElicitation         ReplayCommandKind = "resolve_elicitation"
	ReplayCommandRequestPermissionsResponse ReplayCommandKind = "request_permissions_response"
	ReplayCommandUserInputAnswer            ReplayCommandKind = "user_input_answer"
	ReplayCommandShutdown                   ReplayCommandKind = "shutdown"
)

type ReplayCommand struct {
	Kind       ReplayCommandKind
	ID         string
	TurnID     string
	ServerName string
	RequestID  string
}

type ElicitationRequestKey struct {
	ServerName string
	RequestID  string
}

type pendingInteractiveRequestKind string

const (
	pendingExecApproval       pendingInteractiveRequestKind = "exec_approval"
	pendingPatchApproval      pendingInteractiveRequestKind = "patch_approval"
	pendingElicitation        pendingInteractiveRequestKind = "elicitation"
	pendingRequestPermissions pendingInteractiveRequestKind = "request_permissions"
	pendingRequestUserInput   pendingInteractiveRequestKind = "request_user_input"
)

type pendingInteractiveRequest struct {
	Kind       pendingInteractiveRequestKind
	TurnID     string
	ItemID     string
	ApprovalID string
	Key        ElicitationRequestKey
}

type PendingInteractiveReplayState struct {
	ExecApprovalCallIDs         map[string]struct{}
	ExecApprovalCallIDsByTurnID map[string][]string

	PatchApprovalCallIDs         map[string]struct{}
	PatchApprovalCallIDsByTurnID map[string][]string

	ElicitationRequests map[ElicitationRequestKey]struct{}

	RequestPermissionsCallIDs         map[string]struct{}
	RequestPermissionsCallIDsByTurnID map[string][]string

	RequestUserInputCallIDs         map[string]struct{}
	RequestUserInputCallIDsByTurnID map[string][]string

	pendingRequestsByRequestID map[string]pendingInteractiveRequest
}

func NewPendingInteractiveReplayState() *PendingInteractiveReplayState {
	state := &PendingInteractiveReplayState{}
	state.ensure()
	return state
}

func ReplayCommandCanChangeState(op ReplayCommand) bool {
	switch op.Kind {
	case ReplayCommandExecApproval,
		ReplayCommandPatchApproval,
		ReplayCommandResolveElicitation,
		ReplayCommandRequestPermissionsResponse,
		ReplayCommandUserInputAnswer,
		ReplayCommandShutdown:
		return true
	default:
		return false
	}
}

func (s *PendingInteractiveReplayState) NoteOutboundOp(op ReplayCommand) {
	if s == nil {
		return
	}
	s.ensure()
	switch op.Kind {
	case ReplayCommandExecApproval:
		delete(s.ExecApprovalCallIDs, op.ID)
		if op.TurnID != "" {
			removeCallIDFromTurnMapEntry(s.ExecApprovalCallIDsByTurnID, op.TurnID, op.ID)
		}
		s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
			return !(p.Kind == pendingExecApproval && p.ApprovalID == op.ID)
		})
	case ReplayCommandPatchApproval:
		delete(s.PatchApprovalCallIDs, op.ID)
		removeCallIDFromTurnMap(s.PatchApprovalCallIDsByTurnID, op.ID)
		s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
			return !(p.Kind == pendingPatchApproval && p.ItemID == op.ID)
		})
	case ReplayCommandResolveElicitation:
		key := ElicitationRequestKey{ServerName: op.ServerName, RequestID: op.RequestID}
		delete(s.ElicitationRequests, key)
		s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
			return !(p.Kind == pendingElicitation && p.Key == key)
		})
	case ReplayCommandRequestPermissionsResponse:
		delete(s.RequestPermissionsCallIDs, op.ID)
		removeCallIDFromTurnMap(s.RequestPermissionsCallIDsByTurnID, op.ID)
		s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
			return !(p.Kind == pendingRequestPermissions && p.ItemID == op.ID)
		})
	case ReplayCommandUserInputAnswer:
		s.removeOldestRequestUserInputForTurn(op.ID)
	case ReplayCommandShutdown:
		s.Clear()
	}
}

func (s *PendingInteractiveReplayState) NoteServerRequest(request ServerRequest) {
	if s == nil {
		return
	}
	s.ensure()
	switch request.Kind {
	case ServerRequestCommandExecutionApproval:
		approvalID := firstNonEmptyApp(request.ApprovalID, request.ItemID)
		s.ExecApprovalCallIDs[approvalID] = struct{}{}
		s.ExecApprovalCallIDsByTurnID[request.TurnID] = append(s.ExecApprovalCallIDsByTurnID[request.TurnID], approvalID)
		s.pendingRequestsByRequestID[request.ID] = pendingInteractiveRequest{
			Kind:       pendingExecApproval,
			TurnID:     request.TurnID,
			ApprovalID: approvalID,
		}
	case ServerRequestFileChangeApproval:
		s.PatchApprovalCallIDs[request.ItemID] = struct{}{}
		s.PatchApprovalCallIDsByTurnID[request.TurnID] = append(s.PatchApprovalCallIDsByTurnID[request.TurnID], request.ItemID)
		s.pendingRequestsByRequestID[request.ID] = pendingInteractiveRequest{
			Kind:   pendingPatchApproval,
			TurnID: request.TurnID,
			ItemID: request.ItemID,
		}
	case ServerRequestMcpElicitation:
		key := ElicitationRequestKey{ServerName: request.ServerName, RequestID: request.ID}
		s.ElicitationRequests[key] = struct{}{}
		s.pendingRequestsByRequestID[request.ID] = pendingInteractiveRequest{Kind: pendingElicitation, Key: key}
	case ServerRequestPermissionsApproval:
		s.RequestPermissionsCallIDs[request.ItemID] = struct{}{}
		s.RequestPermissionsCallIDsByTurnID[request.TurnID] = append(s.RequestPermissionsCallIDsByTurnID[request.TurnID], request.ItemID)
		s.pendingRequestsByRequestID[request.ID] = pendingInteractiveRequest{
			Kind:   pendingRequestPermissions,
			TurnID: request.TurnID,
			ItemID: request.ItemID,
		}
	case ServerRequestUserInput:
		s.RequestUserInputCallIDs[request.ItemID] = struct{}{}
		s.RequestUserInputCallIDsByTurnID[request.TurnID] = append(s.RequestUserInputCallIDsByTurnID[request.TurnID], request.ItemID)
		s.pendingRequestsByRequestID[request.ID] = pendingInteractiveRequest{
			Kind:   pendingRequestUserInput,
			TurnID: request.TurnID,
			ItemID: request.ItemID,
		}
	}
}

func (s *PendingInteractiveReplayState) NoteServerNotification(notification ServerEvent) {
	if s == nil {
		return
	}
	s.ensure()
	switch notification.Name {
	case ServerNotificationItemStarted:
		switch notification.ItemKind {
		case ThreadItemCommandExecution:
			delete(s.ExecApprovalCallIDs, notification.ItemID)
			removeCallIDFromTurnMap(s.ExecApprovalCallIDsByTurnID, notification.ItemID)
		case ThreadItemFileChange:
			delete(s.PatchApprovalCallIDs, notification.ItemID)
			removeCallIDFromTurnMap(s.PatchApprovalCallIDsByTurnID, notification.ItemID)
		}
	case ServerNotificationTurnCompleted:
		s.clearExecApprovalTurn(notification.TurnID)
		s.clearPatchApprovalTurn(notification.TurnID)
		s.clearRequestPermissionsTurn(notification.TurnID)
		s.clearRequestUserInputTurn(notification.TurnID)
	case ServerNotificationServerRequestResolved:
		s.RemoveRequest(notification.RequestID)
	case ServerNotificationThreadClosed:
		s.Clear()
	}
}

func (s *PendingInteractiveReplayState) NoteEvictedServerRequest(request ServerRequest) {
	if s == nil {
		return
	}
	s.ensure()
	switch request.Kind {
	case ServerRequestCommandExecutionApproval:
		approvalID := firstNonEmptyApp(request.ApprovalID, request.ItemID)
		delete(s.ExecApprovalCallIDs, approvalID)
		removeCallIDFromTurnMapEntry(s.ExecApprovalCallIDsByTurnID, request.TurnID, approvalID)
	case ServerRequestFileChangeApproval:
		delete(s.PatchApprovalCallIDs, request.ItemID)
		removeCallIDFromTurnMapEntry(s.PatchApprovalCallIDsByTurnID, request.TurnID, request.ItemID)
	case ServerRequestMcpElicitation:
		delete(s.ElicitationRequests, ElicitationRequestKey{ServerName: request.ServerName, RequestID: request.ID})
	case ServerRequestPermissionsApproval:
		delete(s.RequestPermissionsCallIDs, request.ItemID)
		removeCallIDFromTurnMapEntry(s.RequestPermissionsCallIDsByTurnID, request.TurnID, request.ItemID)
	case ServerRequestUserInput:
		delete(s.RequestUserInputCallIDs, request.ItemID)
		removeCallIDFromTurnMapEntry(s.RequestUserInputCallIDsByTurnID, request.TurnID, request.ItemID)
	}
	s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
		return !pendingRequestMatchesServerRequest(p, request)
	})
}

func (s *PendingInteractiveReplayState) ShouldReplaySnapshotRequest(request ServerRequest) bool {
	if s == nil {
		return true
	}
	s.ensure()
	switch request.Kind {
	case ServerRequestCommandExecutionApproval:
		_, ok := s.ExecApprovalCallIDs[firstNonEmptyApp(request.ApprovalID, request.ItemID)]
		return ok
	case ServerRequestFileChangeApproval:
		_, ok := s.PatchApprovalCallIDs[request.ItemID]
		return ok
	case ServerRequestMcpElicitation:
		_, ok := s.ElicitationRequests[ElicitationRequestKey{ServerName: request.ServerName, RequestID: request.ID}]
		return ok
	case ServerRequestPermissionsApproval:
		_, ok := s.RequestPermissionsCallIDs[request.ItemID]
		return ok
	case ServerRequestUserInput:
		_, ok := s.RequestUserInputCallIDs[request.ItemID]
		return ok
	default:
		return true
	}
}

func (s *PendingInteractiveReplayState) HasPendingThreadApprovals() bool {
	if s == nil {
		return false
	}
	s.ensure()
	return len(s.ExecApprovalCallIDs) > 0 ||
		len(s.PatchApprovalCallIDs) > 0 ||
		len(s.ElicitationRequests) > 0 ||
		len(s.RequestPermissionsCallIDs) > 0
}

func (s *PendingInteractiveReplayState) HasPendingThreadUserInput() bool {
	if s == nil {
		return false
	}
	s.ensure()
	return len(s.RequestUserInputCallIDs) > 0
}

func (s *PendingInteractiveReplayState) RemoveRequest(requestID string) {
	if s == nil {
		return
	}
	s.ensure()
	pending, ok := s.pendingRequestsByRequestID[requestID]
	if !ok {
		return
	}
	delete(s.pendingRequestsByRequestID, requestID)
	switch pending.Kind {
	case pendingExecApproval:
		delete(s.ExecApprovalCallIDs, pending.ApprovalID)
		removeCallIDFromTurnMapEntry(s.ExecApprovalCallIDsByTurnID, pending.TurnID, pending.ApprovalID)
	case pendingPatchApproval:
		delete(s.PatchApprovalCallIDs, pending.ItemID)
		removeCallIDFromTurnMapEntry(s.PatchApprovalCallIDsByTurnID, pending.TurnID, pending.ItemID)
	case pendingElicitation:
		delete(s.ElicitationRequests, pending.Key)
	case pendingRequestPermissions:
		delete(s.RequestPermissionsCallIDs, pending.ItemID)
		removeCallIDFromTurnMapEntry(s.RequestPermissionsCallIDsByTurnID, pending.TurnID, pending.ItemID)
	case pendingRequestUserInput:
		delete(s.RequestUserInputCallIDs, pending.ItemID)
		removeCallIDFromTurnMapEntry(s.RequestUserInputCallIDsByTurnID, pending.TurnID, pending.ItemID)
	}
}

func (s *PendingInteractiveReplayState) Clear() {
	if s == nil {
		return
	}
	s.ensure()
	clear(s.ExecApprovalCallIDs)
	clear(s.ExecApprovalCallIDsByTurnID)
	clear(s.PatchApprovalCallIDs)
	clear(s.PatchApprovalCallIDsByTurnID)
	clear(s.ElicitationRequests)
	clear(s.RequestPermissionsCallIDs)
	clear(s.RequestPermissionsCallIDsByTurnID)
	clear(s.RequestUserInputCallIDs)
	clear(s.RequestUserInputCallIDsByTurnID)
	clear(s.pendingRequestsByRequestID)
}

func (s *PendingInteractiveReplayState) ensure() {
	if s.ExecApprovalCallIDs == nil {
		s.ExecApprovalCallIDs = map[string]struct{}{}
	}
	if s.ExecApprovalCallIDsByTurnID == nil {
		s.ExecApprovalCallIDsByTurnID = map[string][]string{}
	}
	if s.PatchApprovalCallIDs == nil {
		s.PatchApprovalCallIDs = map[string]struct{}{}
	}
	if s.PatchApprovalCallIDsByTurnID == nil {
		s.PatchApprovalCallIDsByTurnID = map[string][]string{}
	}
	if s.ElicitationRequests == nil {
		s.ElicitationRequests = map[ElicitationRequestKey]struct{}{}
	}
	if s.RequestPermissionsCallIDs == nil {
		s.RequestPermissionsCallIDs = map[string]struct{}{}
	}
	if s.RequestPermissionsCallIDsByTurnID == nil {
		s.RequestPermissionsCallIDsByTurnID = map[string][]string{}
	}
	if s.RequestUserInputCallIDs == nil {
		s.RequestUserInputCallIDs = map[string]struct{}{}
	}
	if s.RequestUserInputCallIDsByTurnID == nil {
		s.RequestUserInputCallIDsByTurnID = map[string][]string{}
	}
	if s.pendingRequestsByRequestID == nil {
		s.pendingRequestsByRequestID = map[string]pendingInteractiveRequest{}
	}
}

func (s *PendingInteractiveReplayState) removeOldestRequestUserInputForTurn(turnID string) {
	callIDs := s.RequestUserInputCallIDsByTurnID[turnID]
	if len(callIDs) == 0 {
		return
	}
	callID := callIDs[0]
	delete(s.RequestUserInputCallIDs, callID)
	if len(callIDs) == 1 {
		delete(s.RequestUserInputCallIDsByTurnID, turnID)
	} else {
		s.RequestUserInputCallIDsByTurnID[turnID] = append([]string(nil), callIDs[1:]...)
	}
	s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
		return !(p.Kind == pendingRequestUserInput && p.ItemID == callID)
	})
}

func (s *PendingInteractiveReplayState) clearRequestUserInputTurn(turnID string) {
	for _, callID := range s.RequestUserInputCallIDsByTurnID[turnID] {
		delete(s.RequestUserInputCallIDs, callID)
	}
	delete(s.RequestUserInputCallIDsByTurnID, turnID)
	s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
		return !(p.Kind == pendingRequestUserInput && p.TurnID == turnID)
	})
}

func (s *PendingInteractiveReplayState) clearRequestPermissionsTurn(turnID string) {
	for _, callID := range s.RequestPermissionsCallIDsByTurnID[turnID] {
		delete(s.RequestPermissionsCallIDs, callID)
	}
	delete(s.RequestPermissionsCallIDsByTurnID, turnID)
	s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
		return !(p.Kind == pendingRequestPermissions && p.TurnID == turnID)
	})
}

func (s *PendingInteractiveReplayState) clearExecApprovalTurn(turnID string) {
	for _, callID := range s.ExecApprovalCallIDsByTurnID[turnID] {
		delete(s.ExecApprovalCallIDs, callID)
	}
	delete(s.ExecApprovalCallIDsByTurnID, turnID)
	s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
		return !(p.Kind == pendingExecApproval && p.TurnID == turnID)
	})
}

func (s *PendingInteractiveReplayState) clearPatchApprovalTurn(turnID string) {
	for _, callID := range s.PatchApprovalCallIDsByTurnID[turnID] {
		delete(s.PatchApprovalCallIDs, callID)
	}
	delete(s.PatchApprovalCallIDsByTurnID, turnID)
	s.retainPendingRequests(func(p pendingInteractiveRequest) bool {
		return !(p.Kind == pendingPatchApproval && p.TurnID == turnID)
	})
}

func (s *PendingInteractiveReplayState) retainPendingRequests(keep func(pendingInteractiveRequest) bool) {
	for requestID, pending := range s.pendingRequestsByRequestID {
		if !keep(pending) {
			delete(s.pendingRequestsByRequestID, requestID)
		}
	}
}

func removeCallIDFromTurnMap(callIDsByTurnID map[string][]string, callID string) {
	for turnID, callIDs := range callIDsByTurnID {
		filtered := filterOutString(callIDs, callID)
		if len(filtered) == 0 {
			delete(callIDsByTurnID, turnID)
		} else {
			callIDsByTurnID[turnID] = filtered
		}
	}
}

func removeCallIDFromTurnMapEntry(callIDsByTurnID map[string][]string, turnID string, callID string) {
	callIDs, ok := callIDsByTurnID[turnID]
	if !ok {
		return
	}
	filtered := filterOutString(callIDs, callID)
	if len(filtered) == 0 {
		delete(callIDsByTurnID, turnID)
	} else {
		callIDsByTurnID[turnID] = filtered
	}
}

func pendingRequestMatchesServerRequest(pending pendingInteractiveRequest, request ServerRequest) bool {
	switch request.Kind {
	case ServerRequestCommandExecutionApproval:
		return pending.Kind == pendingExecApproval &&
			pending.TurnID == request.TurnID &&
			pending.ApprovalID == firstNonEmptyApp(request.ApprovalID, request.ItemID)
	case ServerRequestFileChangeApproval:
		return pending.Kind == pendingPatchApproval && pending.TurnID == request.TurnID && pending.ItemID == request.ItemID
	case ServerRequestMcpElicitation:
		return pending.Kind == pendingElicitation && pending.Key == (ElicitationRequestKey{ServerName: request.ServerName, RequestID: request.ID})
	case ServerRequestPermissionsApproval:
		return pending.Kind == pendingRequestPermissions && pending.TurnID == request.TurnID && pending.ItemID == request.ItemID
	case ServerRequestUserInput:
		return pending.Kind == pendingRequestUserInput && pending.TurnID == request.TurnID && pending.ItemID == request.ItemID
	default:
		return false
	}
}

func filterOutString(values []string, drop string) []string {
	out := values[:0]
	for _, value := range values {
		if value != drop {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmptyApp(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
