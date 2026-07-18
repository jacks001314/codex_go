package auth

import (
	"strings"
	"testing"
)

func TestHeadlessChatGPTLoginStateLines(t *testing.T) {
	pending := Pending("request-1")
	if pending.IsShowingCopyableAuth() {
		t.Fatal("pending login should not show copyable auth")
	}
	if got := strings.Join(pending.Lines(), "\n"); !strings.Contains(got, "Preparing device code login") || !strings.Contains(got, "Requesting a one-time code") {
		t.Fatalf("pending lines = %q", got)
	}
	ready := Ready("request-1", "login-1", "https://example.test/device", "ABCD-EFGH")
	if !ready.IsShowingCopyableAuth() {
		t.Fatal("ready login should show copyable auth")
	}
	got := strings.Join(ready.Lines(), "\n")
	for _, want := range []string{
		"Finish signing in via your browser",
		"https://example.test/device",
		"ABCD-EFGH",
		"Never share this code",
		"Press Esc to cancel",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ready lines missing %q:\n%s", want, got)
		}
	}
}
