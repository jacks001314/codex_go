//go:build windows

package elevated

import "testing"

func TestRunnerStartupInfoHidesBusyFeedbackLikeRust(t *testing.T) {
	info := runnerStartupInfo()
	if info.Flags&startForceOffFeedback == 0 {
		t.Fatalf("startup flags = 0x%X, want STARTF_FORCEOFFFEEDBACK", info.Flags)
	}
	if info.Cb == 0 {
		t.Fatalf("startup cb = %d, want sizeof(STARTUPINFOW)", info.Cb)
	}
}
