package remotecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/model"
	"codex_go/runtimeauth"
)

const remoteControlRuntimeInstallationID = "88888888-8888-4888-8888-888888888888"

// newRemoteControlRuntimeManager returns a manager whose registration endpoint
// confirms actorAccountID, mirroring the Rust remote-control ground truth tests.
func newRemoteControlRuntimeManager(t *testing.T, actorAccountID string, registrations *int) *runtimeauth.Manager {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*registrations = *registrations + 1
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"runtime_agent_instance_id": "rai_1",
			"agent_host_id":             "host_1",
			"actor": map[string]any{
				"kind":       "chatgpt",
				"user_id":    "user_1",
				"account_id": actorAccountID,
			},
		})
	}))
	t.Cleanup(server.Close)
	manager, err := runtimeauth.NewManager(runtimeauth.ManagerOptions{
		Credentials: func(context.Context) (*auth.AuthDotJSON, error) {
			return &auth.AuthDotJSON{
				AuthMode: "chatgpt",
				Tokens: map[string]any{
					"access_token": "Access Token",
					"account_id":   "account_id",
				},
			}, nil
		},
		InstallationID:  remoteControlRuntimeInstallationID,
		RegistrationURL: server.URL,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatalf("runtimeauth.NewManager() error = %v", err)
	}
	return manager
}

// runtimeBoundRemoteControlAuth mirrors CodexAuth::create_dummy_chatgpt_auth_for_testing:
// a bearer actor credential whose account id matches the registered runtime.
func runtimeBoundRemoteControlAuth(t *testing.T, manager *runtimeauth.Manager, mode runtimeauth.Mode) *RemoteControlConnectionAuth {
	t.Helper()
	slot := &RuntimeAuthSlot{}
	if err := slot.Set(manager); err != nil {
		t.Fatalf("RuntimeAuthSlot.Set() error = %v", err)
	}
	return &RemoteControlConnectionAuth{
		AuthHeaders: &model.AuthHeaders{Headers: http.Header{
			"Authorization":      []string{"Bearer Access Token"},
			"ChatGPT-Account-ID": []string{"account_id"},
		}},
		AccountID:   "account_id",
		RuntimeAuth: &RuntimeAuthSelection{Slot: slot, Mode: mode},
	}
}

func TestRemoteControlAuthLoaderBindsRuntimeAuthLikeRust(t *testing.T) {
	registrations := 0
	manager := newRemoteControlRuntimeManager(t, "account_id", &registrations)
	selection := &RuntimeAuthSelection{Slot: &RuntimeAuthSlot{}, Mode: runtimeauth.ModeAssertionOnly}
	if err := selection.Slot.Set(manager); err != nil {
		t.Fatalf("RuntimeAuthSlot.Set() error = %v", err)
	}

	loader := WithRuntimeAuth(func(context.Context) (*RemoteControlConnectionAuth, error) {
		return &RemoteControlConnectionAuth{AccountID: "account_id"}, nil
	}, selection)
	auth, err := loader(context.Background())
	if err != nil {
		t.Fatalf("loader error = %v", err)
	}
	if auth.RuntimeAuth != selection {
		t.Fatalf("loader did not bind the runtime selection")
	}
	if got := auth.RuntimeAuth.Mode; got != runtimeauth.ModeAssertionOnly {
		t.Fatalf("mode = %s, want assertion_only", got)
	}

	// Without a selection the loader keeps its own behavior, so callers outside
	// runtime auth are not affected.
	plain := func(context.Context) (*RemoteControlConnectionAuth, error) {
		return &RemoteControlConnectionAuth{AccountID: "account_id"}, nil
	}
	if got := WithRuntimeAuth(plain, nil); got == nil {
		t.Fatalf("nil selection dropped the loader")
	}
}

