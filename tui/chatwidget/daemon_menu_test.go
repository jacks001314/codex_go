package chatwidget

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// TestDaemonMenuViewLikeRust pins the `/daemon` menu Rust renders
// (App::open_daemon_menu): the two sources, the disabled guidance, and the
// "use this CLI build" package requirement.
func TestDaemonMenuViewLikeRust(t *testing.T) {
	view := DaemonMenuView(DaemonMenuConfig{
		LocalDaemon:   true,
		DaemonVersion: "1.2.3",
		CLIExecutable: "/usr/local/bin/codex",
		HasPackage:    true,
	})
	if view.ViewID != DaemonMenuViewID || view.Title != "Daemon" {
		t.Fatalf("view = %#v", view)
	}
	header := strings.Join(view.HeaderLines, "\n")
	if !strings.Contains(header, "Daemon") || !strings.Contains(header, "Running daemon: 1.2.3") {
		t.Fatalf("header = %q", header)
	}
	if len(view.Items) != 2 ||
		view.Items[0].ID != DaemonMenuItemPublicStable || view.Items[1].ID != DaemonMenuItemThisCli {
		t.Fatalf("items = %#v", view.Items)
	}
	for _, item := range view.Items {
		if item.Disabled || item.DisabledReason != "" {
			t.Fatalf("item %q disabled = %v (%q), want enabled", item.ID, item.Disabled, item.DisabledReason)
		}
	}

	// Without a local daemon the status falls back to the disconnected text.
	disconnected := DaemonMenuView(DaemonMenuConfig{CLIExecutable: "/usr/local/bin/codex"})
	if got := disconnected.HeaderLines[1]; got != daemonMenuStatusDisconnected {
		t.Fatalf("status = %q", got)
	}

	// An explicit remote server cannot be managed here.
	remote := DaemonMenuView(DaemonMenuConfig{Remote: true, CLIExecutable: "/usr/local/bin/codex"})
	if !strings.Contains(strings.Join(remote.HeaderLines, "\n"), daemonMenuUnavailableRemote) {
		t.Fatalf("remote header = %#v", remote.HeaderLines)
	}
	for _, item := range remote.Items {
		if !item.Disabled {
			t.Fatalf("remote item %q is enabled", item.ID)
		}
	}

	// Without a launching CLI the menu explains how to get one.
	noCLI := DaemonMenuView(DaemonMenuConfig{})
	if !strings.Contains(strings.Join(noCLI.HeaderLines, "\n"), daemonMenuUnavailableNoCLI) {
		t.Fatalf("no-cli header = %#v", noCLI.HeaderLines)
	}

	// A CLI without a complete package cannot copy its build, but the public
	// stable source stays available.
	noPackage := DaemonMenuView(DaemonMenuConfig{CLIExecutable: "/usr/local/bin/codex"})
	if noPackage.Items[0].Disabled || noPackage.Items[0].DisabledReason != "" {
		t.Fatalf("public stable item = %#v", noPackage.Items[0])
	}
	if !noPackage.Items[1].Disabled || noPackage.Items[1].DisabledReason != daemonMenuDisabledNoPackage {
		t.Fatalf("this-cli item = %#v", noPackage.Items[1])
	}
}

// TestDaemonConfirmViewLikeRust pins the confirmation copy and items Rust
// renders in App::confirm_daemon_update.
func TestDaemonConfirmViewLikeRust(t *testing.T) {
	publicStable := DaemonConfirmView(codextui.DaemonUpdateSourcePublicStable, "9.9.9", "/usr/local/bin/codex")
	if publicStable.ViewID != DaemonConfirmViewID || publicStable.Title != "Update daemon and exit Codex?" {
		t.Fatalf("view = %#v", publicStable)
	}
	if !strings.Contains(publicStable.Subtitle, daemonMenuPublicStableExplanation) ||
		!strings.Contains(publicStable.Subtitle, "This may restart the daemon") {
		t.Fatalf("subtitle = %q", publicStable.Subtitle)
	}

	thisCli := DaemonConfirmView(codextui.DaemonUpdateSourceThisCli, "1.2.3", "/usr/local/bin/codex")
	if !strings.Contains(thisCli.Subtitle, "Use this CLI package v1.2.3 from /usr/local/bin/codex.") ||
		!strings.Contains(thisCli.Subtitle, "pin it against automatic updates") {
		t.Fatalf("this-cli subtitle = %q", thisCli.Subtitle)
	}

	if len(publicStable.Items) != 2 ||
		publicStable.Items[0].ID != DaemonMenuItemCancel ||
		publicStable.Items[1].ID != DaemonMenuItemUpdateAndExit {
		t.Fatalf("items = %#v", publicStable.Items)
	}
	if !publicStable.Items[1].RequireExplicitConfirmation {
		t.Fatal("the update item must require explicit confirmation")
	}
}

// TestDaemonUpdateSourceCommandArgsLikeRust pins the CLI arguments each source
// relaunches with (Rust DaemonUpdateSource::command_args).
func TestDaemonUpdateSourceCommandArgsLikeRust(t *testing.T) {
	if got := codextui.DaemonUpdateSourcePublicStable.CommandArgs(); len(got) != 3 ||
		got[0] != "app-server" || got[1] != "daemon" || got[2] != "update" {
		t.Fatalf("public stable args = %#v", got)
	}
	want := []string{"app-server", "daemon", "update", "--from-cli", "--yes"}
	if got := codextui.DaemonUpdateSourceThisCli.CommandArgs(); len(got) != len(want) {
		t.Fatalf("this-cli args = %#v", got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("this-cli args = %#v", got)
			}
		}
	}
	if action := codextui.DaemonUpdateAction(codextui.DaemonUpdateSourceThisCli); action != codextui.UpdateActionDaemonThisCli {
		t.Fatalf("action = %q", action)
	}
	if source, ok := codextui.UpdateActionDaemonPublicStable.DaemonUpdateSourceOf(); !ok || source != codextui.DaemonUpdateSourcePublicStable {
		t.Fatalf("source = %q ok=%v", source, ok)
	}
}
