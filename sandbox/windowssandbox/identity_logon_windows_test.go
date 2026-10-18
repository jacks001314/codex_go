//go:build windows

package windowssandbox

import (
	"errors"
	"testing"
)

func TestLogonSandboxAccountClassifiesRejectedCredentials(t *testing.T) {
	err := logonSandboxAccount(SandboxCredentials{
		Username: "CodexSandboxAccountThatDoesNotExist",
		Password: "invalid",
		Domain:   ".",
	})
	if err == nil {
		t.Fatal("logon unexpectedly succeeded")
	}
	var mismatch *SandboxAccountCredentialMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("logon error = %v, want credential mismatch", err)
	}
}

func TestResolveSandboxAccountSID(t *testing.T) {
	sid, err := resolveSandboxAccountSID("Everyone")
	if err != nil {
		t.Fatalf("resolveSandboxAccountSID(Everyone) error = %v", err)
	}
	if sid != "S-1-1-0" {
		t.Fatalf("Everyone SID = %q, want S-1-1-0", sid)
	}
}
