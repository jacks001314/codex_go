//go:build !windows

package network

import "errors"

var errProxyAuthUnsupported = errors.New("Windows proxy authentication is unavailable on this platform")

func proxyAuthSupported() bool { return false }

func newProxyAuthSession(scheme string, proxyHost string) (proxyAuthSession, error) {
	return nil, errProxyAuthUnsupported
}
