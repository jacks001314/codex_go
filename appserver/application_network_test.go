package appserver

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/network"
	"codex_go/session"
	"codex_go/turn"
)

// Rust parity (#47410): remote-control enrollment and server requests honor the
// application network policy.
func TestRemoteControlBackendHonorsApplicationNetworkPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, `
[application.network]
[application.network.domains]
"allowed.example" = "allow"
"denied.example" = "deny"
`)
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(home)),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
	})
	defer router.Close()

	// An empty codex home keeps the backend free of an enrollment store, so the
	// test only exercises the server API client.
	backend := router.remoteControlManagerBackend("", &RuntimeRouterOptions{
		RemoteControlURL: "https://allowed.example/backend-api",
	})
	if backend == nil || backend.ServerAPIOptions == nil || backend.ServerAPIOptions.HTTPClient == nil {
		t.Fatalf("remote-control backend = %#v, want a policy-bound server API client", backend)
	}
	if _, err := backend.ServerAPIOptions.HTTPClient.Do(&http.Request{URL: appServerTestURL(t, "https://denied.example/backend-api/enroll")}); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("denied remote-control request error = %v", err)
	}

	// A host without managed requirements keeps the package default doer.
	plainHome := t.TempDir()
	plainRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(plainHome)),
		Config:       config.NewConfigService(plainHome),
		Turns:        turn.NewTurnService(),
	})
	defer plainRouter.Close()
	plainBackend := plainRouter.remoteControlManagerBackend("", &RuntimeRouterOptions{
		RemoteControlURL: "https://plain.example/backend-api",
	})
	if plainBackend == nil || plainBackend.ServerAPIOptions == nil {
		t.Fatalf("plain remote-control backend = %#v", plainBackend)
	}
	if plainBackend.ServerAPIOptions.HTTPClient != nil {
		t.Fatal("an unrestricted policy replaced the remote-control default doer")
	}
}

// accountDoerStub records whether an account/ChatGPT backend request was
// attempted.
type accountDoerStub struct {
	attempts int
}

func (d *accountDoerStub) Do(*http.Request) (*http.Response, error) {
	d.attempts++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
}

// Rust parity (#47703): ChatGPT backend requests (account, plugins, cloud
// config) preserve the account's application network policy.
func TestRuntimeRouterAccountClientHonorsApplicationNetworkPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, `
[application.network]
[application.network.domains]
"allowed.example" = "allow"
"denied.example" = "deny"
`)
	stub := &accountDoerStub{}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(home)),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		AccountHTTP:  stub,
	})
	defer router.Close()

	doer := router.accountHTTPClient()
	response, err := doer.Do(&http.Request{URL: appServerTestURL(t, "https://allowed.example/backend-api/wham/config")})
	if err != nil {
		t.Fatalf("allowed backend request error = %v", err)
	}
	_ = response.Body.Close()
	if stub.attempts != 1 {
		t.Fatalf("attempts = %d, want the allowed request to reach the account transport", stub.attempts)
	}
	if _, err := doer.Do(&http.Request{URL: appServerTestURL(t, "https://denied.example/backend-api/wham/config")}); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("denied backend request error = %v", err)
	}
	if stub.attempts != 1 {
		t.Fatalf("attempts = %d, want the denied request rejected before connecting", stub.attempts)
	}

	// A host without managed requirements keeps its account client unchanged.
	plainStub := &accountDoerStub{}
	plainRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       config.NewConfigService(t.TempDir()),
		Turns:        turn.NewTurnService(),
		AccountHTTP:  plainStub,
	})
	defer plainRouter.Close()
	if got, ok := plainRouter.accountHTTPClient().(*accountDoerStub); !ok || got != plainStub {
		t.Fatal("an unrestricted policy replaced the account client")
	}
}

// appServerDoerStub records whether a request was attempted at all, so a test
// can prove a denied destination never reached the transport.
type appServerDoerStub struct {
	attempts int
}

func (d *appServerDoerStub) Do(*http.Request) (*http.Response, error) {
	d.attempts++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

func appServerTestURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return parsed
}

