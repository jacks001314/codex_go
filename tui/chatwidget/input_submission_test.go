package chatwidget

import (
	"reflect"
	"testing"

	"codex_go/apps"
	"codex_go/appserver"
	pluginapi "codex_go/plugin"
)

func TestAudioSubmissionRequiresAudioModelAndBuildsItems(t *testing.T) {
	message := UserMessage{LocalAudio: []string{"sample.wav"}, RemoteAudioURLs: []string{"data:audio/wav;base64,YQ=="}}
	blocked := DecideUserMessageSubmission(message, UserMessageTextHistoryRecord(), SubmissionOptions{SessionConfigured: true})
	if blocked.Accepted || !blocked.RestoreBlockedAudio {
		t.Fatalf("blocked = %#v", blocked)
	}
	accepted := DecideUserMessageSubmission(message, UserMessageTextHistoryRecord(), SubmissionOptions{SessionConfigured: true, CurrentModelHasAudio: true})
	if !accepted.Accepted || len(accepted.Items) != 2 || accepted.Items[0].Kind != SubmittedInputRemoteAudio || accepted.Items[1].Kind != SubmittedInputLocalAudio {
		t.Fatalf("accepted = %#v", accepted)
	}
}

func TestInputSubmissionMentionItemsOrderMatchesRust(t *testing.T) {
	message := UserMessage{
		Text:            "Use $writer [$reader](skill://skills/reader/SKILL.md) and $google-drive",
		LocalImages:     []string{"local.png"},
		RemoteImageURLs: []string{"https://example.test/remote.png"},
		MentionBindings: []string{
			"writer|skills/writer/SKILL.md",
			"Docs|plugin://docs@market",
			"figma|app://figma",
		},
	}
	decision := DecideUserMessageSubmission(message, UserMessageTextHistoryRecord(), SubmissionOptions{
		SessionConfigured:     true,
		CurrentModelHasImages: true,
		EffectiveModel:        "gpt-5",
		RequireModel:          true,
		MentionCatalog: SubmissionMentionCatalog{
			Skills: []appserver.SkillsListEntry{
				{Name: "writer", Path: "skills/writer/SKILL.md", Enabled: true},
				{Name: "reader", Path: "skills/reader/SKILL.md", Enabled: true},
			},
			Plugins: []pluginapi.PluginSummary{
				{ID: "docs@market", Name: "Docs"},
			},
			Apps: []apps.AppEntry{
				{ID: "figma", Name: "Figma", IsAccessible: true, IsEnabled: true},
				{ID: "google_drive", Name: "Google Drive", IsAccessible: true, IsEnabled: true},
			},
		},
	})

	want := []SubmittedInputItem{
		{Kind: SubmittedInputRemoteImage, URL: "https://example.test/remote.png"},
		{Kind: SubmittedInputLocalImage, Path: "local.png"},
		{Kind: SubmittedInputText, Text: message.Text},
		{Kind: SubmittedInputSkill, Name: "writer", Path: "skills/writer/SKILL.md"},
		{Kind: SubmittedInputSkill, Name: "reader", Path: "skills/reader/SKILL.md"},
		{Kind: SubmittedInputPlugin, Name: "Docs", Path: "plugin://docs@market"},
		{Kind: SubmittedInputApp, Name: "Figma", Path: "app://figma"},
		{Kind: SubmittedInputApp, Name: "Google Drive", Path: "app://google_drive"},
	}
	if !decision.Accepted || !reflect.DeepEqual(decision.Items, want) {
		t.Fatalf("decision items = %#v, want %#v", decision.Items, want)
	}
	if !decision.UserTurnPendingStart || decision.PendingSteer != nil {
		t.Fatalf("foreground submission flags = %#v", decision)
	}
}

func TestInputSubmissionUnavailableModelRestoresComposerMatchRust(t *testing.T) {
	decision := DecideUserMessageSubmission(NewUserMessage("hello"), UserMessageTextHistoryRecord(), SubmissionOptions{
		SessionConfigured:     true,
		CurrentModelHasImages: true,
		RequireModel:          true,
	})

	if decision.Accepted || !decision.RestoreUnavailableModel || decision.ErrorMessage != ThreadModelUnavailableMessage {
		t.Fatalf("unavailable model decision = %#v", decision)
	}
	if decision.QueueDrain != QueueDrainContinue {
		t.Fatalf("queue drain = %q, want continue", decision.QueueDrain)
	}
}

