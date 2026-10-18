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
