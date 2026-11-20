package tea

import (
	"context"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: codex-rs/tui/src/app/remote_control.rs. `/rc` manages remote
// control for the whole local background server; the actions apply only to local
// sessions, and the menu can be opened while a cloud session is displayed.

// RemoteControlRunFunc performs one `/rc` operation against the verified local
// background server and reports the decoded answer. Rust runs this through the
// daemon's app-server socket; the host supplies the call so the TUI stays free of
// process and socket handling. A nil runner leaves the menu unavailable, which is
// what Rust shows when it cannot verify the local background server.
type RemoteControlRunFunc func(ctx context.Context, action chatwidget.RemoteControlAction) (chatwidget.RemoteControlResponse, error)

const (
	// Rust bounds every remote control operation, including its status read.
	remoteControlOperationTimeout = 30 * time.Second
	// Messages the command shows when it never reaches the daemon.
	remoteControlUnavailable      = "Remote control requires the local background server"
	remoteControlPendingMessage   = "A remote control operation is already in progress. Try again when it finishes."
	remoteControlUnavailableError = "Remote control requires the local background server."
)

// remoteControlFinishedMsg carries one operation's answer back to the model.
type remoteControlFinishedMsg struct {
	action   chatwidget.RemoteControlAction
	response chatwidget.RemoteControlResponse
	failure  string
}

// openRemoteControlMenu runs the `/rc` status read, which decides the menu.
func (m *Model) openRemoteControlMenu() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	return m.startRemoteControl(chatwidget.RemoteControlActionMenu)
}

// startRemoteControl runs one guarded, time-bounded operation.
func (m *Model) startRemoteControl(action chatwidget.RemoteControlAction) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.remoteControlPending {
		m.addErrorHistoryMessage(remoteControlPendingMessage)
		return nil
	}
	run := m.remoteControlRun
	if run == nil {
		m.showRemoteControlOutcome(chatwidget.RemoteControlOutcome{
			Kind:    chatwidget.RemoteControlOutcomeUnavailable,
			Message: remoteControlUnavailable,
		})
		return nil
	}
	m.remoteControlPending = true
	return func() bubbletea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), remoteControlOperationTimeout)
		defer cancel()
		answers := make(chan remoteControlFinishedMsg, 1)
		go func() {
			response, err := run(ctx, action)
			if err != nil {
				answers <- remoteControlFinishedMsg{action: action, failure: err.Error()}
				return
			}
			answers <- remoteControlFinishedMsg{action: action, response: response}
		}()
		select {
		case finished := <-answers:
			return finished
		case <-ctx.Done():
			// A stalled mutation has an unknown outcome, so the timeout wording
			// differs from a status read.
			return remoteControlFinishedMsg{action: action, failure: timeoutFailureText(action)}
		}
	}
}

// handleRemoteControlFinished clears the guard and reports what the daemon said.
func (m *Model) handleRemoteControlFinished(msg remoteControlFinishedMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.remoteControlPending = false
	if msg.failure != "" {
		kind := chatwidget.RemoteControlOutcomeError
		if msg.action == chatwidget.RemoteControlActionMenu {
			// A menu that cannot reach the daemon reports the reason instead of
			// offering actions.
			kind = chatwidget.RemoteControlOutcomeUnavailable
		}
		m.showRemoteControlOutcome(chatwidget.RemoteControlOutcome{Kind: kind, Message: msg.failure})
		return nil
	}
	outcome := chatwidget.RemoteControlOutcomeFor(msg.action, msg.response)
	outcome = chatwidget.ApplyRemoteControlSavedPreference(msg.action, outcome, msg.response.Saved)
	m.showRemoteControlOutcome(outcome)
	return nil
}

// showRemoteControlOutcome renders one operation's answer.
func (m *Model) showRemoteControlOutcome(outcome chatwidget.RemoteControlOutcome) {
	if m == nil {
		return
	}
	switch outcome.Kind {
	case chatwidget.RemoteControlOutcomeMenu:
		m.showRemoteControlView(chatwidget.RemoteControlMenuView(chatwidget.RemoteControlMenuConfig{
			Available: true,
			Status:    outcome.Status,
			Saved:     outcome.Saved,
		}))
	case chatwidget.RemoteControlOutcomeUnavailable:
		m.showRemoteControlView(chatwidget.RemoteControlMenuView(chatwidget.RemoteControlMenuConfig{
			Unavailable: outcome.Message,
		}))
	case chatwidget.RemoteControlOutcomePairing:
		m.showRemoteControlView(chatwidget.RemoteControlPairingView(outcome.Message))
	case chatwidget.RemoteControlOutcomeInfo:
		if m.State != nil {
			m.State.AddHistoryLines([]string{outcome.Message}, []string{outcome.Message})
		}
		m.notice = outcome.Message
		m.refreshTranscript()
	case chatwidget.RemoteControlOutcomeError:
		m.addErrorHistoryMessage(outcome.Message)
	default:
		// An operation that reported nothing usable still needs to explain
		// itself, so it lands on the action's error wording.
		m.addErrorHistoryMessage(remoteControlUnavailableError)
	}
}

// showRemoteControlView opens a remote control menu as a bottom-pane picker, as
// Rust's SelectionViewParams::picker() does.
func (m *Model) showRemoteControlView(view chatwidget.SelectionView) {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, view)
	if m.modal != nil {
		m.modal.inlinePicker = true
	}
}

// applyRemoteControlMenuOption runs the action a menu row selected.
func (m *Model) applyRemoteControlMenuOption(optionID string) bubbletea.Cmd {
	action, ok := chatwidget.RemoteControlItemAction(optionID)
	if !ok {
		return nil
	}
	return m.startRemoteControl(action)
}

// timeoutFailureText renders the message a stalled operation reports.
func timeoutFailureText(action chatwidget.RemoteControlAction) string {
	return chatwidget.RemoteControlTimeoutOutcome(action).Message
}
