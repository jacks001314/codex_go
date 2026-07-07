//go:build windows

package windowssandbox

import "testing"

func TestAllowNullDeviceRejectsEmptySID(t *testing.T) {
	if err := AllowNullDevice(""); err == nil {
		t.Fatalf("AllowNullDevice(\"\") error = nil, want error")
	}
}
