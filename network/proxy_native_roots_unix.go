//go:build !windows && !darwin

package network

import (
	"errors"
	"os"
)

func loadProxyPlatformRootBundle() (string, error) {
	paths := []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/ca-bundle.pem",
		"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
		"/etc/ssl/cert.pem",
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err == nil && len(contents) > 0 {
			return string(contents), nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}
