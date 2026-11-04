//go:build !linux && !android

package execserver

func hardenRegistrationBearerRead() error { return nil }
