//go:build linux

package linuxsandbox

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/linux-sandbox/src/daemon_mounts_tests.rs at #46125.
// The opened socket directory's mount ID selects the real containing mount, so
// private tmp mounts and stacked mounts are accepted while covered mounts,
// exposed aliases and nested mounts keep rejecting sandbox startup.

// checkDaemonMountsLikeRust mirrors Rust's test-only wrapper: most cases have
// no independently masked subtree.
func checkDaemonMountsLikeRust(t *testing.T, directory string, device string, mountID string, mountinfo string, allow bool) {
	t.Helper()
	err := validateDaemonSocketMountIsolation(directory, device, daemonSocketFilesystemOther, mountID, []byte(mountinfo), "")
	if allow && err != nil {
		t.Fatalf("mountinfo %q (device %s, mount id %q) error = %v", mountinfo, device, mountID, err)
	}
	if !allow && err == nil {
		t.Fatalf("mountinfo %q (device %s, mount id %q) was accepted", mountinfo, device, mountID)
	}
}

// TestDaemonMountsRejectOnlyCompromisingMountsLikeRust mirrors Rust's
// `rejects_only_mounts_that_compromise_the_directory` table. Every layout is
// checked twice: with the visible containing mount ID and without any ID.
func TestDaemonMountsRejectOnlyCompromisingMountsLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		root        string
		destination string
		allow       bool
	}{
		{"ancestor alias", "/tmp", "/host-tmp", false},
		{"directory alias", "/tmp/codex-daemon-1000", "/alias", false},
		{"socket alias", "/tmp/codex-daemon-1000/rpc.sock", "/alias.sock", false},
		{"root alias", "/", "/host", false},
		{"unrelated project bind", "/workspace", "/project", true},
		{"same location", "/tmp", "/tmp", true},
		{"escaped alias", "/tmp", `/host\040tmp`, false},
		{"nested mount", "/other", "/tmp/codex-daemon-1000/nested", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mounts := "1 0 0:1 / / rw - ext4 disk rw\n2 1 0:1 " + tc.root + " " + tc.destination + " rw - ext4 disk rw\n"
			visibleMount := "1"
			if tc.destination == "/tmp" {
				visibleMount = "2"
			}
			for _, mountID := range []string{visibleMount, ""} {
				checkDaemonMountsLikeRust(t, "/tmp/codex-daemon-1000", "0:1", mountID, mounts, tc.allow)
			}
		})
	}
}

// TestDaemonMountsRejectAliasWhenTmpIsBindMountLikeRust pins that a hidden
// deeper mount cannot override the actual /tmp backing location.
func TestDaemonMountsRejectAliasWhenTmpIsBindMountLikeRust(t *testing.T) {
	mounts := "1 0 0:1 / / rw - ext4 disk rw\n2 1 0:1 /backing/tmp /tmp rw - ext4 disk rw\n"
	checkDaemonMountsLikeRust(t, "/tmp/codex-daemon-1000", "0:1", "2", mounts, false)
	hidden := mounts + "3 1 0:1 /tmp/codex-daemon-1000 /tmp/codex-daemon-1000 rw - ext4 disk rw\n"
	checkDaemonMountsLikeRust(t, "/tmp/codex-daemon-1000", "0:1", "2", hidden, false)
}

// TestDaemonMountsAcceptPrivateTmpAndResolveStackedLikeRust covers a private
// tmpfs at /tmp and a stacked tmpfs over it, plus the missing and inconsistent
// mount IDs that must not fall back to the conservative interpretation.
func TestDaemonMountsAcceptPrivateTmpAndResolveStackedLikeRust(t *testing.T) {
	directory := "/tmp/codex-daemon-1000"
	mounts := "1 0 0:1 / / rw - ext4 disk rw\n2 1 0:2 / /tmp rw - tmpfs tmpfs rw\n"
	checkDaemonMountsLikeRust(t, directory, "0:2", "2", mounts, true)
	checkDaemonMountsLikeRust(t, directory, "0:2", "", mounts, true)
	checkDaemonMountsLikeRust(t, directory, "0:2", "missing", mounts, false)
	checkDaemonMountsLikeRust(t, directory, "0:2", "1", mounts, false)
	checkDaemonMountsLikeRust(t, directory, "0:3", "", mounts, false)
	stacked := mounts + "3 2 0:2 /other /tmp rw - tmpfs tmpfs rw\n"
	checkDaemonMountsLikeRust(t, directory, "0:2", "3", stacked, true)
	checkDaemonMountsLikeRust(t, directory, "0:2", "", stacked, false)
}

