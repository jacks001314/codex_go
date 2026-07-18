//go:build !windows

package execserver

import "testing"

func requireExecServerTTYOutput(t *testing.T) {
	t.Helper()
}
