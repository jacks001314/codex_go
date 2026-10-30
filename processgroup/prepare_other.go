//go:build !unix

package processgroup

import osexec "os/exec"

// PrepareChildSession is a no-op outside Unix. Rust's NewSession and
// parent-death settings are Unix-only; Windows pipe children stay under the
// platform containment already applied by their launcher.
func PrepareChildSession(*osexec.Cmd) {}
