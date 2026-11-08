//go:build darwin && !cgo

package appserver

// newUserVerificationPlatformProvider falls back to the unsupported provider
// when cgo is disabled: Security.framework and LocalAuthentication are only
// reachable through the cgo backend.
func newUserVerificationPlatformProvider() UserVerificationProvider {
	return UnsupportedUserVerificationProvider{}
}
