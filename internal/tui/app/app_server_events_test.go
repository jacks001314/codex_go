package app

import "testing"

func TestHandleAppServerLaggedAndDisconnectedDecisionMatchRust(t *testing.T) {
	lagged := HandleAppServerLaggedDecision()
	if lagged.Kind != AppServerEventLagged || !lagged.RefreshMCPStartupExpectedServers || !lagged.FinishMCPStartupAfterLag {
		t.Fatalf("lagged decision = %#v", lagged)
	}

	disconnected := HandleAppServerDisconnectedDecision("stream closed")
	if disconnected.Kind != AppServerEventDisconnected || disconnected.ErrorMessage != "stream closed" || disconnected.FatalExitMessage != "stream closed" {
		t.Fatalf("disconnected decision = %#v", disconnected)
	}
}

func TestHandleServerNotificationEventDecisionSpecialCasesMatchRust(t *testing.T) {
	pending := NewPendingAppServerRequests()
	request := ServerRequest{ID: "req-1", Kind: ServerRequestUserInput, ThreadID: "thread-1", TurnID: "turn-1", ItemID: "call-1"}
	pending.NoteServerRequest(request)

	resolved := HandleServerNotificationEventDecision("thread-1", pending, ServerEvent{
		Name:      ServerNotificationServerRequestResolved,
		ThreadID:  "thread-1",
		RequestID: "req-1",
	})
	if resolved.DismissResolvedRequest == nil || resolved.DismissResolvedRequest.Kind != ResolvedAppServerRequestUserInput || resolved.NotificationRoute != ServerEventRoutePrimary {
		t.Fatalf("resolved decision = %#v", resolved)
	}
	if pending.PendingCount() != 0 {
		t.Fatalf("pending count after resolved = %d, want 0", pending.PendingCount())
	}

	mcp := HandleServerNotificationEventDecision("thread-1", pending, ServerEvent{Name: ServerNotificationMcpServerStatusUpdated})
	if !mcp.RefreshMCPStartupExpectedServers || mcp.NotificationRoute != ServerEventRouteAppScoped || mcp.IgnoredReason != "app_scoped_without_tui_target" {
		t.Fatalf("mcp decision = %#v", mcp)
	}

	rateLimits := HandleServerNotificationEventDecision("thread-1", pending, ServerEvent{Name: ServerNotificationAccountRateLimitsUpdated})
	if !rateLimits.UpdateRateLimits || rateLimits.NotificationRoute != "" {
		t.Fatalf("rate limits decision = %#v", rateLimits)
	}

	account := HandleServerNotificationEventDecision("thread-1", pending, ServerEvent{Name: ServerNotificationAccountUpdated})
	if !account.UpdateAccountState || account.HandleGlobalNotification {
		t.Fatalf("account decision = %#v", account)
	}

	importDone := HandleServerNotificationEventDecision("thread-1", pending, ServerEvent{Name: ServerNotificationExternalAgentConfigImportCompleted})
	if !importDone.ReloadExternalAgentConfig {
		t.Fatalf("external import decision = %#v", importDone)
	}

	appList := HandleServerNotificationEventDecision("thread-1", pending, ServerEvent{Name: ServerNotificationAppListUpdated})
	if !appList.LoadConnectorsSnapshot {
		t.Fatalf("app list decision = %#v", appList)
	}
}

func TestHandleServerNotificationEventDecisionRoutesMatchRust(t *testing.T) {
	primary := HandleServerNotificationEventDecision("thread-1", nil, ServerEvent{Name: ServerNotificationTurnCompleted, ThreadID: "thread-1"})
	if primary.NotificationRoute != ServerEventRoutePrimary || primary.ThreadID != "thread-1" {
		t.Fatalf("primary notification decision = %#v", primary)
	}

	background := HandleServerNotificationEventDecision("thread-1", nil, ServerEvent{Name: ServerNotificationTurnCompleted, ThreadID: "thread-2"})
	if background.NotificationRoute != ServerEventRouteThread || background.ThreadID != "thread-2" {
		t.Fatalf("background notification decision = %#v", background)
	}

	global := HandleServerNotificationEventDecision("thread-1", nil, ServerEvent{Name: ServerNotificationWarning})
	if global.NotificationRoute != ServerEventRouteGlobal || !global.HandleGlobalNotification {
		t.Fatalf("global notification decision = %#v", global)
	}

	invalid := HandleServerNotificationEventDecision("thread-1", nil, ServerEvent{Name: ServerNotificationThreadClosed, ThreadID: "bad id"})
	if invalid.NotificationRoute != ServerEventRouteIgnored || invalid.IgnoredReason != "invalid_thread_id" {
		t.Fatalf("invalid notification decision = %#v", invalid)
	}
}

func TestHandleServerRequestEventDecisionMatchRust(t *testing.T) {
	pending := NewPendingAppServerRequests()
	primary := HandleServerRequestEventDecision("thread-1", pending, ServerRequest{
		ID:       "req-1",
		Kind:     ServerRequestUserInput,
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "call-1",
	})
	if primary.RequestRoute != ServerEventRoutePrimary || primary.ThreadID != "thread-1" || pending.PendingCount() != 1 {
		t.Fatalf("primary request decision = %#v pending=%d", primary, pending.PendingCount())
	}

	background := HandleServerRequestEventDecision("thread-1", pending, ServerRequest{
		ID:       "req-2",
		Kind:     ServerRequestFileChangeApproval,
		ThreadID: "thread-2",
		TurnID:   "turn-2",
		ItemID:   "patch-1",
	})
	if background.RequestRoute != ServerEventRouteThread || background.ThreadID != "thread-2" {
		t.Fatalf("background request decision = %#v", background)
	}

	threadless := HandleServerRequestEventDecision("thread-1", pending, ServerRequest{ID: "legacy", Kind: ServerRequestApplyPatchApproval})
	if threadless.RejectUnsupported == nil || threadless.ErrorMessage != "Legacy patch approval requests are not available in TUI yet." {
		t.Fatalf("legacy request decision = %#v", threadless)
	}

	ignored := HandleServerRequestEventDecision("thread-1", pending, ServerRequest{ID: "unknown", Kind: "unknown"})
	if ignored.RequestRoute != ServerEventRouteThreadless || ignored.IgnoredReason != "threadless_request" {
		t.Fatalf("ignored request decision = %#v", ignored)
	}
}
