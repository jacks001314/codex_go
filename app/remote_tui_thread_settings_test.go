package app

import (
	"encoding/json"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #45519: a resume response's collaborationMode becomes part of the
// thread's settings so the TUI applies the restored mode instead of falling back
// to Default, while an older server that omits it leaves the field unset.
func TestRemoteTUIResumeCarriesCollaborationModeLikeRust(t *testing.T) {
	effort := appserver.ReasoningEffort("high")
	instructions := "Keep planning"
	settings := remoteTUISettingsFromResume(&appserver.ThreadResumeResponse{
		Thread: &appserver.Thread{ID: "thread-plan"},
		CollaborationMode: &appserver.CollaborationMode{
			Mode: appserver.ModeKindPlan,
			Settings: appserver.CollaborationSettings{
				Model:                 "gpt-5.4",
				ReasoningEffort:       &effort,
				DeveloperInstructions: &instructions,
			},
		},
	})
	if settings == nil || settings.CollaborationMode == nil {
		t.Fatalf("settings = %#v, want the restored collaboration mode", settings)
	}
	if mode, _ := settings.CollaborationMode["mode"].(string); mode != "plan" {
		t.Fatalf("mode = %#v", settings.CollaborationMode["mode"])
	}
	inner, _ := settings.CollaborationMode["settings"].(map[string]any)
	if inner["model"] != "gpt-5.4" || inner["reasoning_effort"] != "high" || inner["developer_instructions"] != "Keep planning" {
		t.Fatalf("collaboration settings = %#v", inner)
	}

	old := remoteTUISettingsFromResume(&appserver.ThreadResumeResponse{Thread: &appserver.Thread{ID: "thread-old"}})
	if old == nil || old.CollaborationMode != nil {
		t.Fatalf("older server settings = %#v, want no collaboration mode", old)
	}
}

// TestRemoteTUIRoutesThreadSettingsUpdated covers Rust #43330/#43340/#44957: the
// app-server settings notification applies to the active thread, while another
// thread's update is forwarded thread-scoped so the agents dashboard can patch
// its retained row.
func TestRemoteTUIRoutesThreadSettingsUpdated(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	messages := make(chan bubbletea.Msg, 4)
	client := &remoteAppServerTUIClient{state: state, messages: messages}

	params, err := json.Marshal(appserver.SettingsUpdatedNotification{
		ThreadID:       "thread-a",
		ThreadSettings: appserver.Settings{Model: "server-model", ApprovalPolicy: "on-request", SandboxPolicy: "workspace-write"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadSettingsUpdated),
		Params: params,
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	select {
	case msg := <-messages:
		updated, ok := msg.(codextea.ThreadSettingsUpdatedMsg)
		if !ok {
			t.Fatalf("message = %T, want ThreadSettingsUpdatedMsg", msg)
		}
		if updated.ThreadID != "thread-a" || updated.Settings.Model != "server-model" {
			t.Fatalf("settings message = %#v", updated)
		}
	default:
		t.Fatal("active-thread settings notification was dropped")
	}

	otherParams, err := json.Marshal(appserver.SettingsUpdatedNotification{
		ThreadID:       "thread-b",
		ThreadSettings: appserver.Settings{Model: "background-model"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadSettingsUpdated),
		Params: otherParams,
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	select {
	case msg := <-messages:
		scoped, ok := msg.(codextea.ThreadScopedSettingsUpdatedMsg)
		if !ok {
			t.Fatalf("message = %T, want ThreadScopedSettingsUpdatedMsg", msg)
		}
		if scoped.ThreadID != "thread-b" || scoped.Settings.Model != "background-model" {
			t.Fatalf("scoped settings message = %#v", scoped)
		}
	default:
		t.Fatal("inactive-thread settings notification was dropped")
	}
}

// TestRemoteTUIRoutesThreadSettingsUpdatedRustWireShape uses the app-server's
// actual pointer-marshalled shape, where sandboxPolicy and
// activePermissionProfile are tagged objects, to prove the client decodes the
// notification instead of failing with an unmarshal error.
func TestRemoteTUIRoutesThreadSettingsUpdatedRustWireShape(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	messages := make(chan bubbletea.Msg, 4)
	client := &remoteAppServerTUIClient{state: state, messages: messages}

	profile := "trusted"
	params, err := json.Marshal(&appserver.SettingsUpdatedNotification{
		ThreadID: "thread-a",
		ThreadSettings: appserver.Settings{
			Model:                   "server-model",
			ApprovalPolicy:          "never",
			SandboxPolicy:           "read-only",
			ActivePermissionProfile: &profile,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadSettingsUpdated),
		Params: params,
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	select {
	case msg := <-messages:
		updated, ok := msg.(codextea.ThreadSettingsUpdatedMsg)
		if !ok {
			t.Fatalf("message = %T, want ThreadSettingsUpdatedMsg", msg)
		}
		if updated.Settings.SandboxPolicy != "read-only" || updated.Settings.ApprovalPolicy != "never" {
			t.Fatalf("settings message = %#v", updated.Settings)
		}
		if updated.Settings.ActivePermissionProfile == nil || *updated.Settings.ActivePermissionProfile != profile {
			t.Fatalf("active profile = %#v", updated.Settings.ActivePermissionProfile)
		}
	default:
		t.Fatal("object-shaped settings notification was dropped")
	}
}
