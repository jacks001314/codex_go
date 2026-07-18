package network

import (
	"net/http"
	"testing"
)

func TestNewHTTPClientCanDisableSystemProxy(t *testing.T) {
	client := NewHTTPClient(false, 0)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("Proxy is set, want nil")
	}
}

func TestNewHTTPClientCanRespectSystemProxy(t *testing.T) {
	client := NewHTTPClient(true, 0)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T", client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("Proxy is nil, want ProxyFromEnvironment")
	}
}
