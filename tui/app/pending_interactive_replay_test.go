package app

import "testing"

func requestUserInputRequest(callID string, turnID string) ServerRequest {
	return ServerRequest{ID: "rui-" + callID, Kind: ServerRequestUserInput, TurnID: turnID, ItemID: callID}
}

func execApprovalRequest(callID string, approvalID string, turnID string) ServerRequest {
	return ServerRequest{ID: "exec-" + callID, Kind: ServerRequestCommandExecutionApproval, TurnID: turnID, ItemID: callID, ApprovalID: approvalID}
}

func patchApprovalRequest(callID string, turnID string) ServerRequest {
	return ServerRequest{ID: "patch-" + callID, Kind: ServerRequestFileChangeApproval, TurnID: turnID, ItemID: callID}
}

func elicitationRequest(serverName string, requestID string) ServerRequest {
	return ServerRequest{ID: requestID, Kind: ServerRequestMcpElicitation, ServerName: serverName}
}

func permissionsRequest(callID string, turnID string) ServerRequest {
	return ServerRequest{ID: "perm-" + callID, Kind: ServerRequestPermissionsApproval, TurnID: turnID, ItemID: callID}
}

func TestPendingInteractiveReplayKeepsAndDropsRequestUserInputFIFO(t *testing.T) {
	state := NewPendingInteractiveReplayState()
	first := requestUserInputRequest("call-1", "turn-1")
	second := requestUserInputRequest("call-2", "turn-1")
	state.NoteServerRequest(first)
	state.NoteServerRequest(second)

	if !state.ShouldReplaySnapshotRequest(first) || !state.ShouldReplaySnapshotRequest(second) {
		t.Fatalf("both request_user_input prompts should replay while pending")
	}
	if !state.HasPendingThreadUserInput() {
		t.Fatalf("expected pending thread user input")
	}
	if state.HasPendingThreadApprovals() {
		t.Fatalf("request_user_input should not count as pending approval")
	}

	state.NoteOutboundOp(ReplayCommand{Kind: ReplayCommandUserInputAnswer, ID: "turn-1"})
	if state.ShouldReplaySnapshotRequest(first) {
		t.Fatalf("oldest request_user_input prompt should be removed first")
	}
	if !state.ShouldReplaySnapshotRequest(second) {
		t.Fatalf("newer request_user_input prompt should remain pending")
	}

	state.NoteOutboundOp(ReplayCommand{Kind: ReplayCommandUserInputAnswer, ID: "turn-1"})
	if state.HasPendingThreadUserInput() {
		t.Fatalf("all request_user_input prompts should be resolved")
	}
}

func TestPendingInteractiveReplayDropsResolvedExecApprovalByApprovalID(t *testing.T) {
	state := NewPendingInteractiveReplayState()
	request := execApprovalRequest("item-1", "approval-1", "turn-1")
	state.NoteServerRequest(request)
	if !state.HasPendingThreadApprovals() {
		t.Fatalf("exec approval should count as pending approval")
	}
	if !state.ShouldReplaySnapshotRequest(request) {
		t.Fatalf("exec approval should replay before resolution")
	}

	state.NoteOutboundOp(ReplayCommand{Kind: ReplayCommandExecApproval, ID: "approval-1", TurnID: "turn-1"})
	if state.ShouldReplaySnapshotRequest(request) {
		t.Fatalf("resolved exec approval should not replay")
	}
	if state.HasPendingThreadApprovals() {
		t.Fatalf("resolved exec approval should clear approval state")
	}
}

func TestPendingInteractiveReplayDropsResolvedRequestsByServerResolution(t *testing.T) {
	state := NewPendingInteractiveReplayState()
	request := requestUserInputRequest("call-1", "turn-1")
	state.NoteServerRequest(request)
	state.NoteServerNotification(ServerEvent{Name: ServerNotificationServerRequestResolved, RequestID: request.ID})
	if state.ShouldReplaySnapshotRequest(request) {
		t.Fatalf("server-resolved request should not replay")
	}
}

