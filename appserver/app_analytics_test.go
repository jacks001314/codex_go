package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/session"
	"codex_go/telemetry"
	"codex_go/turn"
)

func waitForAppMentionedEvent(t *testing.T, sink *recordingTurnEventSink) telemetry.CodexAppMentionedEventRequest {
	t.Helper()
	select {
	case event := <-sink.appMentioned:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for codex_app_mentioned")
		return telemetry.CodexAppMentionedEventRequest{}
	}
}

func waitForAppUsedEvent(t *testing.T, sink *recordingTurnEventSink) telemetry.CodexAppUsedEventRequest {
	t.Helper()
	select {
	case event := <-sink.appUsed:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for codex_app_used")
		return telemetry.CodexAppUsedEventRequest{}
	}
}

// Mirrors Rust #45716's turn-input tracking: the connectors the input named
// explicitly join the thread's selection - which is what later decides whether an
// app call is explicit or implicit - and each mention is reported.
func TestExplicitAppMentionsPopulateTheSelectionLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const threadID = "thread-app-mentions"
	const turnID = "turn-app-mentions"
	params := &turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "check my calendar",
		Input: []turn.TurnUserInput{
			{Type: "mention", Path: "app://connector_calendar"},
			{Type: "mention", Path: "plugin://sample@openai-curated"},
		},
	}
	router.rememberExplicitAppMentions(context.Background(), threadID, turnID, "conn-model-attribution", params, &config.Config{Values: map[string]any{}}, "gpt-5")

	event := waitForAppMentionedEvent(t, sink).EventParams
	if event.ConnectorID == nil || *event.ConnectorID != "connector_calendar" {
		t.Fatalf("mentioned connector = %#v", event.ConnectorID)
	}
	if event.InvokeType == nil || *event.InvokeType != telemetry.InvocationTypeExplicit {
		t.Fatalf("mention invoke type = %#v", event.InvokeType)
	}
	if event.ThreadID == nil || *event.ThreadID != threadID || event.TurnID == nil || *event.TurnID != turnID {
		t.Fatalf("mention identity = %#v", event)
	}
	if event.ModelSlug == nil || *event.ModelSlug != "gpt-5" {
		t.Fatalf("mention model = %#v", event.ModelSlug)
	}
	// The plugin mention is not an app mention.
	select {
	case extra := <-sink.appMentioned:
		t.Fatalf("unexpected second mention event: %#v", extra)
	default:
	}

	selection := router.sessionStateForThread(threadID).MergeConnectorSelection()
	if len(selection) != 1 || selection[0] != "connector_calendar" {
		t.Fatalf("connector selection = %#v", selection)
	}
}

// Mirrors Rust's `maybe_track_codex_app_used` with its dedup and `invoke_type`:
// one event per turn and connector (the first keeps its classification), an
// explicitly selected connector is explicit, and a call without a connector is
// always reported.
func TestCodexAppUsedEventFollowsRustDedupAndSelection(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const threadID = "thread-app-used"
	const turnID = "turn-app-used"
	classification := telemetry.ElicitationTypeAuthOrLink

	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, "conn-model-attribution", "connector_calendar", "Calendar", "gpt-5", &classification)
	first := waitForAppUsedEvent(t, sink).EventParams
	if first.ConnectorID == nil || *first.ConnectorID != "connector_calendar" || first.AppName == nil || *first.AppName != "Calendar" {
		t.Fatalf("app identity = %#v", first)
	}
	if first.InvokeType == nil || *first.InvokeType != telemetry.InvocationTypeImplicit {
		t.Fatalf("unselected connector invoke type = %#v", first.InvokeType)
	}
	if first.ElicitationType == nil || *first.ElicitationType != telemetry.ElicitationTypeAuthOrLink {
		t.Fatalf("classification = %#v", first.ElicitationType)
	}
	if first.ModelSlug == nil || *first.ModelSlug != "gpt-5" {
		t.Fatalf("model = %#v", first.ModelSlug)
	}

	// The same turn and connector is deduplicated, another turn is not, and a
	// call without a connector is always reported.
	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, "conn-model-attribution", "connector_calendar", "Calendar", "gpt-5", &classification)
	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, "conn-model-attribution", "connector_drive", "Drive", "gpt-5", nil)
	drive := waitForAppUsedEvent(t, sink).EventParams
	if drive.ConnectorID == nil || *drive.ConnectorID != "connector_drive" || drive.ElicitationType != nil {
		t.Fatalf("second connector = %#v", drive)
	}
	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, "conn-model-attribution", "", "", "gpt-5", nil)
	unbound := waitForAppUsedEvent(t, sink).EventParams
	if unbound.ConnectorID != nil {
		t.Fatalf("unbound call = %#v", unbound)
	}
	router.emitCodexAppUsedEvent(context.Background(), threadID, "turn-other", "conn-model-attribution", "connector_calendar", "Calendar", "gpt-5", nil)
	other := waitForAppUsedEvent(t, sink).EventParams
	if other.TurnID == nil || *other.TurnID != "turn-other" {
		t.Fatalf("other turn = %#v", other)
	}
	select {
	case extra := <-sink.appUsed:
		t.Fatalf("an extra app-used event was emitted: %#v", extra)
	default:
	}

	// A connector the turn named explicitly is reported as explicit.
	router.sessionStateForThread(threadID).MergeConnectorSelection("connector_drive")
	router.emitCodexAppUsedEvent(context.Background(), threadID, "turn-explicit", "conn-model-attribution", "connector_drive", "Drive", "gpt-5", nil)
	explicit := waitForAppUsedEvent(t, sink).EventParams
	if explicit.InvokeType == nil || *explicit.InvokeType != telemetry.InvocationTypeExplicit {
		t.Fatalf("explicit invoke type = %#v", explicit.InvokeType)
	}
}

