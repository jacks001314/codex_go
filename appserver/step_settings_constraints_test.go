package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

func TestStepSettingsConstraintsUseProposedPermissionsLikeRust(t *testing.T) {
	requirements := &config.ConfigRequirements{
		AutoReview: &config.AutoReviewRequirements{RequiredOnModels: []string{"protected-model"}},
	}
	readOnly := &sandbox.PermissionProfile{SandboxPolicy: sandbox.NewReadOnlyPolicy()}
	fullAccess := &sandbox.PermissionProfile{
		Disabled:       true,
		SandboxPolicy:  sandbox.NewDangerFullAccessPolicy(),
		NetworkEnabled: true,
	}

	if _, err := applyStepSettingsConstraints(stepSettingsConstraints{
		requirements:            requirements,
		guardianApprovalEnabled: true,
		hasFullDiskWriteAccess:  true,
		model:                   "protected-model",
		approvalsReviewer:       "user",
		modelChanged:            true,
	}); err == nil || !strings.Contains(err.Error(), "you need to use auto review") {
		t.Fatalf("full-access protected model error = %v", err)
	}

	reviewer, err := applyStepSettingsConstraints(stepSettingsConstraints{
		requirements:            requirements,
		guardianApprovalEnabled: true,
		hasFullDiskWriteAccess:  permissionProfileHasFullDiskWriteAccess(readOnly),
		model:                   "protected-model",
		approvalsReviewer:       "user",
		modelChanged:            true,
	})
	if err != nil {
		t.Fatalf("read-only protected model error = %v", err)
	}
	if reviewer != string(config.ApprovalsReviewerAutoReview) {
		t.Fatalf("reviewer = %q, want auto_review", reviewer)
	}

	if _, err := applyStepSettingsConstraints(stepSettingsConstraints{
		requirements:              requirements,
		guardianApprovalEnabled:   true,
		hasFullDiskWriteAccess:    permissionProfileHasFullDiskWriteAccess(readOnly),
		model:                     "protected-model",
		approvalsReviewer:         "user",
		approvalsReviewerExplicit: true,
	}); err == nil || !strings.Contains(err.Error(), "you need to use auto review") {
		t.Fatalf("explicit user reviewer error = %v", err)
	}

	if _, err := applyStepSettingsConstraints(stepSettingsConstraints{
		requirements:            requirements,
		guardianApprovalEnabled: false,
		hasFullDiskWriteAccess:  permissionProfileHasFullDiskWriteAccess(readOnly),
		model:                   "protected-model",
		approvalsReviewer:       string(config.ApprovalsReviewerAutoReview),
	}); err == nil || !strings.Contains(err.Error(), "you need to use auto review") {
		t.Fatalf("disabled Guardian error = %v", err)
	}

	if !permissionProfileHasFullDiskWriteAccess(fullAccess) {
		t.Fatal("full-access profile was not classified as full-disk write access")
	}
	if permissionProfileHasFullDiskWriteAccess(readOnly) {
		t.Fatal("read-only profile was classified as full-disk write access")
	}
}

func TestThreadSettingsProtectedModelUsesProposedPermissionsLikeRust(t *testing.T) {
	router, threadID, _ := newStepSettingsConstraintRouter(t)
	fullAccess := ":" + sandbox.BuiltInPermissionProfileDangerFullAccess
	if response := router.Handle(requestWithParams(t, IntID(1), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    threadID,
		Permissions: &fullAccess,
	})); response.Error != nil {
		t.Fatalf("initial full-access update error: %+v", response.Error)
	}

	protectedModel := "protected-model"
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID: threadID,
		Model:    &protectedModel,
	}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "you need to use auto review") {
		t.Fatalf("model-only update error = %+v", response.Error)
	}
	settings := router.threadSettingsForTurn(threadID)
	if settings == nil || settings.Model != "" || settings.ActivePermissionProfile == nil ||
		*settings.ActivePermissionProfile != fullAccess {
		t.Fatalf("failed update changed settings: %#v", settings)
	}

	readOnly := ":" + sandbox.BuiltInPermissionProfileReadOnly
	if response := router.Handle(requestWithParams(t, IntID(3), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    threadID,
		Model:       &protectedModel,
		Permissions: &readOnly,
	})); response.Error != nil {
		t.Fatalf("model plus read-only update error: %+v", response.Error)
	}
	settings = router.threadSettingsForTurn(threadID)
	if settings == nil || settings.Model != protectedModel ||
		settings.ApprovalsReviewer != string(config.ApprovalsReviewerAutoReview) ||
		settings.ActivePermissionProfile == nil || *settings.ActivePermissionProfile != readOnly {
		t.Fatalf("committed settings = %#v", settings)
	}

	response = router.Handle(requestWithParams(t, IntID(4), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    threadID,
		Permissions: &fullAccess,
	}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "you need to use auto review") {
		t.Fatalf("full-access revalidation error = %+v", response.Error)
	}
	settings = router.threadSettingsForTurn(threadID)
	if settings == nil || settings.ApprovalsReviewer != string(config.ApprovalsReviewerAutoReview) ||
		settings.ActivePermissionProfile == nil || *settings.ActivePermissionProfile != readOnly {
		t.Fatalf("rejected update changed settings: %#v", settings)
	}
}

