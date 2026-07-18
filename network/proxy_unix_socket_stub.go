//go:build !darwin

package network

import (
	"fmt"
	"net/http"
)

func proxyUnixSocketSupported() bool {
	return false
}

func proxyUnixSocketRoundTrip(_ *http.Request, _ string) (*http.Response, error) {
	return nil, fmt.Errorf("unix sockets unsupported")
}
