//go:build unix

package tcptunnel

import "os"

// tokenFilePrivacyProblem mirrors Rust's my Unix permission check: group and
// other bits must be clear.
func tokenFilePrivacyProblem(mode os.FileMode) string {
	if mode.Perm()&0o077 != 0 {
		return "token file must be private"
	}
	return ""
}
