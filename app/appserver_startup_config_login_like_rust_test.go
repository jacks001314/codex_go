package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/config"
)

// Rust #62867: startup discovery turns a login-required configuration-server
// failure into the actionable configServerLogin error, while any other auth
// failure reports LoginFailed.
func TestAppServerPrintStartupConfigRequestsConfigServerLoginLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	restore := loadAppServerStartupConfigForDiscovery
	loadAppServerStartupConfigForDiscovery = func(string, cli.AppServerOptions, *cli.RootOptions) (*config.Config, error) {
		return nil, fmt.Errorf("failed to load cloud config bundle: %w", config.NewCloudConfigLoginRequiredError(configServerSignInMessage))
	}
	defer func() { loadAppServerStartupConfigForDiscovery = restore }()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"app-server", "--print-startup-config"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("login-required configuration was accepted")
	}
	var output struct {
		Error struct {
			Code    int            `json:"code"`
			Message string         `json:"message"`
			Data    map[string]any `json:"data"`
		} `json:"error"`
	}
	if decodeErr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &output); decodeErr != nil {
		t.Fatalf("decode structured error %q: %v", stdout.String(), decodeErr)
	}
	if output.Error.Code != -32600 || output.Error.Message != "Configuration server sign-in required" {
		t.Fatalf("structured error = %#v", output.Error)
	}
	want := map[string]any{"reason": "configServer", "errorCode": "Auth", "action": "configServerLogin"}
	if fmt.Sprint(output.Error.Data) != fmt.Sprint(want) {
		t.Fatalf("error data = %#v, want %#v", output.Error.Data, want)
	}
}

func TestAppServerPrintStartupConfigReportsLoginFailedLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	restore := loadAppServerStartupConfigForDiscovery
	loadAppServerStartupConfigForDiscovery = func(string, cli.AppServerOptions, *cli.RootOptions) (*config.Config, error) {
		return nil, config.NewCloudConfigLoadError(config.CloudConfigLoadAuth, nil, configServerAuthFailureMessage)
	}
	defer func() { loadAppServerStartupConfigForDiscovery = restore }()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"app-server", "--print-startup-config"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("auth failure was accepted")
	}
	var output struct {
		Error struct {
			Code    int            `json:"code"`
			Message string         `json:"message"`
			Data    map[string]any `json:"data"`
		} `json:"error"`
	}
	if decodeErr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &output); decodeErr != nil {
		t.Fatalf("decode structured error %q: %v", stdout.String(), decodeErr)
	}
	if output.Error.Message != configServerAuthFailureMessage {
		t.Fatalf("message = %q", output.Error.Message)
	}
	want := map[string]any{"reason": "configServer", "errorCode": "LoginFailed"}
	if fmt.Sprint(output.Error.Data) != fmt.Sprint(want) {
		t.Fatalf("error data = %#v, want %#v", output.Error.Data, want)
	}
}

func TestCloudConfigLoginRequiredDetectionLikeRust(t *testing.T) {
	status := 401
	cases := []struct {
		name string
		err  *config.CloudConfigLoadError
		want bool
	}{
		{name: "login required", err: config.NewCloudConfigLoginRequiredError(configServerSignInMessage), want: true},
		{name: "auth 401", err: config.NewCloudConfigLoadError(config.CloudConfigLoadAuth, &status, "unauthorized"), want: true},
		{name: "auth no status", err: config.NewCloudConfigLoadError(config.CloudConfigLoadAuth, nil, configServerAuthFailureMessage)},
		{name: "request failed", err: config.NewCloudConfigLoadError(config.CloudConfigLoadRequestFailed, &status, "failed")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.RequiresLogin(); got != tc.want {
				t.Fatalf("RequiresLogin() = %v, want %v", got, tc.want)
			}
		})
	}
	if got := (*config.CloudConfigLoadError)(nil).RequiresLogin(); got {
		t.Fatal("nil error reported login required")
	}
}

// Rust #62867: --startup-oauth is a dedicated stdio mode that runs before the
// app server initializes and refuses subcommands.
func TestAppServerStartupOAuthServesStdioLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	stdin := strings.NewReader(`{"id":1,"method":"account/gatewayOAuth/cancel"}` + "\n")
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"app-server", "--startup-oauth"}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("startup oauth mode error = %v (stderr=%q)", err, stderr.String())
	}
	output := strings.TrimSpace(stdout.String())
	if output != `{"id":1,"result":{}}` {
		t.Fatalf("stdout = %q", output)
	}
	var stdoutAgain, stderrAgain bytes.Buffer
	err := Run(context.Background(), []string{"app-server", "--startup-oauth", "generate-ts", "--out", t.TempDir()}, strings.NewReader(""), &stdoutAgain, &stderrAgain)
	if err == nil || err.Error() != "startup configuration modes cannot run a subcommand" {
		t.Fatalf("subcommand error = %v", err)
	}
}
