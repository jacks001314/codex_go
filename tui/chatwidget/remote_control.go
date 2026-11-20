package chatwidget

import (
	"strconv"
	"strings"

	"codex_go/appserver"
	"codex_go/remotecontrol"
)

// Rust parity: codex-rs/tui/src/app/remote_control.rs. Remote control configures
// the whole local background server; device pairing is a separate action.

const (
	// RemoteControlMenuViewID identifies the `/rc` menu.
	RemoteControlMenuViewID = "remote-control-menu"
	// RemoteControlPairingViewID identifies the manual pairing code view.
	RemoteControlPairingViewID = "remote-control-pairing"

	// The item ids double as the action the menu row performs.
	RemoteControlMenuItemEnable    = "enable"
	RemoteControlMenuItemDisable   = "disable"
	RemoteControlMenuItemAddDevice = "add-device"
	RemoteControlMenuItemDone      = "done"
)

// RemoteControlAction is one remote control operation.
type RemoteControlAction string

const (
	RemoteControlActionMenu      RemoteControlAction = "menu"
	RemoteControlActionEnable    RemoteControlAction = "enable"
	RemoteControlActionDisable   RemoteControlAction = "disable"
	RemoteControlActionAddDevice RemoteControlAction = "add_device"
)

// Rust's fixed guidance strings. The `/rc` command name is deliberate: the
// messages point the user back at the command that re-reads the state.
const (
	remoteControlMenuTitle              = "Remote control"
	remoteControlAppliesToLocalSessions = "Applies to local sessions; cloud sessions are unaffected."
	remoteControlRetryDisableSubtitle   = "Remote control is off now, but may return after restart. Choose Disable to save the preference."
	remoteControlAddDeviceRow           = "Add device"
	remoteControlAddDeviceDisabled      = "Enable remote control first"
	remoteControlDisabledInfo           = "Remote control is disabled for local sessions. Cloud sessions are unaffected."
	remoteControlManagedRequirements    = "Remote control is disabled by managed requirements."
	remoteControlSignInWithChatGPT      = "Sign in with ChatGPT to manage remote control."
	remoteControlStatusReadFailed       = "Could not read remote control status."
	remoteControlAddDeviceFailed        = "Could not add device. Try again."
	remoteControlNoManualCode           = "No manual pairing code was returned."
	remoteControlMenuTimeout            = "Remote control status timed out. Try again."
	remoteControlOperationTimeout       = "The remote control operation timed out. Its outcome is unknown; reopen /rc to check the state."
	remoteControlPreferenceUnsaved      = "Remote control changed for the running daemon, but the preference could not be saved. It may revert after restart."
)

// RemoteControlMenuConfig is the connection state the menu reports. Available
// is false when no verified local background server backs this session, which
// leaves the single row disabled and shows Unavailable as the subtitle.
type RemoteControlMenuConfig struct {
	Available   bool
	Status      remotecontrol.ConnectionStatus
	Saved       *bool
	Unavailable string
}

// RemoteControlMenuView builds the `/rc` menu (Rust remote_control::menu).
func RemoteControlMenuView(config RemoteControlMenuConfig) SelectionView {
	enabled := config.Available && config.Status != remotecontrol.StatusDisabled
	retryDisable := !enabled && config.Available && (config.Saved == nil || *config.Saved)
	name := "Enable"
	action := RemoteControlMenuItemEnable
	switch {
	case enabled || retryDisable:
		name = "Disable"
		action = RemoteControlMenuItemDisable
	case !config.Available:
		// Rust marks the unavailable row "Enable (disabled)".
		name = "Enable (disabled)"
	}
	subtitle := remoteControlAppliesToLocalSessions
	switch {
	case !config.Available:
		subtitle = config.Unavailable
	case retryDisable:
		subtitle = remoteControlRetryDisableSubtitle
	}
	items := []SelectionItem{{
		ID:              action,
		Name:            name,
		Disabled:        !config.Available,
		DismissOnSelect: true,
	}}
	// Rust renders Add device only once a daemon answers, and explains the
	// prerequisite without disabling the row.
	if config.Available {
		reason := ""
		if !enabled {
			reason = remoteControlAddDeviceDisabled
		}
		items = append(items, SelectionItem{
			ID:              RemoteControlMenuItemAddDevice,
			Name:            remoteControlAddDeviceRow,
			DisabledReason:  reason,
			DismissOnSelect: true,
		})
	}
	return SelectionView{
		ViewID:      RemoteControlMenuViewID,
		Title:       remoteControlMenuTitle,
		Subtitle:    subtitle,
		AllowCancel: true,
		Items:       items,
	}
}

