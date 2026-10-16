package appserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/network"
)

// Rust parity: app-server/src/request_processors/account_processor/
// workspace_routing.rs (#53805, "Add safe diagnostics to workspace routing
// configuration errors").
//
// Workspace routing's configuration load and reload failures used to be opaque.
// They now carry a safe reason - a configuration location, a cloud error category
// with its HTTP status, a known application network policy denial, or an OS error
// - and never a raw parser message, configuration contents, or a backend-supplied
// policy name or id.

// malformedWorkspaceRoutingConfig is a configuration that fails to parse and
// carries a value that must never reach a client (the regression fixture from the
// Rust suite).
const malformedWorkspaceRoutingConfig = "invalid = [ 'sk-test-private-value',"

// cloudConfigLoadError hands a cloud bundle failure to the routing code the way
// the loader does (Rust wraps it in an io::Error::other).
func cloudConfigLoadError(code config.CloudConfigLoadErrorCode, statusCode *int, message string) error {
	return fmt.Errorf("failed to load cloud config bundle: %w", config.NewCloudConfigLoadError(code, statusCode, message))
}

// TestWorkspaceConfigLoadReasonPreservesPolicyDenialsButRedactsOtherMessagesLikeRust
// mirrors Rust workspace_routing_tests.rs
// cloud_failures_preserve_policy_denials_but_redact_other_messages: a verbatim
// policy denial survives, anything else is replaced by its category.
func TestWorkspaceConfigLoadReasonPreservesPolicyDenialsButRedactsOtherMessagesLikeRust(t *testing.T) {
	denied := network.ErrNetworkPolicyDestination.Error()
	for _, testCase := range []struct {
		name string
		code config.CloudConfigLoadErrorCode
		want string
	}{
		{name: "request", code: config.CloudConfigLoadRequestFailed, want: "cloud config: request failed"},
		{name: "refresh", code: config.CloudConfigLoadAuth, want: "cloud config: authentication failed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, input := range []struct {
				message string
				want    string
			}{
				{message: denied, want: denied},
				{message: denied + ": sk-test-private-value", want: testCase.want},
			} {
				got := workspaceConfigLoadReason(cloudConfigLoadError(testCase.code, nil, input.message))
				if got != input.want {
					t.Fatalf("workspaceConfigLoadReason(%q) = %q, want %q", input.message, got, input.want)
				}
				if strings.Contains(got, "sk-test-private-value") {
					t.Fatalf("reason leaked the backend message: %q", got)
				}
			}
		})
	}
}

// TestWorkspaceConfigLoadReasonCloudCategoriesLikeRust pins the remaining cloud
// categories, including the HTTP status suffix, and requires the raw message to
// stay redacted in every one of them.
func TestWorkspaceConfigLoadReasonCloudCategoriesLikeRust(t *testing.T) {
	status := http.StatusForbidden
	for _, testCase := range []struct {
		name    string
		code    config.CloudConfigLoadErrorCode
		status  *int
		message string
		want    string
	}{
		{
			name:    "timeout",
			code:    config.CloudConfigLoadTimeout,
			message: "cloud config request timed out",
			want:    "cloud config: request timed out",
		},
		{
			name:    "request failed with status",
			code:    config.CloudConfigLoadRequestFailed,
			status:  &status,
			message: "cloud config request failed",
			want:    "cloud config: request failed (HTTP 403)",
		},
		{
			name:    "authentication failed with status",
			code:    config.CloudConfigLoadAuth,
			status:  &status,
			message: "sk-test-private-value",
			want:    "cloud config: authentication failed (HTTP 403)",
		},
		{
			name:    "invalid bundle",
			code:    config.CloudConfigLoadInvalidBundle,
			message: "invalid cloud config bundle: sk-test-private-value",
			want:    "cloud config: invalid bundle",
		},
		{
			name:    "internal",
			code:    config.CloudConfigLoadInternal,
			message: "sk-test-private-value",
			want:    "cloud config: internal error",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := workspaceConfigLoadReason(cloudConfigLoadError(testCase.code, testCase.status, testCase.message))
			if got != testCase.want {
				t.Fatalf("workspaceConfigLoadReason() = %q, want %q", got, testCase.want)
			}
			if strings.Contains(got, "sk-test-private-value") {
				t.Fatalf("reason leaked the backend message: %q", got)
			}
		})
	}
}

// TestWorkspaceConfigLoadReasonReportsLocationsLikeRust mirrors the two location
// arms: an absolute file path is safe to report, while a non-file diagnostic's
// display name is not, so only its position is forwarded.
func TestWorkspaceConfigLoadReasonReportsLocationsLikeRust(t *testing.T) {
	absolute := config.ConfigPath(t.TempDir())
	if got := workspaceConfigLoadReason(&config.ConfigLoadError{Path: absolute, Line: 3, Column: 7}); got != "invalid configuration at "+absolute+":3:7" {
		t.Fatalf("absolute location = %q", got)
	}
	nonFile := workspaceConfigLoadReason(&config.ConfigLoadError{Path: "sk-test-private-name", Line: 1, Column: 9})
	if nonFile != "invalid non-file configuration at line 1, column 9" {
		t.Fatalf("non-file location = %q", nonFile)
	}
	if strings.Contains(nonFile, "sk-test-private-name") {
		t.Fatalf("non-file reason leaked the display name: %q", nonFile)
	}
	// A missing parser position still reports the 1-based default the Rust range
	// uses.
	if got := workspaceConfigLoadReason(&config.ConfigLoadError{Path: absolute}); got != "invalid configuration at "+absolute+":1:1" {
		t.Fatalf("default location = %q", got)
	}
}

