package codexapi

import (
	"errors"
	"net/http"
)

var (
	ErrAuthBuild     = errors.New("request auth build error")
	ErrAuthTransient = errors.New("transient auth error")
)

// AuthProvider applies authentication to outbound requests. Rust #55607 makes
// header construction fallible: a provider that cannot produce complete
// credentials must report an error so callers do not send the request
// unauthenticated. Providers that need asynchronous work must return an error
// from AddAuthHeaders and implement ApplyAuth with the resolved headers.
type AuthProvider interface {
	AddAuthHeaders(headers http.Header) error
	ApplyAuth(request Request) (Request, error)
}

type HeaderAuthProvider struct {
	Headers http.Header
}

func (p *HeaderAuthProvider) AddAuthHeaders(headers http.Header) error {
	if p == nil || headers == nil {
		return nil
	}
	for key, values := range p.Headers {
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	return nil
}

func (p *HeaderAuthProvider) ApplyAuth(request Request) (Request, error) {
	if request.Headers == nil {
		request.Headers = http.Header{}
	}
	if err := p.AddAuthHeaders(request.Headers); err != nil {
		return request, err
	}
	return request, nil
}

// ToAuthHeaders returns the headers a provider can attach without an async
// resolution step. Callers must not send a request when this fails.
func ToAuthHeaders(auth AuthProvider) (http.Header, error) {
	headers := http.Header{}
	if auth == nil {
		return headers, nil
	}
	if err := auth.AddAuthHeaders(headers); err != nil {
		return headers, err
	}
	return headers, nil
}

type AgentIdentityTelemetry struct {
	AgentID string `json:"agentId"`
	TaskID  string `json:"taskId"`
}

type AuthHeaderTelemetry struct {
	Attached bool   `json:"attached"`
	Name     string `json:"name,omitempty"`
}

func AuthHeaderTelemetryFor(auth AuthProvider) (AuthHeaderTelemetry, error) {
	if auth == nil {
		return AuthHeaderTelemetry{}, nil
	}
	headers, err := ToAuthHeaders(auth)
	if err != nil {
		return AuthHeaderTelemetry{}, err
	}
	if headers.Get("Authorization") != "" {
		return AuthHeaderTelemetry{Attached: true, Name: "authorization"}, nil
	}
	return AuthHeaderTelemetry{}, nil
}
