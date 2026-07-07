package codexapi

import (
	"errors"
	"net/http"
)

var (
	ErrAuthBuild     = errors.New("request auth build error")
	ErrAuthTransient = errors.New("transient auth error")
)

type AuthProvider interface {
	AddAuthHeaders(headers http.Header)
	ApplyAuth(request Request) (Request, error)
}

type HeaderAuthProvider struct {
	Headers http.Header
}

func (p *HeaderAuthProvider) AddAuthHeaders(headers http.Header) {
	if p == nil {
		return
	}
	for key, values := range p.Headers {
		for _, value := range values {
			headers.Add(key, value)
		}
	}
}

func (p *HeaderAuthProvider) ApplyAuth(request Request) (Request, error) {
	if request.Headers == nil {
		request.Headers = http.Header{}
	}
	p.AddAuthHeaders(request.Headers)
	return request, nil
}

type AgentIdentityTelemetry struct {
	AgentID string `json:"agentId"`
	TaskID  string `json:"taskId"`
}

type AuthHeaderTelemetry struct {
	Attached bool   `json:"attached"`
	Name     string `json:"name,omitempty"`
}

func AuthHeaderTelemetryFor(auth AuthProvider) AuthHeaderTelemetry {
	if auth == nil {
		return AuthHeaderTelemetry{}
	}
	headers := http.Header{}
	auth.AddAuthHeaders(headers)
	if headers.Get("Authorization") != "" {
		return AuthHeaderTelemetry{Attached: true, Name: "authorization"}
	}
	return AuthHeaderTelemetry{}
}