// Rust parity: app-server/src/application_network.rs `destination_policy`.
func TestDestinationPolicyFromApplicationRequirementsLikeRust(t *testing.T) {
	allow := config.NetworkPermission("allow")
	deny := config.NetworkPermission("deny")
	tests := []struct {
		name        string
		application *config.ApplicationRequirements
		restricted  bool
		allowed     []string
		denied      []string
	}{
		{
			name: "no application requirements",
		},
		{
			name:        "no network table",
			application: &config.ApplicationRequirements{},
		},
		{
			name: "disabled network table",
			application: &config.ApplicationRequirements{Network: &config.ApplicationNetworkRequirements{
				Enabled: false,
				Domains: map[string]config.NetworkPermission{"example.com": allow},
			}},
		},
		{
			name: "enabled network table allows only allowed domains",
			application: &config.ApplicationRequirements{Network: &config.ApplicationNetworkRequirements{
				Enabled: true,
				Domains: map[string]config.NetworkPermission{
					"allowed.example": allow,
					"denied.example":  deny,
				},
			}},
			restricted: true,
			allowed:    []string{"https://allowed.example/path", "wss://allowed.example/socket"},
			denied:     []string{"https://denied.example/path", "http://allowed.example/path", "https://other.example/path"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := destinationPolicyFromApplicationRequirements(test.application)
			if policy.IsRestricted() != test.restricted {
				t.Fatalf("restricted = %v, want %v", policy.IsRestricted(), test.restricted)
			}
			for _, raw := range test.allowed {
				if !policy.Allows(appServerTestURL(t, raw)) {
					t.Fatalf("Allows(%s) = false, want true", raw)
				}
			}
			for _, raw := range test.denied {
				if policy.Allows(appServerTestURL(t, raw)) {
					t.Fatalf("Allows(%s) = true, want false", raw)
				}
			}
		})
	}
}

// The app-server publishes the policy composed from its managed requirements and
// its own transports enforce it before connecting.
func TestRuntimeRouterEnforcesApplicationNetworkPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, `
[application.network]
[application.network.domains]
"allowed.example" = "allow"
"denied.example" = "deny"
`)
	stub := &appServerDoerStub{}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(home)),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		HTTPClient:   stub,
	})
	defer router.Close()

	doer := router.httpClientForConfig(nil)
	response, err := doer.Do(&http.Request{URL: appServerTestURL(t, "https://allowed.example/v1")})
	if err != nil {
		t.Fatalf("allowed request error = %v", err)
	}
	_ = response.Body.Close()
	if stub.attempts != 1 {
		t.Fatalf("attempts = %d, want the allowed request to reach the transport", stub.attempts)
	}
	_, err = doer.Do(&http.Request{URL: appServerTestURL(t, "https://denied.example/v1")})
	if !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("denied request error = %v", err)
	}
	if stub.attempts != 1 {
		t.Fatalf("attempts = %d, want the denied request to be rejected before connecting", stub.attempts)
	}
	// The concrete client the WebSocket-dialing transports hold enforces the
	// same policy through its transport.
	concrete := router.policyHTTPClientForConfig(nil)
	if _, ok := concrete.Transport.(*network.PolicyRoundTripper); !ok {
		t.Fatalf("concrete client transport = %#v, want a policy round tripper", concrete.Transport)
	}
	if _, err := concrete.Do(&http.Request{URL: appServerTestURL(t, "https://denied.example/v1")}); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("concrete client denied request error = %v", err)
	}

	// Without the managed requirement the same destination is unrestricted.
	if err := os.Remove(filepath.Join(home, "requirements.toml")); err != nil {
		t.Fatalf("remove requirements.toml: %v", err)
	}
	// Rust republishes the policy at an explicit config reload; the next
	// transport build then binds the reloaded requirements.
	if err := router.services.Config.ReloadRequirementsFromHome(); err != nil {
		t.Fatalf("ReloadRequirementsFromHome() error = %v", err)
	}
	unrestricted := router.httpClientForConfig(nil)
	response, err = unrestricted.Do(&http.Request{URL: appServerTestURL(t, "https://denied.example/v1")})
	if err != nil {
		t.Fatalf("unrestricted request error = %v", err)
	}
	_ = response.Body.Close()
	if stub.attempts != 2 {
		t.Fatalf("attempts = %d, want the unrestricted request to pass through", stub.attempts)
	}
	if unrestrictedClient := router.policyHTTPClientForConfig(nil); unrestrictedClient != nil {
		if _, ok := unrestrictedClient.Transport.(*network.PolicyRoundTripper); ok {
			t.Fatal("an unrestricted policy still wrapped the concrete client")
		}
	}
}
