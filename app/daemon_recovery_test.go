package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/tui/onboarding"
)

type daemonRecoveryTestHarness struct {
	reads        int
	read         func() (*daemonCompatibilityReadback, error)
	managed      bool
	restartCalls []map[string]bool
	restartErr   error
	runOptions   *onboarding.DaemonRecoveryOptions
	runResult    onboarding.DaemonRecoveryResult
	runErr       error
	interactive  bool
}

func (h *daemonRecoveryTestHarness) install(t *testing.T) {
	t.Helper()
	originalRead := daemonCompatibilityRead
	originalManaged := daemonRecoveryManaged
	originalRestart := daemonRecoveryRestartWithFeatures
	originalRun := daemonRecoveryRun
	originalInteractive := daemonRecoveryInteractive
	originalWarning := daemonCompatibilityWarningText
	t.Cleanup(func() {
		daemonCompatibilityRead = originalRead
		daemonRecoveryManaged = originalManaged
		daemonRecoveryRestartWithFeatures = originalRestart
		daemonRecoveryRun = originalRun
		daemonRecoveryInteractive = originalInteractive
		daemonCompatibilityWarningText = originalWarning
	})
	daemonCompatibilityRead = func(context.Context, *appserverdaemon.RemoteAppServerEndpoint, string, bool) (*daemonCompatibilityReadback, error) {
		h.reads++
		if h.read == nil {
			return &daemonCompatibilityReadback{}, nil
		}
		return h.read()
	}
	daemonRecoveryManaged = func() bool { return h.managed }
	daemonRecoveryRestartWithFeatures = func(features map[string]bool) error {
		h.restartCalls = append(h.restartCalls, features)
		return h.restartErr
	}
	daemonRecoveryRun = func(_ context.Context, options onboarding.DaemonRecoveryOptions, _ io.Reader, _ io.Writer) (onboarding.DaemonRecoveryResult, error) {
		h.runOptions = &options
		return h.runResult, h.runErr
	}
	daemonRecoveryInteractive = func(io.Reader, io.Writer) bool { return h.interactive }
}

func daemonRecoveryMismatchReadback() (*daemonCompatibilityReadback, error) {
	return &daemonCompatibilityReadback{
		Catalog: daemonCompatibilityTestCatalog(true),
	}, nil
}

// TestResolveDaemonCompatibilityFallsBackLikeRust covers an optional attachment:
// a non-restartable incompatibility becomes a startup warning and the launch
// continues in embedded mode without prompting.
func TestResolveDaemonCompatibilityFallsBackLikeRust(t *testing.T) {
	harness := &daemonRecoveryTestHarness{read: func() (*daemonCompatibilityReadback, error) {
		return nil, errDaemonCompatibilityConnect
	}}
	harness.install(t)
	endpoint := appserverdaemon.NewUnixSocketEndpoint("/tmp/codex.sock")
	resolved, warning, err := resolveDaemonCompatibility(context.Background(), &cli.RootOptions{}, endpoint, true, nil, nil)
	if err != nil || resolved != nil {
		t.Fatalf("resolved = %#v, err = %v", resolved, err)
	}
	if warning != "Running without the shared background server: could not connect to check daemon feature settings." {
		t.Fatalf("warning = %q", warning)
	}
	if harness.runOptions != nil {
		t.Fatal("a fallback must not open the recovery screen")
	}
	if daemonStartupWarning(&cli.RootOptions{}) != warning {
		t.Fatalf("startup warning = %q, want the compatibility warning", daemonStartupWarning(&cli.RootOptions{}))
	}
}

// TestResolveDaemonCompatibilityRequiresTerminalLikeRust covers Rust's
// non-interactive path: an incompatibility that needs recovery is fatal.
func TestResolveDaemonCompatibilityRequiresTerminalLikeRust(t *testing.T) {
	harness := &daemonRecoveryTestHarness{read: daemonRecoveryMismatchReadback}
	harness.install(t)
	endpoint := appserverdaemon.NewUnixSocketEndpoint("/tmp/codex.sock")
	_, _, err := resolveDaemonCompatibility(context.Background(), &cli.RootOptions{
		ConfigOverrides: []string{"features.api_key_model_discovery=false"},
	}, endpoint, false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Cannot use the shared background server: This session requires api_key_model_discovery to be disabled.") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), daemonFailureHint) {
		t.Fatalf("error = %v, want the --no-daemon guidance", err)
	}
	if harness.runOptions != nil {
		t.Fatal("a missing terminal must not open the recovery screen")
	}
}

