package network

import (
	"net/http"
	"time"
)

func NewHTTPClient(respectSystemProxy bool, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !respectSystemProxy {
		transport.Proxy = nil
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
