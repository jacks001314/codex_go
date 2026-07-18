//go:build windows

package windowssandbox

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestProcThreadAttributeListSetHandleList(t *testing.T) {
	attrs, err := NewProcThreadAttributeListWithCount(1)
	if err != nil {
		t.Fatalf("NewProcThreadAttributeListWithCount() error = %v", err)
	}
	defer attrs.Close()
	if attrs.WindowsList() == nil {
		t.Fatalf("WindowsList() = nil")
	}
	if err := attrs.SetHandleList([]uintptr{uintptr(windows.CurrentProcess())}); err != nil {
		t.Fatalf("SetHandleList() error = %v", err)
	}
}

func TestProcThreadAttributeListRejectsZeroCount(t *testing.T) {
	if _, err := NewProcThreadAttributeListWithCount(0); err == nil {
		t.Fatalf("NewProcThreadAttributeListWithCount(0) error = nil, want error")
	}
}
