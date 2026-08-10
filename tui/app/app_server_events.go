package app

import "codex_go/appserver"

// Rust parity subset: codex-rs/tui/src/app/app_server_events.rs.

type ServerEventRoute string

const (
	ServerEventRoutePrimary    ServerEventRoute = "primary"
	ServerEventRouteThread     ServerEventRoute = "thread"
	ServerEventRouteGlobal     ServerEventRoute = "global"
	ServerEventRouteAppScoped  ServerEventRoute = "app_scoped"
	ServerEventRouteIgnored    ServerEventRoute = "ignored"
	ServerEventRouteThreadless ServerEventRoute = "threadless"
)

type AppServerEventKind string

const (
	AppServerEventLagged             AppServerEventKind = "lagged"
	AppServerEventServerNotification AppServerEventKind = "server_notification"
	AppServerEventServerRequest      AppServerEventKind = "server_request"
	AppServerEventDisconnected       AppServerEventKind = "disconnected"
)

type ServerEvent struct {
	Name       string
	Target     EventTarget
	ThreadID   string
	TurnID     string
	ItemID     string
	ItemKind   string
	Item       *appserver.ThreadItem
	Turn       *appserver.Turn
	RequestID  string
	ServerName string
}

type AppServerEventDecision struct {
	Kind                             AppServerEventKind
	ThreadID                         string
	NotificationRoute                ServerEventRoute
	RequestRoute                     ServerEventRoute
	RefreshMCPStartupExpectedServers bool
	FinishMCPStartupAfterLag         bool
	ErrorMessage                     string
	FatalExitMessage                 string
	RejectUnsupported                *UnsupportedAppServerRequest
	DismissResolvedRequest           *ResolvedAppServerRequest
	HandleGlobalNotification         bool
	UpdateRateLimits                 bool
	UpdateAccountState               bool
	ReloadExternalAgentConfig        bool
	LoadConnectorsSnapshot           bool
	IgnoredReason                    string
}

func HandleAppServerLaggedDecision() AppServerEventDecision {
	return AppServerEventDecision{
		Kind:                             AppServerEventLagged,
		RefreshMCPStartupExpectedServers: true,
		FinishMCPStartupAfterLag:         true,
	}
}

func HandleAppServerDisconnectedDecision(message string) AppServerEventDecision {
	return AppServerEventDecision{
		Kind:             AppServerEventDisconnected,
		ErrorMessage:     message,
		FatalExitMessage: message,
	}
}

func HandleServerNotificationEventDecision(primaryThreadID string, pending *PendingAppServerRequests, notification ServerEvent) AppServerEventDecision {
	decision := AppServerEventDecision{Kind: AppServerEventServerNotification}
	switch notification.Name {
	case ServerNotificationServerRequestResolved:
		if pending != nil {
			if resolved, ok := pending.ResolveNotification(notification.RequestID); ok {
				decision.DismissResolvedRequest = &resolved
			}
		}
	case ServerNotificationMcpServerStatusUpdated:
		decision.RefreshMCPStartupExpectedServers = true
	case ServerNotificationAccountRateLimitsUpdated:
		decision.UpdateRateLimits = true
		return decision
	case ServerNotificationAccountUpdated:
		decision.UpdateAccountState = true
		return decision
	case ServerNotificationExternalAgentConfigImportCompleted:
		decision.ReloadExternalAgentConfig = true
		return decision
	case ServerNotificationAppListUpdated:
		decision.LoadConnectorsSnapshot = true
		return decision
	}

	route, threadID := RouteServerNotification(primaryThreadID, &notification)
	decision.NotificationRoute = route
	decision.ThreadID = threadID
	switch route {
	case ServerEventRoutePrimary, ServerEventRouteThread:
		return decision
	case ServerEventRouteIgnored:
		decision.IgnoredReason = "invalid_thread_id"
		return decision
	case ServerEventRouteAppScoped:
		decision.IgnoredReason = "app_scoped_without_tui_target"
		return decision
	case ServerEventRouteGlobal:
		decision.HandleGlobalNotification = true
		return decision
	default:
		decision.IgnoredReason = "ignored"
		return decision
	}
}

func HandleServerRequestEventDecision(primaryThreadID string, pending *PendingAppServerRequests, request ServerRequest) AppServerEventDecision {
	decision := AppServerEventDecision{Kind: AppServerEventServerRequest}
	if pending != nil {
		if unsupported := pending.NoteServerRequest(request); unsupported != nil {
			decision.RejectUnsupported = unsupported
			decision.ErrorMessage = unsupported.Message
			return decision
		}
	}
	route, threadID := RouteServerRequest(primaryThreadID, &request)
	decision.RequestRoute = route
	decision.ThreadID = threadID
	if route == ServerEventRouteThreadless {
		decision.IgnoredReason = "threadless_request"
	}
	return decision
}

func RouteServerRequest(primaryThreadID string, request *ServerRequest) (ServerEventRoute, string) {
	threadID, ok := ServerRequestThreadID(request)
	if !ok {
		return ServerEventRouteThreadless, ""
	}
	if primaryThreadID == "" || primaryThreadID == threadID {
		return ServerEventRoutePrimary, threadID
	}
	return ServerEventRouteThread, threadID
}

func RouteServerNotification(primaryThreadID string, notification *ServerEvent) (ServerEventRoute, string) {
	target := ServerNotificationThreadTargetForEvent(notification)
	switch target.Kind {
	case ServerNotificationThreadTargetThread:
		if primaryThreadID == "" || primaryThreadID == target.ThreadID {
			return ServerEventRoutePrimary, target.ThreadID
		}
		return ServerEventRouteThread, target.ThreadID
	case ServerNotificationThreadTargetAppScoped:
		return ServerEventRouteAppScoped, ""
	case ServerNotificationThreadTargetGlobal:
		return ServerEventRouteGlobal, ""
	default:
		return ServerEventRouteIgnored, target.ThreadID
	}
}
