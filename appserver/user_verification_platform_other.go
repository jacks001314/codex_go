//go:build !darwin

package appserver

// newUserVerificationPlatformProvider selects Rust's
// unsupported::UnsupportedProvider for every non-Darwin build. A macOS build
// without cgo is covered by user_verification_macos_nocgo.go, which keeps the
// two implementations from colliding on darwin && !cgo.
func newUserVerificationPlatformProvider() UserVerificationProvider {
	return UnsupportedUserVerificationProvider{}
}
