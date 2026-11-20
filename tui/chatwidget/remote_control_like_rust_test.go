package chatwidget

import (
	"testing"

	"codex_go/appserver"
	"codex_go/remotecontrol"
)

func remoteControlBool(value bool) *bool {
	return &value
}

// TestRemoteControlMenuStatesLikeRust mirrors Rust remote_control::menu: the row
// name and action follow the live status, a stale enabled preference offers
// Disable again, Add device appears only once a daemon answers, and an
// unavailable session keeps a single disabled row.
func TestRemoteControlMenuStatesLikeRust(t *testing.T) {
	const unavailable = "Remote control requires the local background server"

	cases := []struct {
		name         string
		config       RemoteControlMenuConfig
		wantRow      string
		wantAction   string
		wantSubtitle string
		wantAddRow   bool
		wantAddNote  string
		wantDisabled bool
	}{
		{
			name:         "no daemon",
			config:       RemoteControlMenuConfig{Unavailable: unavailable},
			wantRow:      "Enable (disabled)",
			wantAction:   RemoteControlMenuItemEnable,
			wantSubtitle: unavailable,
			wantDisabled: true,
		},
		{
			name:         "disabled and saved off",
			config:       RemoteControlMenuConfig{Available: true, Status: remotecontrol.StatusDisabled, Saved: remoteControlBool(false)},
			wantRow:      "Enable",
			wantAction:   RemoteControlMenuItemEnable,
			wantSubtitle: remoteControlAppliesToLocalSessions,
			wantAddRow:   true,
			wantAddNote:  remoteControlAddDeviceDisabled,
		},
		{
			name:         "connected",
			config:       RemoteControlMenuConfig{Available: true, Status: remotecontrol.StatusConnected, Saved: remoteControlBool(true)},
			wantRow:      "Disable",
			wantAction:   RemoteControlMenuItemDisable,
			wantSubtitle: remoteControlAppliesToLocalSessions,
			wantAddRow:   true,
		},
		{
			name:         "disabled but saved on",
			config:       RemoteControlMenuConfig{Available: true, Status: remotecontrol.StatusDisabled, Saved: remoteControlBool(true)},
			wantRow:      "Disable",
			wantAction:   RemoteControlMenuItemDisable,
			wantSubtitle: remoteControlRetryDisableSubtitle,
			wantAddRow:   true,
			wantAddNote:  remoteControlAddDeviceDisabled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := RemoteControlMenuView(tc.config)
			if view.ViewID != RemoteControlMenuViewID || view.Title != remoteControlMenuTitle {
				t.Fatalf("view = %#v", view)
			}
			if view.Subtitle != tc.wantSubtitle {
				t.Fatalf("subtitle = %q, want %q", view.Subtitle, tc.wantSubtitle)
			}
			if len(view.Items) == 0 || view.Items[0].Name != tc.wantRow || view.Items[0].ID != tc.wantAction {
				t.Fatalf("first row = %#v, want %q / %q", view.Items[0], tc.wantRow, tc.wantAction)
			}
			if view.Items[0].Disabled != tc.wantDisabled {
				t.Fatalf("first row disabled = %v, want %v", view.Items[0].Disabled, tc.wantDisabled)
			}
			if !tc.wantAddRow {
				if len(view.Items) != 1 {
					t.Fatalf("rows = %#v, want the single unavailable row", view.Items)
				}
				return
			}
			if len(view.Items) != 2 || view.Items[1].ID != RemoteControlMenuItemAddDevice || view.Items[1].Name != remoteControlAddDeviceRow {
				t.Fatalf("rows = %#v, want an Add device row", view.Items)
			}
			if view.Items[1].DisabledReason != tc.wantAddNote {
				t.Fatalf("add-device note = %q, want %q", view.Items[1].DisabledReason, tc.wantAddNote)
			}
		})
	}
}

// TestRemoteControlPairingViewLikeRust pins the manual code view: the code is
// shown inline with the retry hint, and Done is the only row.
func TestRemoteControlPairingViewLikeRust(t *testing.T) {
	view := RemoteControlPairingView("ABCD-EFGH")
	if view.ViewID != RemoteControlPairingViewID || view.Title != "Add device" {
		t.Fatalf("view = %#v", view)
	}
	want := "Enter this pairing code on your other device: ABCD-EFGH. If it expires, select Add device again."
	if view.Subtitle != want {
		t.Fatalf("subtitle = %q, want %q", view.Subtitle, want)
	}
	if len(view.Items) != 1 || view.Items[0].Name != "Done" || !view.Items[0].DismissOnSelect {
		t.Fatalf("rows = %#v, want a dismissing Done row", view.Items)
	}
}

