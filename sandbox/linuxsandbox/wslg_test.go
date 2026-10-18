//go:build linux

package linuxsandbox

import (
	"reflect"
	"strings"
	"testing"
)

func TestEnsureNoWSLGDistroAliasLikeRust(t *testing.T) {
	for _, path := range []string{
		"/mnt/wslg/distro",
		"/mnt/wslg/distro/home/user/project",
	} {
		if err := ensureNoWSLGDistroAlias(path); err == nil || !strings.Contains(err.Error(), "primary filesystem root") {
			t.Fatalf("ensureNoWSLGDistroAlias(%q) = %v", path, err)
		}
	}
	for _, path := range []string{"", "/tmp", "/mnt/wslg", "/mnt/wslg/distro-other"} {
		if err := ensureNoWSLGDistroAlias(path); err != nil {
			t.Fatalf("ensureNoWSLGDistroAlias(%q) error = %v", path, err)
		}
	}
}

func TestWSLGMaskArgsOrderingLikeRust(t *testing.T) {
	want := []string{"--perms", "000", "--tmpfs", "/mnt/wslg/distro", "--remount-ro", "/mnt/wslg/distro", "--tmpfs", "/proc"}
	if got := wslgMaskArgs(false, true, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("wslgMaskArgs(no proc) = %#v, want %#v", got, want)
	}
	if got := wslgMaskArgs(false, false, false); len(got) != 6 {
		t.Fatalf("wslgMaskArgs(with proc) = %#v", got)
	}
	if got := wslgMaskArgs(true, true, false); got != nil {
		t.Fatalf("wslgMaskArgs(masked ancestor) = %#v, want nil", got)
	}
	if got := wslgMaskArgs(false, true, true); len(got) != 6 {
		t.Fatalf("wslgMaskArgs(interop already masks proc) = %#v", got)
	}
}
