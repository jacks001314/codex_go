package app

import "testing"

func TestServerRequestThreadIDMatchRustTargetedKinds(t *testing.T) {
	for _, kind := range []string{
		ServerRequestCommandExecutionApproval,
		ServerRequestFileChangeApproval,
		ServerRequestUserInput,
		ServerRequestMcpElicitation,
		ServerRequestPermissionsApproval,
		"dynamic_tool_call",
		"current_time_read",
	} {
		got, ok := ServerRequestThreadID(&ServerRequest{Kind: kind, ThreadID: " thread-1 "})
		if !ok || got != "thread-1" {
			t.Fatalf("ServerRequestThreadID(%s) = %q/%v, want thread-1/true", kind, got, ok)
		}
	}
	if got, ok := ServerRequestThreadID(&ServerRequest{Kind: ServerRequestApplyPatchApproval, ThreadID: "thread-1"}); ok || got != "" {
		t.Fatalf("threadless apply patch request = %q/%v, want none", got, ok)
	}
	if got, ok := ServerRequestThreadID(&ServerRequest{Kind: ServerRequestUserInput}); ok || got != "" {
		t.Fatalf("missing thread request = %q/%v, want none", got, ok)
	}
	if got, ok := ServerRequestThreadID(&ServerRequest{Kind: ServerRequestUserInput, ThreadID: "bad id"}); ok || got != "" {
		t.Fatalf("invalid thread request = %q/%v, want none", got, ok)
	}
}

func TestServerNotificationThreadTargetForEventMatchRustCases(t *testing.T) {
	target := ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationWarning})
	if target.Kind != ServerNotificationThreadTargetGlobal || target.ThreadID != "" {
		t.Fatalf("warning without thread target = %#v, want global", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationWarning, ThreadID: " thread-1 "})
	if target.Kind != ServerNotificationThreadTargetThread || target.ThreadID != "thread-1" {
		t.Fatalf("warning with thread target = %#v, want thread-1", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationMcpServerStatusUpdated})
	if target.Kind != ServerNotificationThreadTargetAppScoped {
		t.Fatalf("mcp status without thread target = %#v, want app scoped", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{
		Name:   ServerNotificationMcpServerStatusUpdated,
		Target: EventTarget{ThreadID: "thread-2"},
	})
	if target.Kind != ServerNotificationThreadTargetThread || target.ThreadID != "thread-2" {
		t.Fatalf("mcp status with target = %#v, want thread-2", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationGuardianWarning, ThreadID: "thread-3"})
	if target.Kind != ServerNotificationThreadTargetThread || target.ThreadID != "thread-3" {
		t.Fatalf("guardian warning target = %#v, want thread-3", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationThreadSettingsUpdated, ThreadID: "thread-settings"})
	if target.Kind != ServerNotificationThreadTargetThread || target.ThreadID != "thread-settings" {
		t.Fatalf("thread settings target = %#v, want thread-settings", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationThreadClosed, ThreadID: "bad id"})
	if target.Kind != ServerNotificationThreadTargetInvalid || target.ThreadID != "bad id" {
		t.Fatalf("invalid target = %#v, want invalid", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationConfigWarning, ThreadID: "thread-ignored"})
	if target.Kind != ServerNotificationThreadTargetGlobal || target.ThreadID != "" {
		t.Fatalf("config warning target = %#v, want global", target)
	}
}

func TestEventTargetFromServerEventUsesFallbackFields(t *testing.T) {
	got := EventTargetFromServerEvent(ServerEvent{
		ThreadID: " thread-1 ",
		TurnID:   " turn-1 ",
	})
	if got.ThreadID != "thread-1" || got.TurnID != "turn-1" {
		t.Fatalf("EventTargetFromServerEvent() = %#v", got)
	}

	got = EventTargetFromServerEvent(ServerEvent{
		Target:   EventTarget{ThreadID: "target-thread", TurnID: "target-turn"},
		ThreadID: "thread-1",
		TurnID:   "turn-1",
	})
	if got.ThreadID != "target-thread" || got.TurnID != "target-turn" {
		t.Fatalf("EventTargetFromServerEvent(target wins) = %#v", got)
	}
}

func TestRouteServerRequestAndNotificationMatchRustDispatch(t *testing.T) {
	route, threadID := RouteServerRequest("thread-1", &ServerRequest{Kind: ServerRequestUserInput, ThreadID: "thread-1"})
	if route != ServerEventRoutePrimary || threadID != "thread-1" {
		t.Fatalf("primary request route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerRequest("thread-1", &ServerRequest{Kind: ServerRequestUserInput, ThreadID: "thread-2"})
	if route != ServerEventRouteThread || threadID != "thread-2" {
		t.Fatalf("background request route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerRequest("", &ServerRequest{Kind: ServerRequestApplyPatchApproval})
	if route != ServerEventRouteThreadless || threadID != "" {
		t.Fatalf("threadless request route = %s/%q", route, threadID)
	}

	route, threadID = RouteServerNotification("thread-1", &ServerEvent{Name: ServerNotificationTurnCompleted, ThreadID: "thread-1"})
	if route != ServerEventRoutePrimary || threadID != "thread-1" {
		t.Fatalf("primary notification route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerNotification("thread-1", &ServerEvent{Name: ServerNotificationTurnCompleted, ThreadID: "thread-2"})
	if route != ServerEventRouteThread || threadID != "thread-2" {
		t.Fatalf("background notification route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerNotification("thread-1", &ServerEvent{Name: ServerNotificationWarning})
	if route != ServerEventRouteGlobal || threadID != "" {
		t.Fatalf("global notification route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerNotification("thread-1", &ServerEvent{Name: ServerNotificationMcpServerStatusUpdated})
	if route != ServerEventRouteAppScoped || threadID != "" {
		t.Fatalf("app scoped notification route = %s/%q", route, threadID)
	}
}
