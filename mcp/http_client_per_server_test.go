package mcp

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type mcpStaticHTTPDoer string

func (d mcpStaticHTTPDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(string(d))),
	}, nil
}

func TestMCPPerServerHTTPClientOverridesSharedAndRefreshes(t *testing.T) {
	server := ServerConfig{URL: "https://executor.example/mcp", Enabled: true, HTTPClient: mcpStaticHTTPDoer("first")}
	runtimeConfig := &RuntimeConfig{Servers: map[string]ServerRegistration{
		"executor": {Name: "executor", Config: server, Source: "config", CredentialPolicy: CredentialPolicyExecutorOnly},
	}}
	service := NewMCPService(runtimeConfig)
	read := func(client *httpClient) string {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	if got := read(service.httpClientForServer("executor", &server)); got != "first" {
		t.Fatalf("first executor response = %q", got)
	}

	server.HTTPClient = mcpStaticHTTPDoer("second")
	runtimeConfig.Servers["executor"] = ServerRegistration{Name: "executor", Config: server, Source: "config", CredentialPolicy: CredentialPolicyExecutorOnly}
	service.ApplyRuntimeConfig(runtimeConfig)
	if got := read(service.httpClientForServer("executor", &server)); got != "second" {
		t.Fatalf("refreshed executor response = %q", got)
	}
}
