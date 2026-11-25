//go:build darwin

package tui

import (
	"reflect"
	"testing"
)

func TestDarwinComposerUndoLeavesCtrlZForSuspend(t *testing.T) {
	if got, want := defaultComposerUndoBindings(), []string{"alt-z", "super-z", "f7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Darwin undo = %v, want %v without Windows Ctrl-Z", got, want)
	}
	if got, want := defaultComposerRedoBindings(), []string{"alt-shift-z", "shift-super-z", "shift-f7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Darwin redo = %v, want %v", got, want)
	}
}
