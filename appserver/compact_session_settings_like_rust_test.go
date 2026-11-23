package appserver

import (
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #63428: `handlers::compact` validates the session settings before it
// interrupts the active turn. A policy refresh that requires auto review for
// the thread's model therefore rejects `/compact` while the RPC still reports
// success: an idle thread gets a bad request plus an errored status, and a
// running turn gets a warning and keeps running.
func TestCompactSessionSettingsPreflightLikeRust(t *testing.T) {
	protectedModel := "protected-model"
	want := "Cannot start /compact. To use model `" + protectedModel + "`, you need to use auto review."

	t.Run("idle", func(t *testing.T) {
		router, threadID, sink := newCompactSessionSettingsRouter(t)
		commitProtectedModel(t, router, threadID, protectedModel)
		requireAutoReview(t, router)

		response := router.Handle(requestWithParams(t, IntID(3), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: threadID}))
		if response.Error != nil {
			t.Fatalf("rejected /compact returned an RPC error: %+v", response.Error)
		}

		var rejection *ErrorNotification
		for _, notification := range sink.List() {
			if notification.Method != NotificationError {
				continue
			}
			params, ok := notification.Params.(*ErrorNotification)
			if !ok || params == nil {
				continue
			}
			rejection = params
		}
		if rejection == nil {
			t.Fatalf("no error notification for the rejected /compact: %+v", sink.List())
		}
		if rejection.Error.Message != want {
			t.Fatalf("rejection message = %q, want %q", rejection.Error.Message, want)
		}
		if rejection.Error.CodexErrorInfo != CodexErrorInfo("badRequest") || rejection.WillRetry {
			t.Fatalf("rejection = %#v, want a non-retryable bad request", rejection)
		}
		if status := router.requireThreadStatus().LoadedStatusForThread(threadID); status.Type != "systemError" {
			t.Fatalf("thread status = %#v, want systemError", status)
		}
		if active := router.threads.ActiveTurn(threadID); active != nil {
			t.Fatalf("rejected /compact started a turn: %#v", active)
		}
	})

	t.Run("running turn", func(t *testing.T) {
		store := session.NewStore(t.TempDir())
		sink := NewNotificationBuffer()
		agent := newBlockingAgent()
		configService := config.NewConfigService(t.TempDir())
		router := NewRuntimeRouter(RuntimeServices{
			ThreadRouter: NewRouter(store),
			Turns:        turn.NewTurnService(),
			Agent:        agent,
			ThreadStatus: NewThreadStatusManager(),
			Models:       model.NewModelService(nil),
			Config:       configService,
		})
		router.SetNotificationSink(sink)
		t.Cleanup(func() { _ = router.Close() })

		threadID := startCompactSessionSettingsThread(t, router)
		commitProtectedModel(t, router, threadID, protectedModel)
		turnStart := router.Handle(requestWithParams(t, IntID(4), MethodTurnStart, turn.TurnStartParams{
			ThreadID: threadID,
			Prompt:   "running turn",
		}))
		if turnStart.Error != nil {
			t.Fatalf("turn start error: %+v", turnStart.Error)
		}
		turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
		waitForBlockingAgentStart(t, agent)
		requireAutoReview(t, router)

		response := router.Handle(requestWithParams(t, IntID(5), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: threadID}))
		if response.Error != nil {
			t.Fatalf("rejected /compact returned an RPC error: %+v", response.Error)
		}

		var warning *WarningNotification
		for _, notification := range sink.List() {
			if notification.Method != NotificationWarning {
				continue
			}
			params, ok := notification.Params.(*WarningNotification)
			if ok && params != nil && strings.HasPrefix(params.Message, "Cannot start /compact.") {
				warning = params
			}
		}
		if warning == nil || warning.Message != want {
			t.Fatalf("warning = %#v, want %q", warning, want)
		}
		if active := router.threads.ActiveTurn(threadID); active == nil || active.TurnID != turnID {
			t.Fatalf("active turn = %#v, want the running turn %s", active, turnID)
		}
		for _, notification := range sink.List() {
			if notification.Method != NotificationTurnCompleted {
				continue
			}
			params, ok := notification.Params.(*TurnCompletedNotification)
			if ok && params != nil && params.Turn.ID == turnID {
				t.Fatalf("rejected /compact ended the running turn: %#v", params.Turn)
			}
		}
		if status := router.requireThreadStatus().LoadedStatusForThread(threadID); status.Type != "active" {
			t.Fatalf("thread status = %#v, want active", status)
		}

		interrupt := router.Handle(requestWithParams(t, IntID(6), MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: threadID, TurnID: turnID}))
		if interrupt.Error != nil {
			t.Fatalf("interrupt error: %+v", interrupt.Error)
		}
		waitForTurnCompletedStatus(t, sink, turnID, TurnStatusInterrupted)
	})
}

func newCompactSessionSettingsRouter(t *testing.T) (*RuntimeRouter, string, *NotificationBuffer) {
	t.Helper()
	configService := config.NewConfigService(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
		Config:       configService,
	})
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })
	return router, startCompactSessionSettingsThread(t, router), sink
}

func startCompactSessionSettingsThread(t *testing.T, router *RuntimeRouter) string {
	t.Helper()
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	return start.Result.(*ThreadStartResponse).Thread.ID
}

// commitProtectedModel selects the model while the managed requirements are
// still empty, the state a thread is in before a policy refresh lands.
func commitProtectedModel(t *testing.T, router *RuntimeRouter, threadID string, model string) {
	t.Helper()
	response := router.Handle(requestWithParams(t, IntID(7), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID: threadID,
		Model:    &model,
	}))
	if response.Error != nil {
		t.Fatalf("model update error: %+v", response.Error)
	}
}

func requireAutoReview(t *testing.T, router *RuntimeRouter) {
	t.Helper()
	if router.services.Config == nil {
		t.Fatal("router has no config service")
	}
	router.services.Config.SetRequirements(&config.ConfigRequirements{
		AutoReview: &config.AutoReviewRequirements{RequiredOnModels: []string{"protected-model"}},
	})
}
