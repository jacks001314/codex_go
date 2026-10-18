package appserver

import (
	"testing"

	"codex_go/model"
	"codex_go/turn"
)

func TestAuthRecoveryNotificationsAreTurnScopedLikeRust(t *testing.T) {
	runner := model.NewResponsesAgentRunner(nil)
	router := NewRuntimeRouter(RuntimeServices{Agent: runner})
	defer func() { _ = router.Close() }()
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	const threadID = "thread-auth-recovery"
	const turnID = "turn-auth-recovery"
	agent := router.agentForAppTurn(&turn.TurnStartParams{ThreadID: threadID}, turnID)
	streaming, ok := agent.(*model.ResponsesAgentRunner)
	if !ok || streaming.AuthRecoveryHandler == nil {
		t.Fatalf("configured agent = %T, want a runner with an auth recovery handler", agent)
	}
	streaming.AuthRecoveryHandler(model.ProviderAuthRecoveryEvent{
		Phase:    model.ProviderAuthRecoveryPhaseStarted,
		Provider: "Amazon Bedrock",
		Message:  "AWS session has expired. Reauthenticating...",
	})
	streaming.AuthRecoveryHandler(model.ProviderAuthRecoveryEvent{
		Phase:    model.ProviderAuthRecoveryPhaseCompleted,
		Provider: "Amazon Bedrock",
		Message:  "Signed in with AWS.",
	})

	notifications := sink.List()
	if len(notifications) != 2 {
		t.Fatalf("notifications = %#v, want started and completed", notifications)
	}
	for index, want := range []struct {
		method  NotificationMethod
		message string
	}{
		{method: NotificationAuthRecoveryStarted, message: "AWS session has expired. Reauthenticating..."},
		{method: NotificationAuthRecoveryCompleted, message: "Signed in with AWS."},
	} {
		if notifications[index].Method != want.method {
			t.Fatalf("notification[%d].Method = %q, want %q", index, notifications[index].Method, want.method)
		}
		params, ok := notifications[index].Params.(*AuthRecoveryNotification)
		if !ok {
			t.Fatalf("notification[%d].Params = %T", index, notifications[index].Params)
		}
		if params.ThreadID != threadID || params.TurnID != turnID ||
			params.Provider != "Amazon Bedrock" || params.Message != want.message {
			t.Fatalf("notification[%d].Params = %#v", index, params)
		}
	}
}