func TestInputSubmissionSkillMentionAllowsPluginPrefixColonLikeRust(t *testing.T) {
	decision := DecideUserMessageSubmission(NewUserMessage("$Docs:review"), UserMessageTextHistoryRecord(), SubmissionOptions{
		SessionConfigured:     true,
		CurrentModelHasImages: true,
		MentionCatalog: SubmissionMentionCatalog{
			Skills: []appserver.SkillsListEntry{{
				Name:    "Docs:review",
				Path:    "plugins/docs/skills/review/SKILL.md",
				Enabled: true,
			}},
		},
	})

	var found bool
	for _, item := range decision.Items {
		if item.Kind == SubmittedInputSkill && item.Name == "Docs:review" {
			found = true
			if item.Path != "plugins/docs/skills/review/SKILL.md" {
				t.Fatalf("skill path = %q", item.Path)
			}
		}
	}
	if !found {
		t.Fatalf("decision items = %#v", decision.Items)
	}
}

func TestInputSubmissionAgentTurnCreatesPendingSteerCompareKeyMatchRust(t *testing.T) {
	message := UserMessage{
		Text:            "please inspect $writer",
		LocalImages:     []string{"local.png"},
		RemoteImageURLs: []string{"https://example.test/remote.png"},
	}
	decision := DecideUserMessageSubmission(message, UserMessageTextHistoryRecord(), SubmissionOptions{
		SessionConfigured:     true,
		CurrentModelHasImages: true,
		AgentTurnRunning:      true,
		EffectiveModel:        "gpt-5",
		RequireModel:          true,
		MentionCatalog: SubmissionMentionCatalog{
			Skills: []appserver.SkillsListEntry{
				{Name: "writer", Path: "skills/writer/SKILL.md", Enabled: true},
			},
		},
	})

	if !decision.Accepted || decision.UserTurnPendingStart {
		t.Fatalf("pending steer accepted flags = %#v", decision)
	}
	if decision.PendingSteer == nil {
		t.Fatal("expected pending steer")
	}
	wantKey := PendingSteerCompareKey{Message: "please inspect $writer", ImageCount: 2}
	if decision.PendingSteer.CompareKey != wantKey {
		t.Fatalf("compare key = %#v, want %#v", decision.PendingSteer.CompareKey, wantKey)
	}
	if decision.PendingSteer.UserMessage.Text != message.Text {
		t.Fatalf("pending steer message = %#v", decision.PendingSteer.UserMessage)
	}
}

func TestInputSubmissionQueueBeforeConfiguredPushesFrontMatchRust(t *testing.T) {
	state := InputQueueState{
		QueuedUserMessages: []QueuedUserMessage{
			NewQueuedUserMessage(NewUserMessage("older"), QueuedInputPlain),
		},
		QueuedUserMessageHistoryRecords: []UserMessageHistoryRecord{
			UserMessageTextHistoryRecord(),
		},
	}

	state.QueueUserMessageBeforeSessionConfigured(NewUserMessage("new before session"), UserMessageOverrideHistoryRecord("history new"))

	if got := []string{state.QueuedUserMessages[0].UserMessage.Text, state.QueuedUserMessages[1].UserMessage.Text}; !reflect.DeepEqual(got, []string{"new before session", "older"}) {
		t.Fatalf("queue order = %#v", got)
	}
	if state.QueuedUserMessageHistoryRecords[0].Kind != UserMessageHistoryOverride || state.QueuedUserMessageHistoryRecords[0].Text != "history new" {
		t.Fatalf("history order = %#v", state.QueuedUserMessageHistoryRecords)
	}
	decision := DecideUserMessageSubmission(NewUserMessage("hello"), UserMessageTextHistoryRecord(), SubmissionOptions{})
	if !decision.QueueUntilConfigured || !decision.QueueAtFront {
		t.Fatalf("queue decision = %#v", decision)
	}
}

func TestSubmitQueuedLiteralPromptDisablesShellEscape(t *testing.T) {
	// Rust #39604: queued literal input beginning with `!` drains as model
	// input with shell escapes disabled, never as a local shell command.
	decision := SubmitQueuedLiteralPrompt(NewUserMessage("!ls"))
	if decision.ShellCommand != "" || decision.QueueDrain != QueueDrainStop {
		t.Fatalf("literal prompt should not become a shell command: %#v", decision)
	}
	if len(decision.Items) != 1 || decision.Items[0].Kind != SubmittedInputText || decision.Items[0].Text != "!ls" {
		t.Fatalf("literal prompt items = %#v", decision.Items)
	}

	shell := SubmitQueuedShellPrompt(NewUserMessage("!ls"))
	if shell.ShellCommand != "ls" {
		t.Fatalf("shell prompt command = %q, want ls", shell.ShellCommand)
	}
}
