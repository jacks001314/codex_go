package app

import "testing"

const (
	rustThreadID1 = "00000000-0000-0000-0000-000000000001"
	rustThreadID2 = "00000000-0000-0000-0000-000000000002"
	rustThreadID3 = "00000000-0000-0000-0000-000000000003"
)

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
		got, ok := ServerRequestThreadID(&ServerRequest{Kind: kind, ThreadID: rustThreadID1})
		if !ok || got != rustThreadID1 {
			t.Fatalf("ServerRequestThreadID(%s) = %q/%v, want %s/true", kind, got, ok, rustThreadID1)
		}
	}
	if got, ok := ServerRequestThreadID(&ServerRequest{Kind: ServerRequestApplyPatchApproval, ThreadID: rustThreadID1}); ok || got != "" {
		t.Fatalf("threadless apply patch request = %q/%v, want none", got, ok)
	}
	if got, ok := ServerRequestThreadID(&ServerRequest{Kind: ServerRequestUserInput}); ok || got != "" {
		t.Fatalf("missing thread request = %q/%v, want none", got, ok)
	}
	for _, invalid := range []string{"bad id", " thread-1 ", " " + rustThreadID1 + " "} {
		if got, ok := ServerRequestThreadID(&ServerRequest{Kind: ServerRequestUserInput, ThreadID: invalid}); ok || got != "" {
			t.Fatalf("invalid thread request %q = %q/%v, want none", invalid, got, ok)
		}
	}
	if got, ok := ServerRequestThreadID(&ServerRequest{Kind: ServerRequestUserInput, ThreadID: "thread-1"}); ok || got != "" {
		t.Fatalf("invalid thread request = %q/%v, want none", got, ok)
	}
}

func TestServerNotificationThreadTargetForEventMatchRustCases(t *testing.T) {
	target := ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationWarning})
	if target.Kind != ServerNotificationThreadTargetGlobal || target.ThreadID != "" {
		t.Fatalf("warning without thread target = %#v, want global", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationWarning, ThreadID: rustThreadID1})
	if target.Kind != ServerNotificationThreadTargetThread || target.ThreadID != rustThreadID1 {
		t.Fatalf("warning with thread target = %#v, want %s", target, rustThreadID1)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationWarning, ThreadID: " " + rustThreadID1 + " "})
	if target.Kind != ServerNotificationThreadTargetInvalid || target.ThreadID != " "+rustThreadID1+" " {
		t.Fatalf("spaced warning target = %#v, want invalid", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationMcpServerStatusUpdated})
	if target.Kind != ServerNotificationThreadTargetAppScoped {
		t.Fatalf("mcp status without thread target = %#v, want app scoped", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{
		Name:   ServerNotificationMcpServerStatusUpdated,
		Target: EventTarget{ThreadID: rustThreadID2},
	})
	if target.Kind != ServerNotificationThreadTargetThread || target.ThreadID != rustThreadID2 {
		t.Fatalf("mcp status with target = %#v, want %s", target, rustThreadID2)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationGuardianWarning, ThreadID: rustThreadID3})
	if target.Kind != ServerNotificationThreadTargetThread || target.ThreadID != rustThreadID3 {
		t.Fatalf("guardian warning target = %#v, want %s", target, rustThreadID3)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationThreadSettingsUpdated, ThreadID: rustThreadID1})
	if target.Kind != ServerNotificationThreadTargetThread || target.ThreadID != rustThreadID1 {
		t.Fatalf("thread settings target = %#v, want %s", target, rustThreadID1)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationThreadClosed, ThreadID: "bad id"})
	if target.Kind != ServerNotificationThreadTargetInvalid || target.ThreadID != "bad id" {
		t.Fatalf("invalid target = %#v, want invalid", target)
	}

	target = ServerNotificationThreadTargetForEvent(&ServerEvent{Name: ServerNotificationConfigWarning, ThreadID: rustThreadID1})
	if target.Kind != ServerNotificationThreadTargetGlobal || target.ThreadID != "" {
		t.Fatalf("config warning target = %#v, want global", target)
	}
}

func TestEventTargetFromServerEventUsesFallbackFields(t *testing.T) {
	got := EventTargetFromServerEvent(ServerEvent{
		ThreadID: rustThreadID1,
		TurnID:   " turn-1 ",
	})
	if got.ThreadID != rustThreadID1 || got.TurnID != " turn-1 " {
		t.Fatalf("EventTargetFromServerEvent() = %#v", got)
	}

	got = EventTargetFromServerEvent(ServerEvent{
		Target:   EventTarget{ThreadID: rustThreadID2, TurnID: "target-turn"},
		ThreadID: rustThreadID1,
		TurnID:   "turn-1",
	})
	if got.ThreadID != rustThreadID2 || got.TurnID != "target-turn" {
		t.Fatalf("EventTargetFromServerEvent(target wins) = %#v", got)
	}
}

func TestRouteServerRequestAndNotificationMatchRustDispatch(t *testing.T) {
	route, threadID := RouteServerRequest(rustThreadID1, &ServerRequest{Kind: ServerRequestUserInput, ThreadID: rustThreadID1})
	if route != ServerEventRoutePrimary || threadID != rustThreadID1 {
		t.Fatalf("primary request route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerRequest(rustThreadID1, &ServerRequest{Kind: ServerRequestUserInput, ThreadID: rustThreadID2})
	if route != ServerEventRouteThread || threadID != rustThreadID2 {
		t.Fatalf("background request route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerRequest("", &ServerRequest{Kind: ServerRequestApplyPatchApproval})
	if route != ServerEventRouteThreadless || threadID != "" {
		t.Fatalf("threadless request route = %s/%q", route, threadID)
	}

	route, threadID = RouteServerNotification(rustThreadID1, &ServerEvent{Name: ServerNotificationTurnCompleted, ThreadID: rustThreadID1})
	if route != ServerEventRoutePrimary || threadID != rustThreadID1 {
		t.Fatalf("primary notification route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerNotification(rustThreadID1, &ServerEvent{Name: ServerNotificationTurnCompleted, ThreadID: rustThreadID2})
	if route != ServerEventRouteThread || threadID != rustThreadID2 {
		t.Fatalf("background notification route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerNotification(rustThreadID1, &ServerEvent{Name: ServerNotificationWarning})
	if route != ServerEventRouteGlobal || threadID != "" {
		t.Fatalf("global notification route = %s/%q", route, threadID)
	}
	route, threadID = RouteServerNotification(rustThreadID1, &ServerEvent{Name: ServerNotificationMcpServerStatusUpdated})
	if route != ServerEventRouteAppScoped || threadID != "" {
		t.Fatalf("app scoped notification route = %s/%q", route, threadID)
	}
}
