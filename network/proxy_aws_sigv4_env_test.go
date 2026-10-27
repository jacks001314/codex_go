package network

import (
	"context"
	"testing"
)

func awsLocalSigningSettings() ProxySettings {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SocksURL = "http://127.0.0.1:0"
	settings.AllowLocalBinding = true
	settings.MITM = true
	settings.CredentialBroker = true
	settings.SetAllowedDomains([]string{"bucket.s3.amazonaws.com"})
	settings.MITMHooks = []ProxyMITMHookConfig{{
		Host: "bucket.s3.amazonaws.com",
		Match: ProxyMITMHookMatchConfig{
			Methods:      []string{"PUT"},
			PathPrefixes: []string{"/"},
		},
		Actions: ProxyMITMHookActionsConfig{AWSSigV4: &AWSSigV4Config{
			PlaceholderAccessKeyID: "MASKEDACCESSKEY",
			Region:                 "us-east-1",
			Service:                "s3",
			AccessKeyID:            ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_AWS_ACCESS_KEY"},
			SecretAccessKey:        ProxySecretSource{Kind: "env_var", Name: "CODEX_TEST_AWS_SECRET_KEY"},
			Port:                   443,
			MaxBodyBytes:           1024,
		}},
	}}
	return settings
}

// TestLocalManagedNetworkMasksAWSCredentialsLikeRust mirrors #56554: a local
// sandboxed process receives the placeholder pair and the managed CA bundle.
func TestLocalManagedNetworkMasksAWSCredentialsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_TEST_AWS_ACCESS_KEY", "AKIDEXAMPLE")
	t.Setenv("CODEX_TEST_AWS_SECRET_KEY", "wJalrXUtnFEMI")
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: awsLocalSigningSettings()}, map[string]string{
		"AWS_ACCESS_KEY_ID": "AKIDEXAMPLE",
		"PRESERVED":         "value",
	})
	if err != nil {
		t.Fatalf("StartProxyManagedNetwork() error = %v", err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	if got := prepared.Env["AWS_ACCESS_KEY_ID"]; got != "MASKEDACCESSKEY" {
		t.Fatalf("access key = %q", got)
	}
	if got := prepared.Env["AWS_SECRET_ACCESS_KEY"]; got != awsSigV4DummySecret {
		t.Fatalf("secret = %q", got)
	}
	if got := prepared.Env["PRESERVED"]; got != "value" {
		t.Fatalf("unrelated env = %q", got)
	}
	if got := prepared.Env[AWSProxyCABundleEnvVar]; got == "" || got != prepared.server.mitm.trustBundlePath {
		t.Fatalf("AWS_CA_BUNDLE = %q", got)
	}
}

// TestRemoteEnvironmentKeepsAWSCredentialsLikeRust proves only the local
// sandboxed process receives placeholders.
func TestRemoteEnvironmentKeepsAWSCredentialsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_TEST_AWS_ACCESS_KEY", "AKIDEXAMPLE")
	t.Setenv("CODEX_TEST_AWS_SECRET_KEY", "wJalrXUtnFEMI")
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: awsLocalSigningSettings()}, map[string]string{
		"AWS_ACCESS_KEY_ID": "AKIDEXAMPLE",
	})
	if err != nil {
		t.Fatalf("StartProxyManagedNetwork() error = %v", err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	env, _, err := prepared.PrepareForEnvironment("remote")
	if err != nil {
		t.Fatalf("PrepareForEnvironment() error = %v", err)
	}
	if got := env["AWS_ACCESS_KEY_ID"]; got != "AKIDEXAMPLE" {
		t.Fatalf("remote access key = %q", got)
	}
	if got := env["AWS_SECRET_ACCESS_KEY"]; got == awsSigV4DummySecret {
		t.Fatalf("remote secret was masked")
	}
}

// TestLocalManagedNetworkSkipsAWSSigningWithoutBrokerLikeRust pins Rust's
// enabled/credential_broker/mitm gate for local signing pairs.
func TestLocalManagedNetworkSkipsAWSSigningWithoutBrokerLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_TEST_AWS_ACCESS_KEY", "AKIDEXAMPLE")
	t.Setenv("CODEX_TEST_AWS_SECRET_KEY", "wJalrXUtnFEMI")
	settings := awsLocalSigningSettings()
	settings.CredentialBroker = false
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, map[string]string{
		"AWS_ACCESS_KEY_ID": "AKIDEXAMPLE",
	})
	if err != nil {
		t.Fatalf("StartProxyManagedNetwork() error = %v", err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	if got := prepared.Env["AWS_ACCESS_KEY_ID"]; got != "AKIDEXAMPLE" {
		t.Fatalf("access key = %q", got)
	}
}
