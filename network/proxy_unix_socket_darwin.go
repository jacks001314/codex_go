//go:build darwin

package network

import (
	"context"
	"net"
	"net/http"
)

func proxyUnixSocketSupported() bool {
	return true
}

func proxyUnixSocketRoundTrip(request *http.Request, socketPath string) (*http.Response, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()
	return transport.RoundTrip(request)
}
