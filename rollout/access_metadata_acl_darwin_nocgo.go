//go:build darwin && !cgo

package rollout

import "errors"

// Darwin ACLs are opaque kernel objects and are not exposed by the Go standard
// library. Fail before publication instead of silently dropping an ACL.
func copyPlatformRolloutACL(string, string) error {
	return errors.New("preserving macOS rollout ACLs requires cgo")
}
