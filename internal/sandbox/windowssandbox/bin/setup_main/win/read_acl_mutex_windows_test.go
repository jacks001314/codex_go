//go:build windows

package win

import "testing"

func TestAcquireReadACLMutex(t *testing.T) {
	guard, acquired, err := AcquireReadACLMutex()
	if err != nil {
		t.Fatalf("AcquireReadACLMutex() error = %v", err)
	}
	if !acquired || guard == nil {
		t.Fatalf("AcquireReadACLMutex() = guard %#v acquired %v", guard, acquired)
	}
	exists, err := ReadACLMutexExists()
	if err != nil {
		t.Fatalf("ReadACLMutexExists() error = %v", err)
	}
	if !exists {
		t.Fatalf("ReadACLMutexExists() = false, want true")
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("guard.Close() error = %v", err)
	}
}