// RemoteControlPairingView shows the manual pairing code (Rust
// remote_control::pairing).
func RemoteControlPairingView(code string) SelectionView {
	return SelectionView{
		ViewID:      RemoteControlPairingViewID,
		Title:       "Add device",
		Subtitle:    "Enter this pairing code on your other device: " + code + ". If it expires, select Add device again.",
		AllowCancel: true,
		Items: []SelectionItem{{
			ID:              RemoteControlMenuItemDone,
			Name:            "Done",
			DismissOnSelect: true,
		}},
	}
}

// RemoteControlItemAction maps a selected menu row back to its action.
func RemoteControlItemAction(id string) (RemoteControlAction, bool) {
	switch id {
	case RemoteControlMenuItemEnable:
		return RemoteControlActionEnable, true
	case RemoteControlMenuItemDisable:
		return RemoteControlActionDisable, true
	case RemoteControlMenuItemAddDevice:
		return RemoteControlActionAddDevice, true
	default:
		return "", false
	}
}

// RemoteControlOutcomeKind is the shape of what the command shows.
type RemoteControlOutcomeKind string

const (
	RemoteControlOutcomeMenu        RemoteControlOutcomeKind = "menu"
	RemoteControlOutcomeUnavailable RemoteControlOutcomeKind = "unavailable"
	RemoteControlOutcomePairing     RemoteControlOutcomeKind = "pairing"
	RemoteControlOutcomeInfo        RemoteControlOutcomeKind = "info"
	RemoteControlOutcomeError       RemoteControlOutcomeKind = "error"
)

// RemoteControlOutcome is what one operation reports back to the TUI. Message
// carries the reason, pairing code, info text or error text depending on Kind.
type RemoteControlOutcome struct {
	Kind    RemoteControlOutcomeKind
	Status  remotecontrol.ConnectionStatus
	Saved   *bool
	Message string
}

// String renders the outcome for diagnostics. A pairing code, info text and
// error text are redacted so a manual pairing code never reaches event logs
// (Rust's custom Debug for Outcome).
func (o RemoteControlOutcome) String() string {
	switch o.Kind {
	case RemoteControlOutcomeMenu:
		saved := "none"
		if o.Saved != nil {
			saved = strconv.FormatBool(*o.Saved)
		}
		return "Menu(" + string(o.Status) + ", " + saved + ")"
	case RemoteControlOutcomeUnavailable:
		return "Unavailable(" + o.Message + ")"
	case RemoteControlOutcomePairing:
		return "Pairing([redacted])"
	case RemoteControlOutcomeInfo:
		return "Info([redacted])"
	case RemoteControlOutcomeError:
		return "Error([redacted])"
	default:
		return string(o.Kind)
	}
}

// RemoteControlResponse is one protocol answer the host already decoded: the
// failure flag, the server's error message when the failure came from the
// server, and the fields each request type returns.
type RemoteControlResponse struct {
	Failed        bool
	ServerMessage string
	Status        remotecontrol.ConnectionStatus
	ManualCode    *string
	// Saved is the preference persisted for the next restart, read by the host
	// alongside the answer; nil when it could not be read.
	Saved *bool
}

