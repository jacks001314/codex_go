package plugin

import (
	"context"
	"net/http"
)

// RuntimeAuthProvider supplies per-request runtime credentials for remote plugin
// requests when runtime agent instances are enabled (Rust #59564).
type RuntimeAuthProvider interface {
	HeadersForDestination(ctx context.Context, destination string) (http.Header, error)
}

// ApplyRuntimeAuth overlays runtime request headers onto a prepared remote
// plugin request. Providers return no headers for destinations that must keep
// their own authentication.
func ApplyRuntimeAuth(ctx context.Context, request *http.Request, provider RuntimeAuthProvider) error {
	if request == nil || provider == nil {
		return nil
	}
	headers, err := provider.HeadersForDestination(ctx, request.URL.String())
	if err != nil {
		return err
	}
	for name, values := range headers {
		request.Header.Del(name)
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	return nil
}
