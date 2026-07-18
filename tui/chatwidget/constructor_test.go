package chatwidget

import "testing"

func TestNewChatWidgetSnapshotInitializesRustDefaults(t *testing.T) {
	config := ChatWidgetConfig{
		CWD:                 "  D:/work/repo  ",
		IsFirstRun:          true,
		HasChatGPTAccount:   true,
		HasCodexBackendAuth: true,
		FeatureSettings: map[string]bool{
			ChatWidgetFeatureApps:             true,
			ChatWidgetFeaturePreventIdleSleep: true,
		},
	}

	snapshot := NewChatWidgetSnapshot(config)

	if snapshot.Config.Model != "" || snapshot.SessionHeader.Model != ChatWidgetDefaultModelDisplayName {
		t.Fatalf("model/header = %q/%q", snapshot.Config.Model, snapshot.SessionHeader.Model)
	}
	if snapshot.CurrentCollaborationMode != ChatWidgetModeDefault {
		t.Fatalf("collaboration mode = %q", snapshot.CurrentCollaborationMode)
	}
	if !snapshot.PlaceholderSessionHeader || !snapshot.ShowWelcomeBanner {
		t.Fatalf("header/welcome flags = %v/%v", snapshot.PlaceholderSessionHeader, snapshot.ShowWelcomeBanner)
	}
	if snapshot.NormalPlaceholderText != ChatWidgetPlaceholders[0] || snapshot.SidePlaceholderText != ChatWidgetSidePlaceholders[0] {
		t.Fatalf("placeholders = %q/%q", snapshot.NormalPlaceholderText, snapshot.SidePlaceholderText)
	}
	if !snapshot.ConnectorsEnabled || !snapshot.TokenActivityCommandEnabled || !snapshot.PreventIdleSleep {
		t.Fatalf("derived feature flags = connectors:%v token:%v sleep:%v", snapshot.ConnectorsEnabled, snapshot.TokenActivityCommandEnabled, snapshot.PreventIdleSleep)
	}
	if snapshot.Status.CurrentStatus.Header != "Working" || snapshot.Runtime.Streaming.Width != 80 {
		t.Fatalf("status/runtime defaults = %#v/%#v", snapshot.Status, snapshot.Runtime.Streaming)
	}
	if snapshot.CurrentCWD != "D:/work/repo" {
		t.Fatalf("cwd = %q", snapshot.CurrentCWD)
	}
}

func TestNewChatWidgetSnapshotTrimsModelAndPreservesConfiguredSession(t *testing.T) {
	snapshot := NewChatWidgetSnapshot(ChatWidgetConfig{
		Model:             " gpt-5.1-codex ",
		SessionConfigured: true,
		CollaborationMode: " plan ",
	})

	if snapshot.Config.Model != "gpt-5.1-codex" || snapshot.SessionHeader.Model != "gpt-5.1-codex" {
		t.Fatalf("model/header = %q/%q", snapshot.Config.Model, snapshot.SessionHeader.Model)
	}
	if snapshot.PlaceholderSessionHeader {
		t.Fatal("configured session should not keep placeholder header active")
	}
	if snapshot.CurrentCollaborationMode != "plan" {
		t.Fatalf("mode = %q", snapshot.CurrentCollaborationMode)
	}
}

func TestChatWidgetSnapshotClonesConfig(t *testing.T) {
	initial := NewUserMessage("hello")
	config := ChatWidgetConfig{
		FeatureSettings:    map[string]bool{ChatWidgetFeatureApps: true},
		InitialUserMessage: &initial,
	}
	snapshot := NewChatWidgetSnapshot(config)
	config.FeatureSettings[ChatWidgetFeatureApps] = false
	initial.Text = "mutated"

	if !snapshot.Config.FeatureEnabled(ChatWidgetFeatureApps) {
		t.Fatalf("feature map should be cloned: %#v", snapshot.Config.FeatureSettings)
	}
	if snapshot.Config.InitialUserMessage == nil || snapshot.Config.InitialUserMessage.Text != "hello" {
		t.Fatalf("initial user message should be cloned: %#v", snapshot.Config.InitialUserMessage)
	}
}

func TestChatWidgetPlaceholderWrapsLikeRustCatalog(t *testing.T) {
	if got := ChatWidgetPlaceholder(len(ChatWidgetPlaceholders) + 1); got != ChatWidgetPlaceholders[1] {
		t.Fatalf("wrapped placeholder = %q", got)
	}
	if got := ChatWidgetSidePlaceholder(-5); got != ChatWidgetSidePlaceholders[0] {
		t.Fatalf("negative side placeholder = %q", got)
	}
}
