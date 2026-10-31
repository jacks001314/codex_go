package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"codex_go/config"
)

func TestAppServerPrintStartupConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"app-server", "--print-startup-config"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("startup discovery error = %v (stderr=%q)", err, stderr.String())
	}
	var result struct {
		AppServerVersion   string `json:"appServerVersion"`
		ConfigRequirements struct {
			SupportsIndependentSpeedModes *bool `json:"supportsIndependentSpeedModes"`
			Requirements                  any   `json:"requirements"`
		} `json:"configRequirements"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		t.Fatalf("decode startup config %q: %v", stdout.String(), err)
	}
	if strings.TrimSpace(result.AppServerVersion) == "" {
		t.Fatalf("appServerVersion = %q", result.AppServerVersion)
	}
	if result.ConfigRequirements.SupportsIndependentSpeedModes == nil || !*result.ConfigRequirements.SupportsIndependentSpeedModes {
		t.Fatalf("supportsIndependentSpeedModes = %#v", result.ConfigRequirements.SupportsIndependentSpeedModes)
	}
}

func TestAppServerPrintStartupConfigEmitsStructuredErrorLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("unknown_field = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"app-server", "--print-startup-config", "--strict-config"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("invalid configuration was accepted")
	}
	var output struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if decodeErr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &output); decodeErr != nil {
		t.Fatalf("decode structured error %q: %v", stdout.String(), decodeErr)
	}
	if output.Error.Code != -32600 || !strings.Contains(output.Error.Message, "unknown configuration field") {
		t.Fatalf("structured error = %#v", output.Error)
	}
}

func TestAppServerStartupClientInfoValidationLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"app-server", "--print-startup-config", "--startup-client-info", `{"name":"desktop","version":"1"}`}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("valid client info error = %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	// 0.166 validates the ClientInfo while clap parses the flag, so the missing
	// field is a usage diagnostic on stderr rather than a structured app-server
	// error on stdout.
	err := Run(context.Background(), []string{"app-server", "--print-startup-config", "--startup-client-info", `{"version":"1"}`}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("client info without a name was accepted")
	}
	if want := "error: invalid value '{\"version\":\"1\"}' for '--startup-client-info <STARTUP_CLIENT_INFO>': missing field `name` at line 1 column 15"; !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
