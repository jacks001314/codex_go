package tea

import (
	"errors"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: #42401 (d4dc882998). collaboration_modes::list feeds
// ModelCatalog::with_collaboration_modes; the discovered catalog is the only
// visible collaboration-mode inventory, discovery is optional with a
// two-second bound, and a missing catalog means no server modes.

func serverPlanCatalog() []chatwidget.CollaborationModeMask {
	plan := chatwidget.CollaborationModeKindPlan
	model := "gpt-5-plan"
	return []chatwidget.CollaborationModeMask{{
		Name:                  "Plan (server)",
		Mode:                  &plan,
		Model:                 &model,
		ReasoningEffort:       chatwidget.CollaborationValue("high"),
		DeveloperInstructions: chatwidget.CollaborationValue("restored override"),
	}}
}

func TestWithCollaborationModesInstallsServerCatalogLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	model.withCollaborationModes(serverPlanCatalog())

	presets := model.collaborationModePresets()
	if len(presets) != 1 || presets[0].Name != "Plan (server)" {
		t.Fatalf("presets = %#v", presets)
	}
	mask, ok := chatwidget.PlanCollaborationMask(presets)
	if !ok || mask.Model == nil || *mask.Model != "gpt-5-plan" {
		t.Fatalf("plan mask = %#v", mask)
	}
	if mask.ReasoningEffort.Value == nil || *mask.ReasoningEffort.Value != "high" {
		t.Fatalf("plan effort = %#v", mask.ReasoningEffort)
	}
	// Rust sets `developer_instructions: Some(None)`: the RPC omits prompts and
	// the server supplies them when the mode is selected.
	if !mask.DeveloperInstructions.Present || mask.DeveloperInstructions.Value != nil {
		t.Fatalf("developer instructions were not cleared: %#v", mask.DeveloperInstructions)
	}
}

func TestCollaborationModeDiscoveryFailureLeavesNoServerModesLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	model.applyCollaborationModesResult(CollaborationModesResultMsg{Err: errors.New("method not found")})

	if presets := model.collaborationModePresets(); len(presets) != 0 {
		t.Fatalf("presets after a failed discovery = %#v, want none", presets)
	}
	if model.planModeAvailable() {
		t.Fatal("plan mode stayed available without a server catalog")
	}
}

func TestCollaborationModeDiscoveryUsesServerCatalogLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 24,
		OnListCollaborationModes: func() ([]chatwidget.CollaborationModeMask, error) {
			return serverPlanCatalog(), nil
		},
	})
	if model.onListCollaborationModes == nil {
		t.Fatal("the option was not wired into the model")
	}
	command := model.discoverCollaborationModes()
	if command == nil {
		t.Fatal("discovery did not start")
	}
	message, ok := command().(CollaborationModesResultMsg)
	if !ok || message.Err != nil {
		t.Fatalf("discovery message = %#v", message)
	}
	model.applyCollaborationModesResult(message)
	if !model.planModeAvailable() {
		t.Fatal("the server plan mask was not installed")
	}
}

func TestCollaborationModeDiscoveryTimesOutLikeRust(t *testing.T) {
	previous := collaborationModeDiscoveryTimeout
	collaborationModeDiscoveryTimeout = 20 * time.Millisecond
	defer func() { collaborationModeDiscoveryTimeout = previous }()

	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 24,
		OnListCollaborationModes: func() ([]chatwidget.CollaborationModeMask, error) {
			time.Sleep(250 * time.Millisecond)
			return serverPlanCatalog(), nil
		},
	})
	message := model.discoverCollaborationModes()().(CollaborationModesResultMsg)
	if !errors.Is(message.Err, errCollaborationModeDiscoveryTimeout) {
		t.Fatalf("timeout error = %v", message.Err)
	}
	model.applyCollaborationModesResult(message)
	if len(model.collaborationModePresets()) != 0 {
		t.Fatal("a timed-out discovery installed a catalog")
	}
}

func TestPlanCommandUsesServerCatalogLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		FeatureSettings: map[string]bool{"collaboration_modes": true},
	})
	defaultKind := chatwidget.CollaborationModeKindDefault
	model.withCollaborationModes([]chatwidget.CollaborationModeMask{{Name: "Default", Mode: &defaultKind}})

	typeText(t, model, "/plan investigate")
	model.Update(key(bubbletea.KeyEnter))
	if state.PlanMode {
		t.Fatal("plan mode was selected although the server catalog has no plan mask")
	}
	if !strings.Contains(model.View(), "Plan mode unavailable right now.") {
		t.Fatalf("plan-unavailable notice missing:\n%s", model.View())
	}

	model.withCollaborationModes(serverPlanCatalog())
	typeText(t, model, "/plan investigate")
	model.Update(key(bubbletea.KeyEnter))
	if !state.PlanMode {
		t.Fatal("plan mode was not selected from the server catalog")
	}
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].CollaborationMode == nil {
		t.Fatalf("plan request = %#v", requests)
	}
	mode := requests[0].CollaborationMode
	if mode.Settings.Model != "gpt-5-plan" {
		t.Fatalf("plan model = %q, want the server mask model", mode.Settings.Model)
	}
	if mode.Settings.ReasoningEffort == nil || *mode.Settings.ReasoningEffort != "high" {
		t.Fatalf("plan effort = %#v", mode.Settings.ReasoningEffort)
	}
	if mode.Settings.DeveloperInstructions != nil {
		t.Fatalf("cleared server prompts leaked local instructions: %q", *mode.Settings.DeveloperInstructions)
	}
}
