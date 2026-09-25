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
}