// TestResolveDaemonCompatibilityRunWithoutDaemonLikeRust covers the first
// recovery choice: this launch continues embedded and reports why.
func TestResolveDaemonCompatibilityRunWithoutDaemonLikeRust(t *testing.T) {
	harness := &daemonRecoveryTestHarness{
		read:        daemonRecoveryMismatchReadback,
		managed:     true,
		interactive: true,
		runResult:   onboarding.DaemonRecoveryResult{Choice: onboarding.DaemonRecoveryRunWithoutDaemon},
	}
	harness.install(t)
	endpoint := appserverdaemon.NewUnixSocketEndpoint("/tmp/codex.sock")
	resolved, warning, err := resolveDaemonCompatibility(context.Background(), &cli.RootOptions{
		ConfigOverrides: []string{"features.api_key_model_discovery=false"},
	}, endpoint, false, nil, nil)
	if err != nil || resolved != nil {
		t.Fatalf("resolved = %#v, err = %v", resolved, err)
	}
	want := "Running without the shared background server: This session requires api_key_model_discovery to be disabled."
	if warning != want {
		t.Fatalf("warning = %q, want %q", warning, want)
	}
	if harness.runOptions == nil || !harness.runOptions.Managed || harness.runOptions.RestartFeatures == nil {
		t.Fatalf("recovery options = %#v", harness.runOptions)
	}
	if len(harness.restartCalls) != 0 {
		t.Fatal("running without the daemon must not restart it")
	}
}

// TestResolveDaemonCompatibilityCancelLikeRust covers Cancel (and Esc): startup
// fails with the compatibility error.
func TestResolveDaemonCompatibilityCancelLikeRust(t *testing.T) {
	for _, result := range []onboarding.DaemonRecoveryResult{
		{Choice: onboarding.DaemonRecoveryCancel},
		{Choice: onboarding.DaemonRecoveryCancel, Cancelled: true},
	} {
		harness := &daemonRecoveryTestHarness{
			read:        daemonRecoveryMismatchReadback,
			managed:     true,
			interactive: true,
			runResult:   result,
		}
		harness.install(t)
		endpoint := appserverdaemon.NewUnixSocketEndpoint("/tmp/codex.sock")
		_, _, err := resolveDaemonCompatibility(context.Background(), &cli.RootOptions{
			ConfigOverrides: []string{"features.api_key_model_discovery=false"},
		}, endpoint, false, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "This session requires api_key_model_discovery to be disabled") {
			t.Fatalf("cancel error = %v", err)
		}
		if len(harness.restartCalls) != 0 {
			t.Fatal("cancel must not restart the daemon")
		}
	}
}

// TestResolveDaemonCompatibilityRestartLikeRust covers the confirmed restart:
// the shared settings are persisted once, compatibility is re-checked exactly
// once, and a still-incompatible daemon fails instead of looping.
func TestResolveDaemonCompatibilityRestartLikeRust(t *testing.T) {
	harness := &daemonRecoveryTestHarness{
		read:        daemonRecoveryMismatchReadback,
		managed:     true,
		interactive: true,
		runResult:   onboarding.DaemonRecoveryResult{Choice: onboarding.DaemonRecoveryRestart},
	}
	harness.install(t)
	endpoint := appserverdaemon.NewUnixSocketEndpoint("/tmp/codex.sock")
	root := &cli.RootOptions{ConfigOverrides: []string{"features.api_key_model_discovery=false"}}
	resolved, warning, err := resolveDaemonCompatibility(context.Background(), root, endpoint, false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "This session requires api_key_model_discovery to be disabled") {
		t.Fatalf("re-check error = %v", err)
	}
	if resolved != nil || warning != "" {
		t.Fatalf("resolved = %#v, warning = %q", resolved, warning)
	}
	if len(harness.restartCalls) != 1 || !FeatureOverridesEqualForTest(harness.restartCalls[0], map[string]bool{"api_key_model_discovery": false}) {
		t.Fatalf("restart calls = %#v", harness.restartCalls)
	}
	if harness.reads != 2 {
		t.Fatalf("compatibility reads = %d, want exactly one re-check", harness.reads)
	}

	// A restart that resolves the mismatch keeps the daemon endpoint.
	harness = &daemonRecoveryTestHarness{
		managed:     true,
		interactive: true,
		runResult:   onboarding.DaemonRecoveryResult{Choice: onboarding.DaemonRecoveryRestart},
	}
	harness.read = func() (*daemonCompatibilityReadback, error) {
		if harness.reads == 1 {
			return daemonRecoveryMismatchReadback()
		}
		return &daemonCompatibilityReadback{Catalog: daemonCompatibilityTestCatalog(false)}, nil
	}
	harness.install(t)
	resolved, warning, err = resolveDaemonCompatibility(context.Background(), root, endpoint, false, nil, nil)
	if err != nil || warning != "" || resolved != endpoint {
		t.Fatalf("resolved = %#v, warning = %q, err = %v", resolved, warning, err)
	}
	if len(harness.restartCalls) != 1 || harness.reads != 2 {
		t.Fatalf("restart calls = %#v, reads = %d", harness.restartCalls, harness.reads)
	}
}