// TestRemoteControlOutcomesAreRedactedLikeRust keeps a manual pairing code, info
// text and error text out of anything that stringifies an outcome.
func TestRemoteControlOutcomesAreRedactedLikeRust(t *testing.T) {
	if got := (RemoteControlOutcome{Kind: RemoteControlOutcomePairing, Message: "ABCD-EFGH"}).String(); got != "Pairing([redacted])" {
		t.Fatalf("pairing outcome = %q", got)
	}
	if got := (RemoteControlOutcome{Kind: RemoteControlOutcomeInfo, Message: remoteControlDisabledInfo}).String(); got != "Info([redacted])" {
		t.Fatalf("info outcome = %q", got)
	}
	if got := (RemoteControlOutcome{Kind: RemoteControlOutcomeError, Message: "boom"}).String(); got != "Error([redacted])" {
		t.Fatalf("error outcome = %q", got)
	}
	menu := RemoteControlOutcome{Kind: RemoteControlOutcomeMenu, Status: remotecontrol.StatusConnected, Saved: remoteControlBool(true)}
	if got := menu.String(); got != "Menu(connected, true)" {
		t.Fatalf("menu outcome = %q", got)
	}
	unavailable := RemoteControlOutcome{Kind: RemoteControlOutcomeUnavailable, Message: remoteControlManagedRequirements}
	if got := unavailable.String(); got != "Unavailable("+remoteControlManagedRequirements+")" {
		t.Fatalf("unavailable outcome = %q", got)
	}
}

// TestRemoteControlOutcomeMappingLikeRust covers the answer mapping for each
// action, including the daemon refusals that get their own wording.
func TestRemoteControlOutcomeMappingLikeRust(t *testing.T) {
	code := "ABCD-EFGH"
	cases := []struct {
		name     string
		action   RemoteControlAction
		response RemoteControlResponse
		wantKind RemoteControlOutcomeKind
		want     string
	}{
		{"status", RemoteControlActionMenu, RemoteControlResponse{Status: remotecontrol.StatusConnected}, RemoteControlOutcomeMenu, ""},
		{"enable", RemoteControlActionEnable, RemoteControlResponse{Status: remotecontrol.StatusConnected}, RemoteControlOutcomeMenu, ""},
		{"disable", RemoteControlActionDisable, RemoteControlResponse{Status: remotecontrol.StatusDisabled}, RemoteControlOutcomeInfo, remoteControlDisabledInfo},
		{"pairing", RemoteControlActionAddDevice, RemoteControlResponse{ManualCode: &code}, RemoteControlOutcomePairing, code},
		{"pairing without code", RemoteControlActionAddDevice, RemoteControlResponse{}, RemoteControlOutcomeError, remoteControlNoManualCode},
		{"pairing failure", RemoteControlActionAddDevice, RemoteControlResponse{Failed: true}, RemoteControlOutcomeError, remoteControlAddDeviceFailed},
		{"status refused by requirements", RemoteControlActionMenu, RemoteControlResponse{Failed: true, ServerMessage: "remote control is disabled by managed requirements"}, RemoteControlOutcomeUnavailable, remoteControlManagedRequirements},
		{"enable refused by requirements", RemoteControlActionEnable, RemoteControlResponse{Failed: true, ServerMessage: "remote control is disabled by managed requirements"}, RemoteControlOutcomeError, remoteControlManagedRequirements},
		{"enable needs chatgpt auth", RemoteControlActionEnable, RemoteControlResponse{Failed: true, ServerMessage: "remote control requires ChatGPT authentication"}, RemoteControlOutcomeError, remoteControlSignInWithChatGPT},
		{"disable needs chatgpt auth", RemoteControlActionDisable, RemoteControlResponse{Failed: true, ServerMessage: "remote control requires ChatGPT authentication; API key auth is not supported"}, RemoteControlOutcomeError, remoteControlSignInWithChatGPT},
		{"status read failure", RemoteControlActionMenu, RemoteControlResponse{Failed: true}, RemoteControlOutcomeError, remoteControlStatusReadFailed},
		{"enable failure", RemoteControlActionEnable, RemoteControlResponse{Failed: true}, RemoteControlOutcomeError, "Could not complete enabling remote control. The running daemon may already have changed; reopen /rc to check its state."},
		{"disable failure", RemoteControlActionDisable, RemoteControlResponse{Failed: true}, RemoteControlOutcomeError, "Could not complete disabling remote control. The running daemon may already have changed; reopen /rc to check its state."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome := RemoteControlOutcomeFor(tc.action, tc.response)
			if outcome.Kind != tc.wantKind || outcome.Message != tc.want {
				t.Fatalf("outcome = %#v, want %q / %q", outcome, tc.wantKind, tc.want)
			}
		})
	}
}

