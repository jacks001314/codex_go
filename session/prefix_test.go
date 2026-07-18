package session

import "testing"

func TestPrefixForSessionID(t *testing.T) {
	if got := PrefixForSessionID("thread-1234567890"); got != "12345678" {
		t.Fatalf("PrefixForSessionID() = %q", got)
	}
	if got := PrefixForSessionID("session-abcd"); got != "abcd" {
		t.Fatalf("PrefixForSessionID(short) = %q", got)
	}
}