func TestPendingInteractiveReplayTurnCompletedClearsTurnScopedPrompts(t *testing.T) {
	state := NewPendingInteractiveReplayState()
	execReq := execApprovalRequest("exec-1", "approval-1", "turn-1")
	patchReq := patchApprovalRequest("patch-1", "turn-1")
	permReq := permissionsRequest("perm-1", "turn-1")
	inputReq := requestUserInputRequest("input-1", "turn-1")
	state.NoteServerRequest(execReq)
	state.NoteServerRequest(patchReq)
	state.NoteServerRequest(permReq)
	state.NoteServerRequest(inputReq)

	state.NoteServerNotification(ServerEvent{Name: ServerNotificationTurnCompleted, TurnID: "turn-1"})
	for _, request := range []ServerRequest{execReq, patchReq, permReq, inputReq} {
		if state.ShouldReplaySnapshotRequest(request) {
			t.Fatalf("%s should not replay after turn completion", request.Kind)
		}
	}
	if state.HasPendingThreadApprovals() || state.HasPendingThreadUserInput() {
		t.Fatalf("turn completion should clear pending approval/user-input state")
	}
}

func TestPendingInteractiveReplayElicitationResolutionAndEviction(t *testing.T) {
	state := NewPendingInteractiveReplayState()
	request := elicitationRequest("server-1", "request-1")
	state.NoteServerRequest(request)
	if !state.ShouldReplaySnapshotRequest(request) || !state.HasPendingThreadApprovals() {
		t.Fatalf("elicitation should replay and count as pending approval-like prompt")
	}

	state.NoteOutboundOp(ReplayCommand{Kind: ReplayCommandResolveElicitation, ServerName: "server-1", RequestID: "request-1"})
	if state.ShouldReplaySnapshotRequest(request) {
		t.Fatalf("resolved elicitation should not replay")
	}

	state.NoteServerRequest(request)
	state.NoteEvictedServerRequest(request)
	if state.ShouldReplaySnapshotRequest(request) {
		t.Fatalf("evicted elicitation should not replay")
	}
}

func TestPendingInteractiveReplayItemStartedAndThreadClosed(t *testing.T) {
	state := NewPendingInteractiveReplayState()
	execReq := execApprovalRequest("exec-1", "", "turn-1")
	patchReq := patchApprovalRequest("patch-1", "turn-1")
	state.NoteServerRequest(execReq)
	state.NoteServerRequest(patchReq)

	state.NoteServerNotification(ServerEvent{Name: ServerNotificationItemStarted, ItemKind: ThreadItemCommandExecution, ItemID: "exec-1"})
	if state.ShouldReplaySnapshotRequest(execReq) {
		t.Fatalf("started command execution should clear exec approval")
	}
	if !state.ShouldReplaySnapshotRequest(patchReq) {
		t.Fatalf("patch approval should remain pending")
	}

	state.NoteServerNotification(ServerEvent{Name: ServerNotificationThreadClosed})
	if state.ShouldReplaySnapshotRequest(patchReq) {
		t.Fatalf("thread close should clear all pending requests")
	}
}

func TestReplayFiltersMatchRustHelpers(t *testing.T) {
	snapshot := ThreadEventSnapshot{Events: []ThreadBufferedEvent{
		{Type: ThreadBufferedEventNotification, Notification: &ServerEvent{Name: ServerNotificationWarning}},
		{Type: ThreadBufferedEventRequest, Request: &ServerRequest{Kind: ServerRequestUserInput}},
	}}
	if !SnapshotHasPendingInteractiveRequest(snapshot) {
		t.Fatalf("snapshot should report pending interactive request")
	}
	if !EventIsNotice(snapshot.Events[0]) {
		t.Fatalf("warning notification should be notice")
	}
	if EventIsNotice(snapshot.Events[1]) {
		t.Fatalf("request should not be notice")
	}
	if ShouldReplay("ephemeral") {
		t.Fatalf("ephemeral events should not replay")
	}
	if !ShouldReplay("normal") {
		t.Fatalf("normal events should replay")
	}
}

func TestReplayCommandCanChangeState(t *testing.T) {
	if !ReplayCommandCanChangeState(ReplayCommand{Kind: ReplayCommandShutdown}) {
		t.Fatalf("shutdown should be state-changing")
	}
	if ReplayCommandCanChangeState(ReplayCommand{Kind: "other"}) {
		t.Fatalf("unknown op should not be state-changing")
	}
}
