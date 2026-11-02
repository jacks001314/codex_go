//go:build !linux

package tool

import "testing"

// prepareSnapshotSandboxHelper is a no-op where the capture does not run
// through the Linux sandbox helper.
func prepareSnapshotSandboxHelper(t *testing.T) {}