func TestTurnStartProtectedModelUsesProposedPermissionsLikeRust(t *testing.T) {
	router, threadID, sink := newStepSettingsConstraintRouter(t)
	fullAccess := ":" + sandbox.BuiltInPermissionProfileDangerFullAccess
	if response := router.Handle(requestWithParams(t, IntID(1), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    threadID,
		Permissions: &fullAccess,
	})); response.Error != nil {
		t.Fatalf("initial full-access update error: %+v", response.Error)
	}

	protectedModel := "protected-model"
	response := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "use the protected model",
		Model:    protectedModel,
	}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "you need to use auto review") {
		t.Fatalf("turn-start model-only error = %+v", response.Error)
	}

	readOnly := ":" + sandbox.BuiltInPermissionProfileReadOnly
	response = router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID:    threadID,
		Prompt:      "use the protected model",
		Model:       protectedModel,
		Permissions: &readOnly,
	}))
	if response.Error != nil {
		t.Fatalf("turn-start model plus read-only error: %+v", response.Error)
	}
	turnID := response.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	settings := router.threadSettingsForTurn(threadID)
	if settings == nil || settings.Model != protectedModel ||
		settings.ApprovalsReviewer != string(config.ApprovalsReviewerAutoReview) ||
		settings.ActivePermissionProfile == nil || *settings.ActivePermissionProfile != readOnly {
		t.Fatalf("turn-start committed settings = %#v", settings)
	}
}

func TestActiveTurnSettingsProtectedModelRejectsUserReviewerLikeRust(t *testing.T) {
	configService := config.NewConfigService(t.TempDir())
	configService.SetRequirements(&config.ConfigRequirements{
		AutoReview: &config.AutoReviewRequirements{RequiredOnModels: []string{"protected-model"}},
	})
	agent := newBlockingAgent()
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		ThreadExtras: NewThreadExtraService(),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
		Config:       configService,
	})
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	readOnly := ":" + sandbox.BuiltInPermissionProfileReadOnly
	protectedModel := "protected-model"
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID:    threadID,
		Prompt:      "keep the protected turn active",
		Model:       protectedModel,
		Permissions: &readOnly,
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	select {
	case <-agent.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the protected turn to start")
	}

	user := string(config.ApprovalsReviewerUser)
	response := router.Handle(requestWithParams(t, IntID(3), MethodTurnSettingsUpdate, turn.TurnSettingsUpdateParams{
		ThreadID:          threadID,
		TurnID:            turnID,
		ApprovalsReviewer: &user,
	}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "you need to use auto review") {
		t.Fatalf("turn settings reviewer update error = %+v", response.Error)
	}
	active := router.activeRuntimeTurnStateSnapshot(threadID, turnID)
	if active == nil || active.Params == nil || active.Params.ApprovalsReviewer == nil ||
		*active.Params.ApprovalsReviewer != string(config.ApprovalsReviewerAutoReview) {
		t.Fatalf("rejected update changed active turn reviewer: %#v", active)
	}

	if interrupt := router.Handle(requestWithParams(t, IntID(4), MethodTurnInterrupt, turn.TurnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	})); interrupt.Error != nil {
		t.Fatalf("turn/interrupt error: %+v", interrupt.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusInterrupted)
}

func newStepSettingsConstraintRouter(t *testing.T) (*RuntimeRouter, string, *NotificationBuffer) {
	t.Helper()
	configService := config.NewConfigService(t.TempDir())
	configService.SetRequirements(&config.ConfigRequirements{
		AutoReview: &config.AutoReviewRequirements{RequiredOnModels: []string{"protected-model"}},
	})
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		ThreadExtras: NewThreadExtraService(),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
		Config:       configService,
	})
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	return router, start.Result.(*ThreadStartResponse).Thread.ID, sink
}
