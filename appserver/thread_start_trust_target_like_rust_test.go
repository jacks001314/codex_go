package appserver

import "testing"

// A projectless working directory must remain the trust target; walking up the
// filesystem must not fall back to the test process's checkout.
func TestThreadStartTrustTargetProjectlessUsesCWDLikeRust(t *testing.T) {
	workspace := t.TempDir()
	if got := threadStartTrustTarget(workspace); !sameAppPath(got, workspace) {
		t.Fatalf("threadStartTrustTarget(%q) = %q, want the workspace", workspace, got)
	}
}