// Mirrors the end-to-end turn-input path: an app mention in the turn's prompt
// populates the thread's connector selection and reports a mention event.
func TestRuntimeRouterAppMentionEmitsMentionAndSelectsConnectorLikeRust(t *testing.T) {
	appService := apps.NewAppService([]apps.AppEntry{{
		ID:           "drive",
		Name:         "Google Drive",
		IsAccessible: true,
		IsEnabled:    true,
	}})
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	analyticsSink := newRecordingTurnEventSink()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:          NewRouter(store),
		Turns:                 turn.NewTurnService(),
		Agent:                 agent,
		Apps:                  appService,
		ThreadStatus:          NewThreadStatusManager(),
		Analytics:             analyticsSink,
		AnalyticsRPCTransport: telemetry.AppServerRPCTransportInProcess,
		DefaultCWD:            t.TempDir(),
	})
	router.SetNotificationSink(sink)

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-tui", Version: "1.2.3"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	})
	initialize.ConnectionID = "conn-app-mention"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	threadStart := requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{CWD: filepath.Join(t.TempDir())})
	threadStart.ConnectionID = "conn-app-mention"
	response := router.Handle(threadStart)
	if response.Error != nil {
		t.Fatalf("thread/start error: %+v", response.Error)
	}
	threadID := response.Result.(*ThreadStartResponse).Thread.ID
	turnStart := requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "Use [$drive](app://drive)",
	})
	turnStart.ConnectionID = "conn-app-mention"
	response = router.Handle(turnStart)
	if response.Error != nil {
		t.Fatalf("turn/start error: %+v", response.Error)
	}
	turnID := response.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	event := waitForAppMentionedEvent(t, analyticsSink).EventParams
	if event.ConnectorID == nil || *event.ConnectorID != "drive" {
		t.Fatalf("mentioned connector = %#v", event.ConnectorID)
	}
	if event.AppName == nil || *event.AppName != "Google Drive" {
		t.Fatalf("mentioned app name = %#v", event.AppName)
	}
	if event.ThreadID == nil || *event.ThreadID != threadID || event.TurnID == nil || *event.TurnID != turnID {
		t.Fatalf("mention identity = %#v", event)
	}
	if event.InvokeType == nil || *event.InvokeType != telemetry.InvocationTypeExplicit {
		t.Fatalf("mention invoke type = %#v", event.InvokeType)
	}
	selection := router.sessionStateForThread(threadID).MergeConnectorSelection()
	if len(selection) != 1 || selection[0] != "drive" {
		t.Fatalf("connector selection = %#v", selection)
	}
}

// Mirrors Rust #45716's skill-item half: a connector a selected skill's
// instructions link joins the thread's connector selection and is reported as a
// mention, so a later call for it counts as explicit.
func TestSkillInstructionsSelectAppsTheyMentionLikeRust(t *testing.T) {
	skillsRoot := t.TempDir()
	skillDir := filepath.Join(skillsRoot, "drive-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	skill := "---\nname: drive-helper\ndescription: Drive helper\n---\nRead [$drive](app://drive) before answering.\n"
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte(skill), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	appService := apps.NewAppService([]apps.AppEntry{{
		ID:           "drive",
		Name:         "Google Drive",
		IsAccessible: true,
		IsEnabled:    true,
	}})
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	analyticsSink := newRecordingTurnEventSink()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:          NewRouter(store),
		Turns:                 turn.NewTurnService(),
		Agent:                 agent,
		Apps:                  appService,
		Skills:                NewSkillsService([]string{skillsRoot}),
		ThreadStatus:          NewThreadStatusManager(),
		Analytics:             analyticsSink,
		AnalyticsRPCTransport: telemetry.AppServerRPCTransportInProcess,
		DefaultCWD:            t.TempDir(),
	})
	router.SetNotificationSink(sink)

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-tui", Version: "1.2.3"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	})
	initialize.ConnectionID = "conn-skill-mention"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	threadStart := requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()})
	threadStart.ConnectionID = "conn-skill-mention"
	response := router.Handle(threadStart)
	if response.Error != nil {
		t.Fatalf("thread/start error: %+v", response.Error)
	}
	threadID := response.Result.(*ThreadStartResponse).Thread.ID
	turnStart := requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "$drive-helper",
	})
	turnStart.ConnectionID = "conn-skill-mention"
	response = router.Handle(turnStart)
	if response.Error != nil {
		t.Fatalf("turn/start error: %+v", response.Error)
	}
	turnID := response.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	event := waitForAppMentionedEvent(t, analyticsSink).EventParams
	if event.ConnectorID == nil || *event.ConnectorID != "drive" {
		t.Fatalf("skill-derived mention = %#v", event.ConnectorID)
	}
	if event.InvokeType == nil || *event.InvokeType != telemetry.InvocationTypeExplicit {
		t.Fatalf("skill-derived invoke type = %#v", event.InvokeType)
	}
	selection := router.sessionStateForThread(threadID).MergeConnectorSelection()
	if len(selection) != 1 || selection[0] != "drive" {
		t.Fatalf("connector selection = %#v", selection)
	}
}
