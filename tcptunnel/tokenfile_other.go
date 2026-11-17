//go:build !unix

package tcptunnel

import "os"

// tokenFilePrivacyProblem is a Unix-only requirement in Rust.
func tokenFilePrivacyProblem(os.FileMode) string {
	return ""
}