// TestWorkspaceConfigLoadReasonFallbackLikeRust pins the tail: the OS error, the
// error kind, and never an untyped error's message.
func TestWorkspaceConfigLoadReasonFallbackLikeRust(t *testing.T) {
	if got := workspaceConfigLoadReason(os.ErrNotExist); got != "entity not found" {
		t.Fatalf("missing file reason = %q", got)
	}
	if got := workspaceConfigLoadReason(os.ErrPermission); got != "permission denied" {
		t.Fatalf("permission reason = %q", got)
	}
	got := workspaceConfigLoadReason(errors.New("sk-test-private-value"))
	if got != "other" {
		t.Fatalf("untyped reason = %q", got)
	}
	if strings.Contains(got, "sk-test-private-value") {
		t.Fatalf("untyped reason leaked the error message: %q", got)
	}
}

// workspaceRoutingMalformedConfigRouter wires a router whose configuration file is
// malformed, so workspace routing cannot capture its configuration scope (Rust
// read_account's first load_config).
func workspaceRoutingMalformedConfigRouter(t *testing.T, home string, handler http.HandlerFunc) *RuntimeRouter {
	t.Helper()
	clearAuthEnvAppserver(t)
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("chatgpt-token", "workspace", nil)); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	if err := os.WriteFile(config.ConfigPath(home), []byte(malformedWorkspaceRoutingConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
}

// TestRuntimeRouterGetAccountWorkspaceRoutingSelectionLikeRust is the selection
// half of the account/read pair: a workspace credential with a valid configuration
// gets the discovered routing back through the real entry. Its failure half is
// TestWorkspaceRoutingRequirementsReloadErrorCarriesSafeReasonLikeRust.
func TestRuntimeRouterGetAccountWorkspaceRoutingSelectionLikeRust(t *testing.T) {
	router := workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/accounts/check" {
			t.Errorf("accounts check path = %q", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{"accounts": []any{map[string]any{
			"id":                       "workspace",
			"workspace_backend_origin": "https://gov.chatgpt.com",
			"account_routing_override": "us_cr",
		}}})
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error != nil {
		t.Fatalf("response error = %+v", response.Error)
	}
	account, ok := response.Result.(*auth.GetAccountResponse)
	if !ok || account == nil || account.WorkspaceRouting == nil {
		t.Fatalf("response result = %+v, want a resolved workspace routing", response.Result)
	}
	if account.WorkspaceRouting.BackendOrigin != "https://gov.chatgpt.com" ||
		account.WorkspaceRouting.AccountRoutingOverride != auth.AccountRoutingOverrideUSCR {
		t.Fatalf("workspace routing = %+v", account.WorkspaceRouting)
	}
}

// TestWorkspaceRoutingRequirementsLoadErrorCarriesSafeReasonLikeRust mirrors the
// load arm, which Rust's regression covers through
// read_account_for_background_workspace_routing (#53805): the configuration cannot
// be captured at all, so workspace routing reports the location instead of falling
// back to the startup configuration.
//
// The account/read and turn entries cannot reach this arm in Go, because both read
// the effective configuration - including the forced-login restrictions - before
// workspace routing, so a malformed configuration file is reported by those steps
// first. The arm is therefore driven through the routing entry itself, which is
// also the entry Rust's unit test calls.
func TestWorkspaceRoutingRequirementsLoadErrorCarriesSafeReasonLikeRust(t *testing.T) {
	home := t.TempDir()
	router := workspaceRoutingMalformedConfigRouter(t, home, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("workspace routing reached %q with a malformed configuration", r.URL.Path)
	})
	snapshot := auth.FromChatGPTAuthTokens("chatgpt-token", "workspace", nil)
	routing, err := router.workspaceRoutingForAccountRead(context.Background(), &snapshot)
	if routing != nil {
		t.Fatalf("routing = %+v, want none", routing)
	}
	if err == nil {
		t.Fatal("workspace routing resolved with a malformed configuration")
	}
	message := err.Error()
	want := "failed to load workspace requirements: invalid configuration at " + config.ConfigPath(home) + ":1:"
	if !strings.HasPrefix(message, want) {
		t.Fatalf("error = %q, want prefix %q", message, want)
	}
	if strings.Contains(message, "sk-test-private-value") {
		t.Fatalf("error leaked the configuration value: %q", message)
	}
}

// TestWorkspaceRoutingRequirementsReloadErrorCarriesSafeReasonLikeRust mirrors the
// reload arm through the real account/read entry: discovery succeeds, the
// configuration then turns invalid, and the reload check reports the location
// instead of falling back to the startup configuration.
func TestWorkspaceRoutingRequirementsReloadErrorCarriesSafeReasonLikeRust(t *testing.T) {
	home := t.TempDir()
	router := workspaceRoutingRouterWithHome(t, home, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/accounts/check" {
			t.Errorf("accounts check path = %q", r.URL.Path)
		}
		if err := os.WriteFile(config.ConfigPath(home), []byte(malformedWorkspaceRoutingConfig), 0o600); err != nil {
			t.Errorf("rewrite config: %v", err)
		}
		writeJSON(t, w, map[string]any{"accounts": []any{map[string]any{
			"id":                       "workspace",
			"workspace_backend_origin": "https://gov.chatgpt.com",
			"account_routing_override": "us",
		}}})
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error == nil || response.Error.Code != JSONRPCInternalErrorCode {
		t.Fatalf("response error = %+v, want internal error %d", response.Error, JSONRPCInternalErrorCode)
	}
	message := response.Error.Message
	want := "failed to reload workspace requirements: invalid configuration at " + config.ConfigPath(home) + ":1:"
	if !strings.HasPrefix(message, want) {
		t.Fatalf("error message = %q, want prefix %q", message, want)
	}
	if strings.Contains(message, "sk-test-private-value") {
		t.Fatalf("error message leaked the configuration value: %q", message)
	}
}
