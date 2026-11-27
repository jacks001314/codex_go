package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

type observedGuardianActionRoot struct {
	turnID string
	root   string
	known  bool
}

func TestGuardianNetworkApprovalReportsVerifiedExecutionRootLikeRust(t *testing.T) {
	const (
		threadID      = "thread-action-root"
		callID        = "execution-action-root"
		issuingRoot   = "issuing-root"
		servicingRoot = "servicing-root"
	)
	router := newOriginatingExecutionNetworkRouter(t, "auto_review")
	cwd := t.TempDir()
	startWithRoot := func(turnID, root string) {
		params := &turn.TurnStartParams{
			ThreadID: threadID, CWD: cwd, RootTurnID: root,
			Environments: []map[string]any{{"environmentId": "local", "cwd": cwd}},
		}
		reviewer := "auto_review"
		params.ApprovalsReviewer = &reviewer
		if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, 1, params); err != nil {
			t.Fatal(err)
		}
		router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "connection-action-root", &appTurnRunConfig{
			Model: "model", Originator: "codex-test", ApprovalPolicy: "on-request", RootTurnID: root,
		})
	}
	startWithRoot("turn-issuing", issuingRoot)
	registerOriginatingExecutionCall(t, router, context.Background(), threadID, "turn-issuing", callID, cwd, "local")

	observed := make(chan observedGuardianActionRoot, 3)
	router.services.GuardianReviewer = networkGuardianReviewerFunc(func(ctx context.Context, gotThreadID, gotTurnID, _ string, _ state.Action) (state.ReviewDecision, string, error) {
		root, known := guardianReviewedActionRootTurnIDFromContext(ctx)
		observed <- observedGuardianActionRoot{turnID: gotTurnID, root: root, known: known}
		return state.DecisionDenied, "test denial", nil
	})

	tagged := originatingExecutionPolicyRequest(callID, "local")
	if decision := router.networkApproval.decideForThread(context.Background(), threadID, tagged); decision.Allow {
		t.Fatalf("tagged decision = %#v, want denied by fixture", decision)
	}
	first := <-observed
	if !first.known || first.root != issuingRoot || first.turnID != "turn-issuing" {
		t.Fatalf("tagged review attribution = %#v", first)
	}

	untagged := originatingExecutionPolicyRequest(callID, "local")
	untagged.ExecutionID = ""
	if decision := router.networkApproval.decideForThread(context.Background(), threadID, untagged); decision.Allow {
		t.Fatalf("untagged decision = %#v, want denied by fixture", decision)
	}
	second := <-observed
	if !second.known || second.root != "" || second.turnID != "turn-issuing" {
		t.Fatalf("untagged review attribution = %#v, want explicitly unknown root", second)
	}

	router.networkApproval.markExecutionPersistent(threadID, "turn-issuing", callID)
	router.clearActiveRuntimeTurn(threadID, "turn-issuing")
	startWithRoot("turn-servicing", servicingRoot)
	t.Cleanup(func() { router.clearActiveRuntimeTurn(threadID, "turn-servicing") })
	if decision := router.networkApproval.decideForThread(context.Background(), threadID, tagged); decision.Allow {
		t.Fatalf("cross-turn decision = %#v, want denied by fixture", decision)
	}
	third := <-observed
	if !third.known || third.root != issuingRoot || third.turnID != "turn-servicing" {
		t.Fatalf("cross-turn review attribution = %#v, want servicing turn and issuing root", third)
	}
}

