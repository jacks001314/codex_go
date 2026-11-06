package tea

import (
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func newApprovalPromptDelayTestModel(t *testing.T, now *time.Time) *Model {
	t.Helper()
	animations := false
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, AnimationsEnabled: &animations})
	model.now = func() time.Time { return *now }
	return model
}

func updateApprovalPromptDelayTestModel(t *testing.T, model *Model, msg bubbletea.Msg) bubbletea.Cmd {
	t.Helper()
	next, cmd := model.Update(msg)
	got, ok := next.(*Model)
	if !ok || got != model {
		t.Fatalf("Update returned %T, want the same *Model", next)
	}
	return cmd
}

func TestApprovalPromptShowsImmediatelyWithoutRecentComposerActivity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	model := newApprovalPromptDelayTestModel(t, &now)

	updateApprovalPromptDelayTestModel(t, model, ApprovalRequestMsg{ID: "immediate", Title: "Run?"})

	if model.modal == nil || model.modal.id != "immediate" {
		t.Fatalf("modal = %#v, want immediate approval", model.modal)
	}
	if len(model.pendingApprovals) != 0 {
		t.Fatalf("pending approvals = %#v, want none", model.pendingApprovals)
	}
}

func TestApprovalPromptWaitsForComposerIdleDelay(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	model := newApprovalPromptDelayTestModel(t, &now)

	updateApprovalPromptDelayTestModel(t, model, keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'x'}})
	if got := model.approvalPromptDelay.lastComposerActivityAt; !got.Equal(base) {
		t.Fatalf("last composer activity = %v, want %v", got, base)
	}

	cmd := updateApprovalPromptDelayTestModel(t, model, ApprovalRequestMsg{ID: "delayed", Title: "Run?"})
	if model.modal != nil {
		t.Fatalf("modal = %#v, want approval to wait for composer idle", model.modal)
	}
	if len(model.pendingApprovals) != 1 || model.pendingApprovals[0].ID != "delayed" {
		t.Fatalf("pending approvals = %#v", model.pendingApprovals)
	}
	if cmd == nil {
		t.Fatal("approval delay did not schedule a timer")
	}

	now = base.Add(500 * time.Millisecond)
	updateApprovalPromptDelayTestModel(t, model, approvalPromptDelayMsg{token: model.approvalPromptDelay.token})
	if model.modal != nil {
		t.Fatalf("modal = %#v, want no modal before the idle deadline", model.modal)
	}

	now = base.Add(approvalPromptTypingIdleDelay)
	updateApprovalPromptDelayTestModel(t, model, approvalPromptDelayMsg{token: model.approvalPromptDelay.token})
	if model.modal == nil || model.modal.id != "delayed" {
		t.Fatalf("modal = %#v, want delayed approval after idle deadline", model.modal)
	}
	if len(model.pendingApprovals) != 0 {
		t.Fatalf("pending approvals = %#v, want none", model.pendingApprovals)
	}
}

func TestApprovalPromptDelayResetsWhenTypingContinues(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	model := newApprovalPromptDelayTestModel(t, &now)

	updateApprovalPromptDelayTestModel(t, model, keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'x'}})
	updateApprovalPromptDelayTestModel(t, model, ApprovalRequestMsg{ID: "reset", Title: "Run?"})

	now = base.Add(750 * time.Millisecond)
	updateApprovalPromptDelayTestModel(t, model, keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'y'}})

	now = base.Add(approvalPromptTypingIdleDelay)
	updateApprovalPromptDelayTestModel(t, model, approvalPromptDelayMsg{token: model.approvalPromptDelay.token})
	if model.modal != nil {
		t.Fatalf("modal = %#v, want continued typing to extend the deadline", model.modal)
	}

	now = base.Add(750*time.Millisecond + approvalPromptTypingIdleDelay)
	updateApprovalPromptDelayTestModel(t, model, approvalPromptDelayMsg{token: model.approvalPromptDelay.token})
	if model.modal == nil || model.modal.id != "reset" {
		t.Fatalf("modal = %#v, want approval after the reset deadline", model.modal)
	}
}

func TestApprovalPromptDelayIgnoresInactiveThreadRequests(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	model := newApprovalPromptDelayTestModel(t, &now)

	updateApprovalPromptDelayTestModel(t, model, keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'x'}})
	cmd := updateApprovalPromptDelayTestModel(t, model, ApprovalRequestMsg{ID: "background", ThreadID: "another-thread", Title: "Run?"})

	if model.modal != nil {
		t.Fatalf("modal = %#v, want background approval to stay queued", model.modal)
	}
	if cmd != nil {
		t.Fatal("inactive-thread approval scheduled the active composer delay")
	}
	if len(model.pendingApprovals) != 1 {
		t.Fatalf("pending approvals = %#v", model.pendingApprovals)
	}
}

