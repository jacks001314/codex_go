//go:build linux

package linuxsandbox

import (
	"strings"
	"testing"
)

func TestValidateDaemonSocketMountIsolationLikeRust(t *testing.T) {
	directory := "/tmp/codex-daemon-1000"
	cases := []struct {
		name     string
		mount    string
		wantFail bool
	}{
		{"ordinary root", "22 1 8:1 / / rw - ext4 /dev/sda1 rw", false},
		{"nested mount", "30 22 8:2 / /tmp/codex-daemon-1000/nested rw - tmpfs tmpfs rw", true},
		{"bind alias", "31 22 8:2 /tmp/codex-daemon-1000 /mnt/evil rw - ext4 /dev/sda2 rw", true},
		{"wslg view", "32 22 0:40 / /mnt/wslg/distro rw - overlay overlay rw", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDaemonSocketMountIsolation(directory, []byte(tc.mount+"\n"))
			if tc.wantFail && err == nil {
				t.Fatalf("mount %q was accepted", tc.mount)
			}
			if !tc.wantFail && err != nil {
				t.Fatalf("mount %q error = %v", tc.mount, err)
			}
			if tc.wantFail && !strings.Contains(err.Error(), "unsupported host mount") {
				t.Fatalf("mount %q error = %v", tc.mount, err)
			}
		})
	}
}

func TestValidateDaemonSocketMountIsolationRejectsMalformedMountinfo(t *testing.T) {
	if err := validateDaemonSocketMountIsolation("/tmp/codex-daemon-1000", []byte("broken line\n")); err == nil {
		t.Fatal("malformed mountinfo was accepted")
	}
}

// TestValidateDaemonSocketMountIsolationNamespaceMountsLikeRust mirrors Rust
// #46535: namespace mounts carry non-path roots such as mnt:[inode] or
// net:[inode], so only the destination decides whether they matter. Their
// destinations are still validated, and a non-path root on the socket's own
// filesystem, a relative destination and a malformed escape all reject startup.
func TestValidateDaemonSocketMountIsolationNamespaceMountsLikeRust(t *testing.T) {
	directory := "/tmp/codex-daemon-1000"
	cases := []struct {
		name     string
		mount    string
		wantFail bool
	}{
		{"unrelated mount namespace", "2 1 0:2 mnt:[4026532835] /run/snapd/ns/example.mnt rw - nsfs nsfs rw", false},
		{"unrelated network namespace", "2 1 0:2 net:[4026531840] /run/netns/example rw - nsfs nsfs rw", false},
		{"nested namespace mount", "2 1 0:2 mnt:[4026532835] /tmp/codex-daemon-1000/ns rw - nsfs nsfs rw", true},
		{"relative destination", "2 1 0:2 mnt:[4026532835] relative/ns rw - nsfs nsfs rw", true},
		{"invalid destination escape", `2 1 0:2 mnt:[4026532835] /run/snapd/ns/\invalid rw - nsfs nsfs rw`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mounts := "1 0 0:1 / / rw - ext4 disk rw\n" + tc.mount + "\n"
			err := validateDaemonSocketMountIsolation(directory, []byte(mounts))
			if tc.wantFail && err == nil {
				t.Fatalf("mount %q was accepted", tc.mount)
			}
			if !tc.wantFail && err != nil {
				t.Fatalf("mount %q error = %v", tc.mount, err)
			}
		})
	}
}

// TestValidateDaemonSocketMountIsolationNamespaceMountKeepsAliasChecksLikeRust
// pins that an unrelated namespace mount does not hide a real socket alias.
func TestValidateDaemonSocketMountIsolationNamespaceMountKeepsAliasChecksLikeRust(t *testing.T) {
	mounts := "1 0 0:1 / / rw - ext4 disk rw\n" +
		"2 1 0:2 net:[4026531840] /run/netns/example rw - nsfs nsfs rw\n" +
		"3 1 0:1 /tmp/codex-daemon-1000 /alias rw - ext4 disk rw\n"
	err := validateDaemonSocketMountIsolation("/tmp/codex-daemon-1000", []byte(mounts))
	if err == nil {
		t.Fatal("socket alias behind an unrelated namespace mount was accepted")
	}
	if !strings.Contains(err.Error(), "unsupported host mount") {
		t.Fatalf("alias error = %v, want the unsupported-mount reason", err)
	}
}
