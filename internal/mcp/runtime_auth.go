package mcp

import (
	"net/http"

	"codex_go/internal/auth"
	"codex_go/internal/model"
)

func RuntimeAuthFromSnapshot(snapshot *auth.AuthDotJSON) *RuntimeAuth {
	if snapshot == nil || !RuntimeAuthUsesCodexBackend(snapshot.Mode()) {
		return nil
	}
	resolved, err := model.AuthHeadersFromAuth(*snapshot)
	if err != nil {
		return nil
	}
	headers := map[string]string{}
	for name, values := range resolved.Headers {
		if len(values) > 0 {
			headers[name] = values[len(values)-1]
		}
	}
	runtimeAuth := &RuntimeAuth{
		UsesCodexBackend: true,
		HTTPHeaders:      headers,
	}
	if resolved.SignRequest != nil {
		runtimeAuth.ApplyHTTPRequest = func(request *http.Request, body []byte) error {
			return resolved.Apply(request.Context(), request, body)
		}
	}
	return runtimeAuth
}
