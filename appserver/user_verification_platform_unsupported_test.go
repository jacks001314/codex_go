//go:build !darwin || !cgo

package appserver

import "testing"

// Pin the complementary build path without calling any platform APIs.
func TestUserVerificationUnsupportedPlatformProviderSelection(t *testing.T) {
	for name, provider := range map[string]UserVerificationProvider{
		"factory":              newUserVerificationPlatformProvider(),
		"nil service fallback": (&userVerificationService{}).platformProvider(),
	} {
		if _, ok := provider.(UnsupportedUserVerificationProvider); !ok {
			t.Errorf("%s provider = %T, want UnsupportedUserVerificationProvider", name, provider)
		}
	}
}
