package network

import (
	"context"
	"testing"
)

// Rust #62386 prepares the managed proxy environment once per sandbox
// command. The environment bound to a command is a snapshot: reloading the
// proxy configuration afterwards must not rewrite it, including the credential
// brokerage state a shell may have already observed.
func TestPreparedProxyEnvironmentSnapshotSurvivesConfigReloadLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.MITM = true
	settings.CredentialBroker = false
	settings.EnableSocks5 = false
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"api.openai.com"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{
		"OPENAI_API_KEY": "sk-real-openai-key",
		"SHELL_TOKEN":    "bound-for-shell",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	env, sandboxContext, err := prepared.PrepareForEnvironment("local")
	if err != nil {
		t.Fatal(err)
	}
	if env[CredentialBrokerActiveEnvKey] == "1" {
		t.Fatalf("unbrokered environment already reports an active broker: %#v", env)
	}
	if len(sandboxContext.LoopbackPorts) == 0 {
		t.Fatalf("prepared sandbox context has no loopback proxy port: %#v", sandboxContext)
	}

	reloaded := settings
	reloaded.CredentialBroker = true
	if err := prepared.ReloadConfig(ProxyConfig{Network: reloaded}); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}

	if env[CredentialBrokerActiveEnvKey] == "1" || env["SHELL_TOKEN"] != "bound-for-shell" || env["HTTP_PROXY"] == "" {
		t.Fatalf("prepared environment followed the reload: %#v", env)
	}

	if _, _, err := prepared.PrepareForEnvironment("local"); err != nil {
		t.Fatalf("PrepareForEnvironment after reload: %v", err)
	}
	if env[CredentialBrokerActiveEnvKey] == "1" {
		t.Fatalf("a later preparation rewrote the first command's environment: %#v", env)
	}
}