func TestRemoteControlRequestsUseRuntimeAuthLikeRust(t *testing.T) {
	const enrollEndpoint = "https://chatgpt.com/backend-api/wham/remote/control/server/enroll"
	for _, mode := range []runtimeauth.Mode{runtimeauth.ModeDualHeader, runtimeauth.ModeAssertionOnly} {
		registrations := 0
		manager := newRemoteControlRuntimeManager(t, "account_id", &registrations)
		auth := runtimeBoundRemoteControlAuth(t, manager, mode)

		headers, err := auth.RequestHeadersForEndpoint(context.Background(), enrollEndpoint)
		if err != nil {
			t.Fatalf("mode %s: RequestHeadersForEndpoint() error = %v", mode, err)
		}
		if got := headers.Get(RemoteControlAccountIDHeader); got != "account_id" {
			t.Fatalf("mode %s: account header = %q, want the runtime actor account", mode, got)
		}
		if got := headers.Values("Authorization"); len(got) != 1 {
			t.Fatalf("mode %s: Authorization = %#v, want exactly one replaced credential", mode, got)
		}
		switch mode {
		case runtimeauth.ModeDualHeader:
			if got := headers.Get("Authorization"); got != "Bearer Access Token" {
				t.Fatalf("dual header Authorization = %q, want the actor credential", got)
			}
			if assertion := headers.Get(runtimeauth.AssertionHeader); !strings.HasPrefix(assertion, "1.") {
				t.Fatalf("dual header assertion = %q, want a signed runtime assertion", assertion)
			}
		case runtimeauth.ModeAssertionOnly:
			if got := headers.Get("Authorization"); !strings.HasPrefix(got, "AgentAssertion ") {
				t.Fatalf("assertion-only Authorization = %q, want AgentAssertion", got)
			}
			if assertion := headers.Get(runtimeauth.AssertionHeader); assertion != "" {
				t.Fatalf("assertion-only header = %q, want none", assertion)
			}
		}
		if registrations != 1 {
			t.Fatalf("mode %s: registrations = %d, want 1", mode, registrations)
		}

		// The request path shares the same resolution, and a runtime selection
		// must not append the actor bearer it just removed.
		request, err := http.NewRequest(http.MethodPost, enrollEndpoint, strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if _, err := auth.ApplyRequest(context.Background(), request, []byte("{}")); err != nil {
			t.Fatalf("mode %s: ApplyRequest() error = %v", mode, err)
		}
		if got := request.Header.Values("Authorization"); len(got) != 1 {
			t.Fatalf("mode %s: request Authorization = %#v, want exactly one credential", mode, got)
		}
		if mode == runtimeauth.ModeAssertionOnly {
			if got := request.Header.Get("Authorization"); !strings.HasPrefix(got, "AgentAssertion ") {
				t.Fatalf("assertion-only request Authorization = %q, want AgentAssertion", got)
			}
		}
	}
}

func TestRemoteControlRuntimeAuthRejectsNonOpenAIDestinationLikeRust(t *testing.T) {
	t.Run("unsupported openai origin", func(t *testing.T) {
		registrations := 0
		manager := newRemoteControlRuntimeManager(t, "account_id", &registrations)
		auth := runtimeBoundRemoteControlAuth(t, manager, runtimeauth.ModeDualHeader)
		for _, endpoint := range []string{
			"https://chatgpt.com:8443/backend-api",
			"http://chatgpt.com/backend-api",
		} {
			_, err := auth.RequestHeadersForEndpoint(context.Background(), endpoint)
			if !errors.Is(err, ErrRemoteControlRuntimeAuthUnsupportedEndpoint) {
				t.Fatalf("endpoint %q error = %v, want ErrRemoteControlRuntimeAuthUnsupportedEndpoint", endpoint, err)
			}
		}
		if registrations != 0 {
			t.Fatalf("registrations = %d, want none for rejected origins", registrations)
		}
	})

	t.Run("custom destination keeps actor credential", func(t *testing.T) {
		registrations := 0
		manager := newRemoteControlRuntimeManager(t, "account_id", &registrations)
		auth := runtimeBoundRemoteControlAuth(t, manager, runtimeauth.ModeDualHeader)
		headers, err := auth.RequestHeadersForEndpoint(context.Background(), "https://remote.example/control")
		if err != nil {
			t.Fatalf("custom destination error = %v", err)
		}
		if got := headers.Get("Authorization"); got != "Bearer Access Token" {
			t.Fatalf("custom destination Authorization = %q, want the actor credential", got)
		}
		if assertion := headers.Get(runtimeauth.AssertionHeader); assertion != "" {
			t.Fatalf("custom destination assertion = %q, want none", assertion)
		}
		// A destination the runtime declines must keep every actor credential
		// header instead of being stripped by the replacement pass.
		if got := headers.Get(RemoteControlAccountIDHeader); got != "account_id" {
			t.Fatalf("custom destination account header = %q, want the actor account", got)
		}
		if got := headers.Get("Authorization"); len(got) == 0 {
			t.Fatalf("custom destination lost the actor credential entirely")
		}
		if registrations != 0 {
			t.Fatalf("registrations = %d, want none for custom destinations", registrations)
		}
	})

	t.Run("runtime account mismatch", func(t *testing.T) {
		registrations := 0
		manager := newRemoteControlRuntimeManager(t, "another-account", &registrations)
		for _, mode := range []runtimeauth.Mode{runtimeauth.ModeDualHeader, runtimeauth.ModeAssertionOnly} {
			auth := runtimeBoundRemoteControlAuth(t, manager, mode)
			_, err := auth.RequestHeadersForEndpoint(
				context.Background(),
				"https://chatgpt.com/backend-api/wham/remote/control/server/enroll",
			)
			if err == nil || !strings.Contains(err.Error(), "chatgpt-account-id does not match") {
				t.Fatalf("mode %s: error = %v, want chatgpt-account-id mismatch", mode, err)
			}
		}
	})

	t.Run("empty slot", func(t *testing.T) {
		auth := &RemoteControlConnectionAuth{
			AuthHeaders: &model.AuthHeaders{Headers: http.Header{
				"Authorization": []string{"Bearer Access Token"},
			}},
			AccountID:   "account_id",
			RuntimeAuth: &RuntimeAuthSelection{Slot: &RuntimeAuthSlot{}, Mode: runtimeauth.ModeDualHeader},
		}
		_, err := auth.RequestHeadersForEndpoint(
			context.Background(),
			"https://chatgpt.com/backend-api/wham/remote/control/server/enroll",
		)
		if !errors.Is(err, ErrRemoteControlRuntimeAuthNotInitialized) {
			t.Fatalf("empty slot error = %v, want ErrRemoteControlRuntimeAuthNotInitialized", err)
		}
	})
}
