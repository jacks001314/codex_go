//go:build windows

package windowssandbox

import "testing"

func TestPreparedSandboxCleanupRefusesAMissingGuard(t *testing.T) {
	var prepared *PreparedSandboxCleanup
	if err := prepared.Finish(nil, nil, nil); err == nil || err.Error() != "missing prepared sandbox cleanup" {
		t.Fatalf("Finish(nil guard) error = %v", err)
	}
	prepared.Release()

	empty := &PreparedSandboxCleanup{}
	if err := empty.Finish(nil, nil, nil); err == nil || err.Error() != "missing prepared sandbox cleanup" {
		t.Fatalf("Finish(released guard) error = %v", err)
	}
}