func TestStreamDeliveredApprovalUsesComposerIdleDelay(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	model := newApprovalPromptDelayTestModel(t, &now)

	updateApprovalPromptDelayTestModel(t, model, keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'x'}})
	cmd := updateApprovalPromptDelayTestModel(t, model, streamEnvelopeMsg{
		Batch: []bubbletea.Msg{ApprovalRequestMsg{ID: "streamed-approval", Title: "Run?"}},
		Done:  true,
	})

	if model.modal != nil {
		t.Fatalf("modal = %#v, want stream-delivered approval to wait for composer idle", model.modal)
	}
	if len(model.pendingApprovals) != 1 || model.pendingApprovals[0].ID != "streamed-approval" {
		t.Fatalf("pending approvals = %#v", model.pendingApprovals)
	}
	if cmd == nil {
		t.Fatal("stream-delivered approval did not schedule the idle timer")
	}

	now = base.Add(approvalPromptTypingIdleDelay)
	updateApprovalPromptDelayTestModel(t, model, approvalPromptDelayMsg{token: model.approvalPromptDelay.token})
	if model.modal == nil || model.modal.id != "streamed-approval" {
		t.Fatalf("modal = %#v, want stream-delivered approval after idle deadline", model.modal)
	}
}

func TestWaitForStreamCoalescesAgentTextDeltas(t *testing.T) {
	messages := make(chan bubbletea.Msg, 3)
	messages <- ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "a")}
	messages <- ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "b")}
	messages <- ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "c")}
	close(messages)

	envelope, ok := waitForStream(messages)().(streamEnvelopeMsg)
	if !ok {
		t.Fatalf("waitForStream returned %T, want streamEnvelopeMsg", envelope)
	}
	if len(envelope.Batch) != 1 {
		t.Fatalf("batch length = %d, want one coalesced delta", len(envelope.Batch))
	}
	event, ok := envelope.Batch[0].(ThreadEventMsg)
	if !ok || event.Event.Delta == nil {
		t.Fatalf("batch message = %#v, want ThreadEventMsg delta", envelope.Batch[0])
	}
	if got := event.Event.Delta.Text; got != "abc" {
		t.Fatalf("coalesced text = %q, want abc", got)
	}
	if !envelope.Done {
		t.Fatal("coalesced envelope should observe the closed stream")
	}
}

func TestWaitForStreamStopsCoalescingAtNonDeltaMessage(t *testing.T) {
	messages := make(chan bubbletea.Msg, 3)
	messages <- ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "a")}
	messages <- TurnCompletedMsg{ThreadID: "thread-1"}
	messages <- ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "b")}
	close(messages)

	first, ok := waitForStream(messages)().(streamEnvelopeMsg)
	if !ok {
		t.Fatalf("first waitForStream returned %T", first)
	}
	if len(first.Batch) != 2 {
		t.Fatalf("first batch length = %d, want merged delta plus turn boundary", len(first.Batch))
	}
	if _, ok := first.Batch[1].(TurnCompletedMsg); !ok {
		t.Fatalf("first batch boundary = %T, want TurnCompletedMsg", first.Batch[1])
	}
	if first.Done {
		t.Fatal("first envelope must leave the trailing delta for the next wait")
	}

	second, ok := waitForStream(messages)().(streamEnvelopeMsg)
	if !ok {
		t.Fatalf("second waitForStream returned %T", second)
	}
	if len(second.Batch) != 1 {
		t.Fatalf("second batch length = %d, want trailing delta", len(second.Batch))
	}
	event, ok := second.Batch[0].(ThreadEventMsg)
	if !ok || event.Event.Delta == nil || event.Event.Delta.Text != "b" {
		t.Fatalf("second batch message = %#v, want trailing delta b", second.Batch[0])
	}
	if !second.Done {
		t.Fatal("second envelope should observe the closed stream")
	}
}

func TestApprovalDelayTracksOnlyComposerEditingKeys(t *testing.T) {
	tests := []struct {
		name string
		msg  keymsg.KeyMsg
		want bool
	}{
		{name: "rune", msg: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}}, want: true},
		{name: "space", msg: keymsg.KeyMsg{Type: keymsg.KeySpace}, want: true},
		{name: "backspace", msg: keymsg.KeyMsg{Type: keymsg.KeyBackspace}, want: true},
		{name: "delete", msg: keymsg.KeyMsg{Type: keymsg.KeyDelete}, want: true},
		{name: "enter", msg: keymsg.KeyMsg{Type: keymsg.KeyEnter}, want: true},
		{name: "tab", msg: keymsg.KeyMsg{Type: keymsg.KeyTab}, want: true},
		{name: "shift tab", msg: keymsg.KeyMsg{Type: keymsg.KeyShiftTab}, want: true},
		{name: "alt rune", msg: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}, Alt: true}, want: false},
		{name: "paste", msg: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'a'}, Paste: true}, want: false},
		{name: "ctrl c", msg: keymsg.KeyMsg{Type: keymsg.KeyCtrlC}, want: false},
		{name: "up", msg: keymsg.KeyMsg{Type: keymsg.KeyUp}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recordsComposerApprovalDelayActivity(test.msg); got != test.want {
				t.Fatalf("recordsComposerApprovalDelayActivity(%v) = %t, want %t", test.msg, got, test.want)
			}
		})
	}
}
