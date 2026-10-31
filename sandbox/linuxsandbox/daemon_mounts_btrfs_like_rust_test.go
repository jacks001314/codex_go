//go:build linux

package linuxsandbox

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/linux-sandbox/src/daemon_mounts_tests.rs at #47968.
// Btrfs subvolumes can report an st_dev that differs from the device in
// /proc/self/mountinfo. Only a verified Btrfs descriptor may authorize the
// mismatch, and aliases stay subject to the usual mount checks.

func TestDaemonMountsAllowVerifiedBtrfsDeviceMismatchLikeRust(t *testing.T) {
	mounts := "1 0 0:1 / / rw - btrfs disk rw\n"
	if err := validateDaemonSocketMountIsolation(
		"/tmp/codex-daemon-1000",
		"0:2",
		daemonSocketFilesystemBtrfs,
		"1",
		[]byte(mounts),
		"",
	); err != nil {
		t.Fatalf("verified btrfs device mismatch error = %v", err)
	}
}

func TestDaemonMountsRejectUnverifiedBtrfsDeviceMismatchLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		filesystem  daemonSocketFilesystem
		mountSystem string
		mountID     string
	}{
		{"unverified descriptor filesystem", daemonSocketFilesystemOther, "btrfs", "1"},
		{"inconsistent mount filesystem", daemonSocketFilesystemBtrfs, "ext4", "1"},
		{"unavailable mount id", daemonSocketFilesystemBtrfs, "btrfs", ""},
		{"missing mount id", daemonSocketFilesystemBtrfs, "btrfs", "missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mounts := "1 0 0:1 / / rw - " + tc.mountSystem + " disk rw\n"
			err := validateDaemonSocketMountIsolation(
				"/tmp/codex-daemon-1000",
				"0:2",
				tc.filesystem,
				tc.mountID,
				[]byte(mounts),
				"",
			)
			if err == nil {
				t.Fatalf("mountinfo %q was accepted", mounts)
			}
		})
	}
}

func TestDaemonMountsRejectBtrfsAliasesUnderBothDevicesLikeRust(t *testing.T) {
	mounts := "1 0 0:1 /@ / rw - btrfs disk rw\n" +
		"2 1 0:1 /@/tmp /tmp rw - btrfs disk rw\n" +
		"3 1 0:1 /@/tmp /mount-device-alias rw - btrfs disk rw\n" +
		"4 1 0:2 /@/tmp /stat-device-alias rw - btrfs disk rw\n"
	err := validateDaemonSocketMountIsolation(
		"/tmp/codex-daemon-1000",
		"0:2",
		daemonSocketFilesystemBtrfs,
		"2",
		[]byte(mounts),
		"",
	)
	if err == nil {
		t.Fatal("btrfs aliases under both device identities were accepted")
	}
	if !strings.Contains(err.Error(), "unsupported host mount") {
		t.Fatalf("alias error = %v, want the unsupported-mount reason", err)
	}
}

func TestDaemonMountsRejectBtrfsUnsafeMountsLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		device      string
		root        string
		destination string
	}{
		{"mount device directory alias", "0:1", "/@/tmp/codex-daemon-1000", "/alias"},
		{"stat device socket alias", "0:2", "/@/tmp/codex-daemon-1000/rpc.sock", "/alias.sock"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mounts := "1 0 0:1 /@ / rw - btrfs disk rw\n" +
				"2 1 " + tc.device + " " + tc.root + " " + tc.destination + " rw - btrfs disk rw\n"
			err := validateDaemonSocketMountIsolation(
				"/tmp/codex-daemon-1000",
				"0:2",
				daemonSocketFilesystemBtrfs,
				"1",
				[]byte(mounts),
				"",
			)
			if err == nil {
				t.Fatalf("unsafe mount %q was accepted", tc.destination)
			}
		})
	}
}

func TestDaemonMountsRejectDuplicateBtrfsMountIDLikeRust(t *testing.T) {
	mounts := "1 0 0:1 /@ / rw - btrfs disk rw\n" +
		"1 0 0:1 /@ / rw - btrfs disk rw\n"
	err := validateDaemonSocketMountIsolation(
		"/tmp/codex-daemon-1000",
		"0:2",
		daemonSocketFilesystemBtrfs,
		"1",
		[]byte(mounts),
		"",
	)
	if err == nil {
		t.Fatal("duplicate mount ID was accepted")
	}
}