// TestDaemonMountsRejectCoveredOpenMountLikeRust pins that a mount covered by
// a newer one on the same path rejects startup, while the visible mount and an
// alias of a hidden backing path stay accepted or rejected as Rust decides.
func TestDaemonMountsRejectCoveredOpenMountLikeRust(t *testing.T) {
	directory := "/tmp/codex-daemon-1000"
	mounts := "1 0 0:1 / / rw - ext4 disk rw\n" +
		"2 1 0:1 /tmp/private-old/tmp /tmp rw - ext4 disk rw\n" +
		"3 2 0:1 /tmp/private-new/tmp /tmp rw - ext4 disk rw\n"
	checkDaemonMountsLikeRust(t, directory, "0:1", "2", mounts, false)
	checkDaemonMountsLikeRust(t, directory, "0:1", "3", mounts, true)
	exposed := mounts + "4 1 0:1 /tmp/private-new/tmp/codex-daemon-1000 /outside rw - ext4 disk rw\n"
	for _, mountID := range []string{"2", "3", ""} {
		checkDaemonMountsLikeRust(t, directory, "0:1", mountID, exposed, false)
	}
}

// TestDaemonMountsRejectMountHiddenByAncestorOvermountLikeRust covers an
// ancestor overmount that hides the selected branch.
func TestDaemonMountsRejectMountHiddenByAncestorOvermountLikeRust(t *testing.T) {
	directory := "/tmp/private/codex-daemon-1000"
	mounts := "1 0 0:1 / / rw - ext4 disk rw\n" +
		"2 1 0:2 / /tmp rw - tmpfs tmpfs rw\n" +
		"3 2 0:3 / /tmp/private rw - tmpfs tmpfs rw\n"
	checkDaemonMountsLikeRust(t, directory, "0:3", "3", mounts, true)
	covered := mounts + "4 2 0:2 /other /tmp rw - tmpfs tmpfs rw\n"
	checkDaemonMountsLikeRust(t, directory, "0:3", "3", covered, false)
	checkDaemonMountsLikeRust(t, directory, "0:2", "4", covered, true)
}

// TestDaemonMountsAcceptPrivateTmpBindRejectExposedAliasesLikeRust mirrors
// systemd PrivateTmp: the bind is safe with a precise mount ID, ambiguous
// without one, and never hides a real alias beneath /tmp.
func TestDaemonMountsAcceptPrivateTmpBindRejectExposedAliasesLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		root   string
		parent string
	}{
		{"tmp on root filesystem", "/tmp/systemd-private-service/tmp", "/"},
		{"tmp on separate filesystem", "/systemd-private-service/tmp", "/tmp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mounts := "1 0 0:1 / " + tc.parent + " rw - ext4 disk rw\n" +
				"2 1 0:1 " + tc.root + " /tmp rw - ext4 disk rw\n"
			checkDaemonMountsLikeRust(t, "/tmp/codex-daemon-1000", "0:1", "2", mounts, true)
			checkDaemonMountsLikeRust(t, "/tmp/codex-daemon-1000", "0:1", "", mounts, false)
			aliases := []struct {
				root        string
				destination string
			}{
				{tc.root, "/tmp/exposed"},
				{tc.root + "/codex-daemon-1000/rpc.sock", "/tmp/alias.sock"},
				{"/", "/host"},
			}
			for _, alias := range aliases {
				exposed := mounts + "3 2 0:1 " + alias.root + " " + alias.destination + " rw - ext4 disk rw\n"
				checkDaemonMountsLikeRust(t, "/tmp/codex-daemon-1000", "0:1", "2", exposed, false)
			}
		})
	}
}

// TestDaemonMountsMaskedWSLGAliasKeepsOtherAliasesLikeRust pins that the WSLg
// duplicate view is only tolerated when it is actually masked.
func TestDaemonMountsMaskedWSLGAliasKeepsOtherAliasesLikeRust(t *testing.T) {
	mounts := "1 0 0:1 / / rw - ext4 disk rw\n2 1 0:1 / /mnt/wslg/distro rw - ext4 disk rw\n"
	exposed := mounts + "3 1 0:1 /tmp /host-tmp rw - ext4 disk rw\n"
	directory := "/tmp/codex-daemon-1000"
	for _, mountID := range []string{"1", ""} {
		if err := validateDaemonSocketMountIsolation(directory, "0:1", daemonSocketFilesystemOther, mountID, []byte(mounts), ""); err == nil {
			t.Fatalf("unmasked WSLg alias (mount id %q) was accepted", mountID)
		}
		if err := validateDaemonSocketMountIsolation(directory, "0:1", daemonSocketFilesystemOther, mountID, []byte(mounts), wslgDistroRoot); err != nil {
			t.Fatalf("masked WSLg alias (mount id %q) error = %v", mountID, err)
		}
		err := validateDaemonSocketMountIsolation(directory, "0:1", daemonSocketFilesystemOther, mountID, []byte(exposed), wslgDistroRoot)
		if err == nil || !strings.Contains(err.Error(), "unsupported host mount") {
			t.Fatalf("exposed alias beside WSLg (mount id %q) error = %v", mountID, err)
		}
	}
}
