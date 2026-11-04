package app

import (
	"testing"

	"codex_go/appserverdaemon"
	"codex_go/cli"
)

func providerSetupDaemonTestState(t *testing.T) {
	t.Helper()
	originalFeature := daemonAutoStartFeature
	originalStart := daemonAutoStartStart
	originalReason := daemonAutoStartExclusionReason
	originalGCP := gcpSetupWizardRequestedThisLaunch
	t.Cleanup(func() {
		daemonAutoStartFeature = originalFeature
		daemonAutoStartStart = originalStart
		daemonAutoStartExclusionReason = originalReason
		gcpSetupWizardRequestedThisLaunch = originalGCP
	})
	t.Setenv("CODEX_HOME", t.TempDir())
	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) { return "/tmp/provider-setup.sock", nil }
}

// TestGcpSetupWizardKeepsEmbeddedServerLikeRust pins Rust #59068: GCP setup
// resolves credential paths and runs gcloud in the launch environment, so the
// launch keeps the embedded server and reports the exclusion.
func TestGcpSetupWizardKeepsEmbeddedServerLikeRust(t *testing.T) {
	providerSetupDaemonTestState(t)
	gcpSetupWizardRequestedThisLaunch = true
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) {
		t.Fatal("GCP sign-in must not start the shared server")
		return "", nil
	}
	endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{})
	if err != nil || endpoint != nil {
		t.Fatalf("GCP sign-in endpoint = %#v, err = %v; want embedded", endpoint, err)
	}
	if daemonAutoStartExclusionReason != gcpSignInExclusion {
		t.Fatalf("exclusion = %q, want %q", daemonAutoStartExclusionReason, gcpSignInExclusion)
	}
	if got := interactiveEmbeddedTUIStartReason(&cli.RootOptions{}, ""); got != "gcp_sign_in" {
		t.Fatalf("embedded start reason = %q, want gcp_sign_in", got)
	}
}

// TestBedrockSetupWizardUsesLocalDaemonLikeRust pins the Bedrock half of
// #59068: the wizard no longer forces the embedded server, so an eligible
// launch still starts or reuses the local daemon.
func TestBedrockSetupWizardUsesLocalDaemonLikeRust(t *testing.T) {
	providerSetupDaemonTestState(t)
	gcpSetupWizardRequestedThisLaunch = false
	endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{})
	if err != nil {
		t.Fatalf("auto-start endpoint error = %v", err)
	}
	if endpoint == nil || endpoint.Kind != appserverdaemon.RemoteEndpointUnixSocket {
		t.Fatalf("endpoint = %#v, want the local daemon", endpoint)
	}
	if daemonAutoStartExclusionReason != "" {
		t.Fatalf("exclusion = %q, want none", daemonAutoStartExclusionReason)
	}
}
