package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/utils"
)

func agentsNavigationModel(t *testing.T, options Options) *Model {
	t.Helper()
	options.Width = 120
	options.Height = 24
	if options.OnAgentsOverviewRefresh == nil {
		options.OnAgentsOverviewRefresh = func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		}
	}
	return NewModel(nil, options)
}

// Mirrors Rust #41917: Left opens the agents overview from an empty, focused
// composer in a local-daemon session. A non-empty draft keeps Left local.
func TestAgentsNavigationOpensOverviewFromEmptyComposerLikeRust(t *testing.T) {
	model := agentsNavigationModel(t, Options{LocalDaemonSession: true})
	if !strings.Contains(utils.StripANSI(model.View()), "\u2190 for agents") {
		t.Fatalf("empty local-daemon composer missing the agents hint:\n%s", utils.StripANSI(model.View()))
	}
	updated, command := model.Update(key(bubbletea.KeyLeft))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("Left returned no dashboard refresh command")
	}
	if model.agentsOverview == nil {
		t.Fatal("Left did not open the agents overview")
	}

	draft := agentsNavigationModel(t, Options{LocalDaemonSession: true})
	typeText(t, draft, "ab")
	draft.Update(key(bubbletea.KeyLeft))
	if draft.agentsOverview != nil {
		t.Fatal("Left opened the agents overview from a non-empty draft")
	}
	if got := draft.composerText(); got != "ab" {
		t.Fatalf("draft text = %q, want ab", got)
	}
	if strings.Contains(utils.StripANSI(draft.View()), "for agents") {
		t.Fatal("non-empty draft still advertises the agents shortcut")
	}
}

// Local-daemon and remote sessions enable the shortcut; embedded sessions keep
// Left for the composer (Rust #57281 supports_command_center).
func TestAgentsNavigationRequiresSharedAppServerLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		options Options
	}{
		{name: "embedded", options: Options{}},
		{name: "embedded dashboard", options: Options{AgentsOverviewEmbedded: true}},
	} {
		model := agentsNavigationModel(t, testCase.options)
		if strings.Contains(utils.StripANSI(model.View()), "for agents") {
			t.Fatalf("%s session advertises the local-daemon shortcut", testCase.name)
		}
		updated, command := model.Update(key(bubbletea.KeyLeft))
		model = updated.(*Model)
		if command != nil || model.agentsOverview != nil {
			t.Fatalf("%s session opened the agents overview", testCase.name)
		}
	}
}

func TestAgentsNavigationOpensOverviewForRemoteSessionLikeRust(t *testing.T) {
	model := agentsNavigationModel(t, Options{RemoteAppServer: true})
	if !strings.Contains(utils.StripANSI(model.View()), "for agents") {
		t.Fatal("remote session is missing the agents footer hint")
	}
	updated, command := model.Update(key(bubbletea.KeyLeft))
	model = updated.(*Model)
	if command == nil || model.agentsOverview == nil {
		t.Fatal("remote session did not open the agents overview")
	}
}

// Draft contents, attachments, paste bursts, popups, and a pending Vim
// operator all keep Left out of agents navigation.
func TestAgentsNavigationPreservesTransientInputLikeRust(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Model)
	}{
		{
			name: "attachment",
			setup: func(model *Model) {
				model.attachments = []bottompane.ComposerAttachment{{
					Kind: bottompane.AttachmentImage,
					Path: "C:/tmp/image.png",
				}}
			},
		},
		{
			name: "paste burst",
			setup: func(model *Model) {
				now := model.currentTime()
				model.composerLastCharAt = &now
			},
		},
		{
			name: "slash popup",
			setup: func(model *Model) {
				model.slashPopup.Active = true
			},
		},
		{
			name: "vim operator pending",
			setup: func(model *Model) {
				model.vimMode = true
				model.vimInsert = false
				model.vimPendingOp = "d"
			},
		},
		{
			name: "vim search",
			setup: func(model *Model) {
				model.vimSearchMode = true
			},
		},
	}
	for _, testCase := range tests {
		model := agentsNavigationModel(t, Options{LocalDaemonSession: true})
		testCase.setup(model)
		if strings.Contains(utils.StripANSI(model.View()), "for agents") {
			t.Fatalf("%s: footer advertises the shortcut during a transient input state", testCase.name)
		}
		updated, command := model.Update(key(bubbletea.KeyLeft))
		model = updated.(*Model)
		if command != nil || model.agentsOverview != nil {
			t.Fatalf("%s: Left opened the agents overview", testCase.name)
		}
	}
}

// An explicit editor or Vim move_left remap keeps Left for the active keymap,
// including when another movement action claims it.
func TestAgentsNavigationRespectsEditorAndVimRemapsLikeRust(t *testing.T) {
	for _, vim := range []bool{false, true} {
		config := codextui.NewKeymapConfig()
		context := "editor"
		if vim {
			context = "vim_normal"
		}
		if err := config.Set(context, "move_left", []string{"ctrl-b"}); err != nil {
			t.Fatalf("remap %s.move_left: %v", context, err)
		}
		if err := config.Set(context, "move_up", []string{"left"}); err != nil {
			t.Fatalf("remap %s.move_up: %v", context, err)
		}
		model := agentsNavigationModel(t, Options{
			LocalDaemonSession: true,
			KeymapConfig:       config,
		})
		if vim {
			model.vimMode = true
			model.vimInsert = false
		}
		if model.agentsNavigationKeyAvailable() {
			t.Fatalf("vim=%v: remapped move_left still claims Left for agents", vim)
		}
		if strings.Contains(utils.StripANSI(model.View()), "for agents") {
			t.Fatalf("vim=%v: footer advertises a remapped Left", vim)
		}
		updated, command := model.Update(key(bubbletea.KeyLeft))
		model = updated.(*Model)
		if command != nil || model.agentsOverview != nil {
			t.Fatalf("vim=%v: Left opened the agents overview despite the remap", vim)
		}
	}
}

// The footer hint is gated on the same availability predicate as dispatch.
func TestAgentsNavigationFooterHintFollowsAvailabilityLikeRust(t *testing.T) {
	model := agentsNavigationModel(t, Options{LocalDaemonSession: true})
	if !strings.Contains(utils.StripANSI(model.View()), "\u2190 for agents") {
		t.Fatal("local-daemon empty composer must advertise the shortcut")
	}
	model.composerLastCharAt = func() *time.Time {
		now := model.currentTime()
		return &now
	}()
	if strings.Contains(utils.StripANSI(model.View()), "for agents") {
		t.Fatal("paste burst must hide the agents hint")
	}
}