// RemoteControlOutcomeFor maps one answer for action to what `/rc` shows (Rust
// remote_control::run).
func RemoteControlOutcomeFor(action RemoteControlAction, response RemoteControlResponse) RemoteControlOutcome {
	if action == RemoteControlActionAddDevice {
		if response.Failed {
			return RemoteControlOutcome{Kind: RemoteControlOutcomeError, Message: remoteControlAddDeviceFailed}
		}
		if response.ManualCode == nil || strings.TrimSpace(*response.ManualCode) == "" {
			return RemoteControlOutcome{Kind: RemoteControlOutcomeError, Message: remoteControlNoManualCode}
		}
		return RemoteControlOutcome{Kind: RemoteControlOutcomePairing, Message: *response.ManualCode}
	}
	if !response.Failed {
		if action == RemoteControlActionDisable {
			return RemoteControlOutcome{Kind: RemoteControlOutcomeInfo, Message: remoteControlDisabledInfo}
		}
		return RemoteControlOutcome{Kind: RemoteControlOutcomeMenu, Status: response.Status}
	}
	if response.ServerMessage == "remote control is disabled by managed requirements" {
		if action == RemoteControlActionMenu {
			return RemoteControlOutcome{Kind: RemoteControlOutcomeUnavailable, Message: remoteControlManagedRequirements}
		}
		return RemoteControlOutcome{Kind: RemoteControlOutcomeError, Message: remoteControlManagedRequirements}
	}
	if action == RemoteControlActionMenu {
		return RemoteControlOutcome{Kind: RemoteControlOutcomeError, Message: remoteControlStatusReadFailed}
	}
	operation := "enabling"
	if action == RemoteControlActionDisable {
		operation = "disabling"
	}
	if response.ServerMessage == "remote control requires ChatGPT authentication" ||
		response.ServerMessage == "remote control requires ChatGPT authentication; API key auth is not supported" {
		return RemoteControlOutcome{Kind: RemoteControlOutcomeError, Message: remoteControlSignInWithChatGPT}
	}
	return RemoteControlOutcome{
		Kind: RemoteControlOutcomeError,
		Message: "Could not complete " + operation +
			" remote control. The running daemon may already have changed; reopen /rc to check its state.",
	}
}

// RemoteControlTimeoutOutcome is what a stalled operation reports. Its wording
// depends on whether the command was only reading state or changing it.
func RemoteControlTimeoutOutcome(action RemoteControlAction) RemoteControlOutcome {
	message := remoteControlOperationTimeout
	if action == RemoteControlActionMenu {
		message = remoteControlMenuTimeout
	}
	return RemoteControlOutcome{Kind: RemoteControlOutcomeError, Message: message}
}

// ApplyRemoteControlSavedPreference records the preference persisted for the next
// restart and turns an unconfirmed change into an error, so a user is told the
// running daemon and the saved preference disagree (Rust handle's expected check).
func ApplyRemoteControlSavedPreference(action RemoteControlAction, outcome RemoteControlOutcome, saved *bool) RemoteControlOutcome {
	expected := false
	checked := false
	switch {
	case action == RemoteControlActionEnable && outcome.Kind == RemoteControlOutcomeMenu:
		expected, checked = true, true
	case action == RemoteControlActionDisable && outcome.Kind == RemoteControlOutcomeInfo:
		expected, checked = false, true
	}
	if outcome.Kind == RemoteControlOutcomeMenu {
		outcome.Saved = saved
	}
	if checked && (saved == nil || *saved != expected) {
		return RemoteControlOutcome{Kind: RemoteControlOutcomeError, Message: remoteControlPreferenceUnsaved}
	}
	return outcome
}

// RemoteControlRequest is the protocol request one action sends. Status reads and
// mutations carry no params, so the daemon applies its own defaults; only manual
// pairing asks for a code.
type RemoteControlRequest struct {
	Method appserver.Method
	Params any
}

// RemoteControlRequestFor maps an action to its request (Rust remote_control::run).
func RemoteControlRequestFor(action RemoteControlAction) (RemoteControlRequest, bool) {
	switch action {
	case RemoteControlActionMenu:
		return RemoteControlRequest{Method: appserver.MethodRemoteControlStatusRead}, true
	case RemoteControlActionEnable:
		return RemoteControlRequest{Method: appserver.MethodRemoteControlEnable}, true
	case RemoteControlActionDisable:
		return RemoteControlRequest{Method: appserver.MethodRemoteControlDisable}, true
	case RemoteControlActionAddDevice:
		return RemoteControlRequest{
			Method: appserver.MethodRemoteControlPairingStart,
			Params: remotecontrol.PairingStartParams{ManualCode: true},
		}, true
	default:
		return RemoteControlRequest{}, false
	}
}