// TestRemoteControlTimeoutsLikeRust keeps the read-only and mutating timeout
// messages distinct, because a stalled mutation has an unknown outcome.
func TestRemoteControlTimeoutsLikeRust(t *testing.T) {
	menu := RemoteControlTimeoutOutcome(RemoteControlActionMenu)
	if menu.Kind != RemoteControlOutcomeError || menu.Message != "Remote control status timed out. Try again." {
		t.Fatalf("menu timeout = %#v", menu)
	}
	for _, action := range []RemoteControlAction{RemoteControlActionEnable, RemoteControlActionDisable, RemoteControlActionAddDevice} {
		outcome := RemoteControlTimeoutOutcome(action)
		if outcome.Kind != RemoteControlOutcomeError || outcome.Message != remoteControlOperationTimeout {
			t.Fatalf("%s timeout = %#v", action, outcome)
		}
	}
}

// TestRemoteControlSavedPreferenceLikeRust proves an unconfirmed change is
// reported, and that a menu result carries the persisted preference.
func TestRemoteControlSavedPreferenceLikeRust(t *testing.T) {
	menu := RemoteControlOutcome{Kind: RemoteControlOutcomeMenu, Status: remotecontrol.StatusConnected}
	saved := ApplyRemoteControlSavedPreference(RemoteControlActionMenu, menu, remoteControlBool(true))
	if saved.Kind != RemoteControlOutcomeMenu || saved.Saved == nil || !*saved.Saved {
		t.Fatalf("menu outcome = %#v, want the saved preference attached", saved)
	}

	enabled := ApplyRemoteControlSavedPreference(RemoteControlActionEnable, menu, remoteControlBool(false))
	if enabled.Kind != RemoteControlOutcomeError || enabled.Message != remoteControlPreferenceUnsaved {
		t.Fatalf("unconfirmed enable = %#v", enabled)
	}
	confirmed := ApplyRemoteControlSavedPreference(RemoteControlActionEnable, menu, remoteControlBool(true))
	if confirmed.Kind != RemoteControlOutcomeMenu {
		t.Fatalf("confirmed enable = %#v", confirmed)
	}

	info := RemoteControlOutcome{Kind: RemoteControlOutcomeInfo, Message: remoteControlDisabledInfo}
	if outcome := ApplyRemoteControlSavedPreference(RemoteControlActionDisable, info, remoteControlBool(true)); outcome.Message != remoteControlPreferenceUnsaved {
		t.Fatalf("unconfirmed disable = %#v", outcome)
	}
	if outcome := ApplyRemoteControlSavedPreference(RemoteControlActionDisable, info, nil); outcome.Message != remoteControlPreferenceUnsaved {
		t.Fatalf("unreadable preference = %#v", outcome)
	}
	if outcome := ApplyRemoteControlSavedPreference(RemoteControlActionDisable, info, remoteControlBool(false)); outcome.Kind != RemoteControlOutcomeInfo {
		t.Fatalf("confirmed disable = %#v", outcome)
	}
}

// TestRemoteControlRequestsLikeRust pins the daemon calls `/rc` makes: status
// reads and mutations carry no params, and manual pairing asks for a code.
func TestRemoteControlRequestsLikeRust(t *testing.T) {
	for action, want := range map[RemoteControlAction]appserver.Method{
		RemoteControlActionMenu:    appserver.MethodRemoteControlStatusRead,
		RemoteControlActionEnable:  appserver.MethodRemoteControlEnable,
		RemoteControlActionDisable: appserver.MethodRemoteControlDisable,
	} {
		request, ok := RemoteControlRequestFor(action)
		if !ok || request.Method != want || request.Params != nil {
			t.Fatalf("%s request = %#v, want %q without params", action, request, want)
		}
	}
	pairing, ok := RemoteControlRequestFor(RemoteControlActionAddDevice)
	if !ok || pairing.Method != appserver.MethodRemoteControlPairingStart {
		t.Fatalf("pairing request = %#v", pairing)
	}
	params, ok := pairing.Params.(remotecontrol.PairingStartParams)
	if !ok || !params.ManualCode {
		t.Fatalf("pairing params = %#v, want manual_code", pairing.Params)
	}
	if _, ok := RemoteControlRequestFor("unknown"); ok {
		t.Fatal("unknown action produced a request")
	}
}

// TestRemoteControlItemActionsLikeRust maps the menu rows to their actions.
func TestRemoteControlItemActionsLikeRust(t *testing.T) {
	for id, want := range map[string]RemoteControlAction{
		RemoteControlMenuItemEnable:    RemoteControlActionEnable,
		RemoteControlMenuItemDisable:   RemoteControlActionDisable,
		RemoteControlMenuItemAddDevice: RemoteControlActionAddDevice,
	} {
		if action, ok := RemoteControlItemAction(id); !ok || action != want {
			t.Fatalf("%s -> %q/%v, want %q", id, action, ok, want)
		}
	}
	if _, ok := RemoteControlItemAction(RemoteControlMenuItemDone); ok {
		t.Fatal("Done produced an action")
	}
}
