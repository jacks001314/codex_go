package linuxsandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWSLInteropMaskArgsLikeRust(t *testing.T) {
	interop := filepath.Join(t.TempDir(), "WSL")
	if err := os.Mkdir(interop, 0o755); err != nil {
		t.Fatalf("Mkdir(WSL) error = %v", err)
	}
	got := appendWSLInteropMaskArgs(nil, false, true, interop)
	want := []string{"--tmpfs", interop}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mask args = %#v, want %#v", got, want)
	}
	noProcArgs := appendWSLInteropMaskArgs(nil, true, true, interop)
	if len(noProcArgs) != 4 || !reflect.DeepEqual(noProcArgs[2:], []string{"--tmpfs", "/proc"}) {
		t.Fatalf("no-proc mask args = %#v", noProcArgs)
	}
	if got := appendWSLInteropMaskArgs(nil, false, false, interop); len(got) != 0 {
		t.Fatalf("unrestricted filesystem mask args = %#v", got)
	}
	if got := appendWSLInteropMaskArgs(nil, false, true, filepath.Join(t.TempDir(), "missing")); len(got) != 0 {
		t.Fatalf("missing interop dir mask args = %#v", got)
	}
}
