package conpty

import "testing"

func TestInstanceTakeHandles(t *testing.T) {
	instance := &Instance{pseudoConsole: 7, inputWrite: 11, outputRead: 13}
	if got := instance.RawHandle(); got != 7 {
		t.Fatalf("RawHandle() = %d, want 7", got)
	}
	if got := instance.TakeInputWrite(); got != 11 {
		t.Fatalf("TakeInputWrite() = %d, want 11", got)
	}
	if got := instance.TakeInputWrite(); got != 0 {
		t.Fatalf("second TakeInputWrite() = %d, want 0", got)
	}
	if got := instance.TakeOutputRead(); got != 13 {
		t.Fatalf("TakeOutputRead() = %d, want 13", got)
	}
	if got := instance.TakeOutputRead(); got != 0 {
		t.Fatalf("second TakeOutputRead() = %d, want 0", got)
	}
}
