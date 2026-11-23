package tea

import (
	"strings"
	"testing"

	mousemsg "codex_go/tui/mousemsg"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

const teaThreadLinkID = "019e60fc-0518-7c21-9596-980fe97225ba"

const teaArchivedThreadLinkID = "019e60fc-0518-7c21-9596-980fe97225bb"

// threadLinkModel renders one assistant message that links to another saved
// conversation and records the thread ids the switch handler receives.
func threadLinkModel(t *testing.T, options Options, linkTarget string) (*Model, *[]string) {
	t.Helper()
	switched := &[]string{}
	options.Width = 100
	options.Height = 24
	options.OnSwitchAgent = func(threadID string) (AgentThreadSwitchResponse, error) {
		*switched = append(*switched, threadID)
		return AgentThreadSwitchResponse{
			Entry:    codextui.AgentThreadEntry{ThreadID: threadID},
			Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "linked transcript"}},
			Status:   "idle",
		}, nil
	}
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	state.AddMessage(codextui.RoleAssistant, "Open [Review notes]("+linkTarget+") for the earlier work.")
	model := NewModel(state, options)
	_ = model.viewString()
	return model, switched
}

// clickThreadLink presses Ctrl on the painted label and returns the command.
func clickThreadLink(t *testing.T, model *Model, needle string) bubbletea.Cmd {
	t.Helper()
	region := model.transcriptSelectionRegion
	if region.w <= 0 || region.h <= 0 {
		t.Fatalf("transcript region not registered: %#v", region)
	}
	if codextui.StripOSC8(strings.Join(region.text, "\n")) == "" {
		t.Fatal("empty transcript")
	}
	for row, line := range region.text {
		column := findDisplayColumn(line, needle)
		if column < 0 {
			continue
		}
		_, cmd := model.Update(mousemsg.MouseMsg{
			X:      region.x + column,
			Y:      region.y + row,
			Action: mousemsg.MouseActionPress,
			Button: mousemsg.MouseButtonLeft,
			Ctrl:   true,
		})
		return cmd
	}
	t.Fatalf("thread link label %q not found in %#v", needle, region.text)
	return nil
}

// TestThreadLinkClickOpensConversationLikeRust covers Rust #62332: a Markdown
// link to `codex://threads/<id>` switches the displayed conversation in place.
func TestThreadLinkClickOpensConversationLikeRust(t *testing.T) {
	linkTarget := "codex://threads/" + teaThreadLinkID
	model, switched := threadLinkModel(t, Options{}, linkTarget)
	cmd := clickThreadLink(t, model, "Review notes")
	if cmd == nil {
		t.Fatal("Ctrl-click on a thread link did not start a switch")
	}
	runTeaCmd(t, model, cmd)
	if len(*switched) != 1 || (*switched)[0] != teaThreadLinkID {
		t.Fatalf("switched threads = %#v, want %s", *switched, teaThreadLinkID)
	}
	if got := strings.TrimSpace(model.State.ThreadID); got != teaThreadLinkID {
		t.Fatalf("displayed thread = %q, want %s", got, teaThreadLinkID)
	}
	if model.readOnlyThread {
		t.Fatal("a live linked conversation opened read-only")
	}
}

// TestComposerGapThreadLinkOpensConversationLikeRust: Rust's
// open_url_in_browser intercepts a saved-conversation link before the browser
// opener, so a footer/tip thread link switches the conversation.
func TestComposerGapThreadLinkOpensConversationLikeRust(t *testing.T) {
	show := true
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	state.ThreadID = "thread-footer"
	model := NewModel(state, Options{Width: 100, Height: 30, ShowTooltips: &show})
	threadLink := "\x1b]8;;codex://threads/" + teaThreadLinkID + "\x07Review notes\x1b]8;;\x07"
	model.renderedSelection.regions[renderedRegionFooter] = renderedRegion{
		kind:  renderedRegionFooter,
		y:     10,
		x:     0,
		w:     100,
		h:     1,
		text:  []string{"Tip: " + codextui.StripOSC8(threadLink) + " end"},
		links: map[int][]regionThreadLink{0: {{Start: 5, End: 17, ThreadID: teaThreadLinkID}}},
	}
	opened := ""
	model.onOpenURL = func(url string) error {
		opened = url
		return nil
	}
	handled, cmd := model.handleComposerGapLinkClick(mousemsg.MouseMsg{
		X: 7, Y: 10, Ctrl: true, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
	})
	if !handled {
		t.Fatal("thread link click not handled")
	}
	runTeaCmdTree(t, model, cmd)
	if opened != "" {
		t.Fatalf("thread link reached the browser opener: %q", opened)
	}
	if got := strings.TrimSpace(model.State.ThreadID); got != teaThreadLinkID {
		t.Fatalf("displayed thread = %q, want %s", got, teaThreadLinkID)
	}
}

// TestThreadLinkFrameNeverReachesTerminalLikeRust: the frame keeps the label
// but drops the OSC 8 target so the terminal cannot launch the desktop app.
func TestThreadLinkFrameNeverReachesTerminalLikeRust(t *testing.T) {
	model, _ := threadLinkModel(t, Options{}, "codex://threads/"+teaThreadLinkID)
	frame := model.ViewContent()
	if strings.Contains(frame, "codex://threads/") {
		t.Fatalf("frame handed the thread link to the terminal:\n%q", frame)
	}
	if !strings.Contains(codextui.StripOSC8(frame), "Review notes") {
		t.Fatalf("frame dropped the link label:\n%q", frame)
	}
	if region := model.transcriptSelectionRegion; len(region.links) == 0 {
		t.Fatal("app-side link ranges disappeared from the painted transcript")
	}
}

// TestThreadLinkClickOnCurrentConversationLikeRust: following a link back to the
// displayed conversation reports it instead of switching.
func TestThreadLinkClickOnCurrentConversationLikeRust(t *testing.T) {
	linkTarget := "codex://threads/" + teaThreadLinkID
	model, switched := threadLinkModel(t, Options{}, linkTarget)
	model.State.SetThreadID(teaThreadLinkID)
	cmd := clickThreadLink(t, model, "Review notes")
	if cmd != nil {
		t.Fatal("clicking the current conversation started a switch")
	}
	if len(*switched) != 0 {
		t.Fatalf("switch handler ran for the current conversation: %#v", *switched)
	}
	if !strings.Contains(model.notice, "Already showing") {
		t.Fatalf("notice = %q", model.notice)
	}
}

// TestThreadLinkClickOpensArchivedConversationReadOnlyLikeRust: an archived
// target still opens, frozen for reading (Rust show_read_only_thread).
func TestThreadLinkClickOpensArchivedConversationReadOnlyLikeRust(t *testing.T) {
	options := Options{}
	options.OnSwitchAgent = nil
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	state.AddMessage(codextui.RoleAssistant, "Open [Review notes](codex://threads/"+teaArchivedThreadLinkID+") for the earlier work.")
	model := NewModel(state, options)
	model.archivedAgentThreads = map[string]bool{teaArchivedThreadLinkID: true}
	_ = model.viewString()
	cmd := clickThreadLink(t, model, "Review notes")
	runTeaCmdTree(t, model, cmd)
	if got := strings.TrimSpace(model.State.ThreadID); got != teaArchivedThreadLinkID {
		t.Fatalf("displayed thread = %q, want %s", got, teaArchivedThreadLinkID)
	}
	if !model.archivedView() {
		t.Fatal("archived conversation did not open in the frozen read-only view")
	}
}