// TestResolveDaemonCompatibilityRejectsUnmanagedRestartLikeRust covers the guard
// behind the disabled row: a restart without a managed daemon or without
// restartable features fails instead of stopping someone else's server.
func TestResolveDaemonCompatibilityRejectsUnmanagedRestartLikeRust(t *testing.T) {
	harness := &daemonRecoveryTestHarness{
		read:        daemonRecoveryMismatchReadback,
		managed:     false,
		interactive: true,
		runResult:   onboarding.DaemonRecoveryResult{Choice: onboarding.DaemonRecoveryRestart},
	}
	harness.install(t)
	endpoint := appserverdaemon.NewUnixSocketEndpoint("/tmp/codex.sock")
	_, _, err := resolveDaemonCompatibility(context.Background(), &cli.RootOptions{
		ConfigOverrides: []string{"features.api_key_model_discovery=false"},
	}, endpoint, false, nil, nil)
	if err == nil || len(harness.restartCalls) != 0 {
		t.Fatalf("unmanaged restart error = %v, calls = %#v", err, harness.restartCalls)
	}

	harness = &daemonRecoveryTestHarness{
		read: func() (*daemonCompatibilityReadback, error) {
			return nil, errDaemonCompatibilityConfig
		},
		managed:     true,
		interactive: true,
		runResult:   onboarding.DaemonRecoveryResult{Choice: onboarding.DaemonRecoveryRestart},
	}
	harness.install(t)
	_, _, err = resolveDaemonCompatibility(context.Background(), &cli.RootOptions{}, endpoint, false, nil, nil)
	if err == nil || len(harness.restartCalls) != 0 {
		t.Fatalf("non-restartable error = %v, calls = %#v", err, harness.restartCalls)
	}
}

// TestResolveDaemonCompatibilityRestartFailureLikeRust covers a failed restart:
// its error (with the --no-daemon guidance) is fatal and compatibility is not
// re-checked.
func TestResolveDaemonCompatibilityRestartFailureLikeRust(t *testing.T) {
	restartErr := errors.New("restart failed\n" + daemonFailureHint)
	harness := &daemonRecoveryTestHarness{
		read:        daemonRecoveryMismatchReadback,
		managed:     true,
		interactive: true,
		runResult:   onboarding.DaemonRecoveryResult{Choice: onboarding.DaemonRecoveryRestart},
		restartErr:  restartErr,
	}
	harness.install(t)
	_, _, err := resolveDaemonCompatibility(context.Background(), &cli.RootOptions{
		ConfigOverrides: []string{"features.api_key_model_discovery=false"},
	}, appserverdaemon.NewUnixSocketEndpoint("/tmp/codex.sock"), false, nil, nil)
	if !errors.Is(err, restartErr) {
		t.Fatalf("error = %v, want the restart error", err)
	}
	if harness.reads != 1 {
		t.Fatalf("compatibility reads = %d, want no re-check after a failed restart", harness.reads)
	}
}