func TestGuardianNetworkApprovalUntaggedAmbiguousRequestOmitsReviewedRootLikeRust(t *testing.T) {
	const (
		threadID      = "thread-ambiguous-action-root"
		turnID        = "turn-ambiguous-action-root"
		servicingRoot = "servicing-root"
	)
	router := newOriginatingExecutionNetworkRouter(t, "auto_review")
	cwd := t.TempDir()
	params := &turn.TurnStartParams{
		ThreadID: threadID, CWD: cwd, RootTurnID: servicingRoot,
		Environments: []map[string]any{{"environmentId": "local", "cwd": cwd}},
	}
	reviewer := "auto_review"
	params.ApprovalsReviewer = &reviewer
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, 1, params); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { router.clearActiveRuntimeTurn(threadID, turnID) })
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "connection-ambiguous-action-root", &appTurnRunConfig{
		Model: "model", Originator: "codex-test", ApprovalPolicy: "on-request", RootTurnID: servicingRoot,
	})
	// Two matching executions make an untagged request ambiguous, so Guardian
	// reviews it without an owner call. Its reviewed-action root stays unknown.
	registerOriginatingExecutionCall(t, router, context.Background(), threadID, turnID, "execution-a", cwd, "local")
	registerOriginatingExecutionCall(t, router, context.Background(), threadID, turnID, "execution-b", cwd, "local")

	observed := make(chan observedGuardianActionRoot, 1)
	router.services.GuardianReviewer = networkGuardianReviewerFunc(func(ctx context.Context, gotThreadID, gotTurnID, _ string, _ state.Action) (state.ReviewDecision, string, error) {
		root, known := guardianReviewedActionRootTurnIDFromContext(ctx)
		observed <- observedGuardianActionRoot{turnID: gotTurnID, root: root, known: known}
		return state.DecisionDenied, "test denial", nil
	})
	untagged := originatingExecutionPolicyRequest("", "local")
	if decision := router.networkApproval.decideForThread(context.Background(), threadID, untagged); decision.Allow {
		t.Fatalf("untagged decision = %#v, want denied by fixture", decision)
	}
	got := <-observed
	if !got.known || got.root != "" || got.turnID != turnID {
		t.Fatalf("ambiguous untagged review attribution = %#v, want servicing turn %q and explicitly unknown root", got, turnID)
	}
}

func TestGuardianReviewCompletedAnalyticsCarriesReviewedActionRootLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`model = "gpt-5"`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	analyticsSink := newRecordingTurnEventSink()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:          NewRouter(store),
		Config:                config.NewConfigService(home),
		Turns:                 turn.NewTurnService(),
		ThreadStatus:          NewThreadStatusManager(),
		Analytics:             analyticsSink,
		DefaultCWD:            home,
		AnalyticsRPCTransport: telemetry.AppServerRPCTransportInProcess,
	})
	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-tui", Version: "1.2.3"},
	})
	initialize.ConnectionID = "connection-guardian-root"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}

	const (
		threadID = "thread-guardian-root"
		turnID   = "turn-guardian-root"
		reviewID = "review-guardian-root"
		rootID   = "root-reviewed-action"
	)
	params := &turn.TurnStartParams{ThreadID: threadID, CWD: home, RootTurnID: rootID}
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, 1, params); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { router.clearActiveRuntimeTurn(threadID, turnID) })
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, initialize.ConnectionID, &appTurnRunConfig{})
	root := rootID
	target := "call-guardian-root"
	router.emitGuardianReviewCompletedAnalytics(context.Background(), &ItemGuardianApprovalReviewCompletedNotification{
		ThreadID: threadID, TurnID: turnID, StartedAtMS: 10, CompletedAtMS: 20,
		ReviewID: reviewID, TargetItemID: &target,
		Review:                           GuardianApprovalReview{Status: GuardianApprovalReviewApproved},
		Action:                           GuardianApprovalReviewAction{Type: "command"},
		guardianReviewedActionRootTurnID: &root,
	})
	var event telemetry.GuardianReviewEventRequest
	select {
	case event = <-analyticsSink.guardianReview:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Guardian review analytics")
	}
	if event.EventType != telemetry.CodexGuardianReviewEventType ||
		event.EventParams.TurnID != turnID ||
		event.EventParams.ReviewID != reviewID ||
		event.EventParams.RootTurnID == nil ||
		*event.EventParams.RootTurnID != rootID {
		t.Fatalf("guardian review analytics = %#v, want turn %q, review %q and root %q", event, turnID, reviewID, rootID)
	}
}
