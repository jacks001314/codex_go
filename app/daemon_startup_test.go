package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
)

// TestDaemonConfigExclusionOverridesLikeRust mirrors Rust
// daemon_startup_tests::audited_overrides_allow_daemon_without_allowing_arbitrary_config:
// only the unstable-features-warning switch and audited feature keys may be set
// on a launch that reuses the shared background server, and no shared-server
// feature may be disabled.
func TestDaemonConfigExclusionOverridesLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		raw      string
		eligible bool
	}{
		{"features.worktrees=true", true},
		{"features.worktrees=false", true},
		{"features.transcript_v2=true", true},
		{"features.transcript_v2=false", true},
		{"features.app_server_websocket_compression=true", true},
		{"features.app_server_websocket_compression=false", true},
		{"features={worktrees=true}", true},
		{"features={worktrees=true,api_key_model_discovery=false}", false},
		{"features.auth_elicitation=false", false},
		{"features.code_mode_host=false", false},
		{"features.mcp_oauth_refresh_coordination=false", false},
		{"suppress_unstable_features_warning=true", true},
		{"suppress_unstable_features_warning='true'", false},
		{"features={worktrees=true,shell_tool=false}", false},
		{"features.shell_tool=false", false},
		{"features.worktrees.enabled=true", false},
		{"features={}", false},
		{"model='test'", false},
	} {
		root := &cli.RootOptions{ConfigOverrides: []string{testCase.raw}}
		exclusion := daemonConfigExclusion(root)
		if eligible := exclusion == ""; eligible != testCase.eligible {
			t.Fatalf("daemonConfigExclusion(%s) = %q, want eligible=%v", testCase.raw, exclusion, testCase.eligible)
		}
	}
}

// TestDaemonConfigExclusionFeatureFlagsLikeRust covers the --enable/--disable/
// --search spellings, which Rust folds into the same override list.
func TestDaemonConfigExclusionFeatureFlagsLikeRust(t *testing.T) {
	if exclusion := daemonConfigExclusion(&cli.RootOptions{EnableFeatures: []string{"worktrees"}}); exclusion != "" {
		t.Fatalf("--enable worktrees = %q, want eligible", exclusion)
	}
	if exclusion := daemonConfigExclusion(&cli.RootOptions{DisableFeatures: []string{"auth_elicitation"}}); exclusion == "" {
		t.Fatal("--disable auth_elicitation must be ineligible")
	}
	if exclusion := daemonConfigExclusion(&cli.RootOptions{DisableFeatures: []string{"worktrees"}}); exclusion != "" {
		t.Fatalf("--disable worktrees = %q, want eligible", exclusion)
	}
	if exclusion := daemonConfigExclusion(&cli.RootOptions{Shared: cli.SharedOptions{Search: true}}); exclusion == "" {
		t.Fatal("--search must be ineligible")
	}
}

// TestDaemonAutoStartPreservesSavedFeatureSettingsLikeRust covers Rust #55442:
// automatic startup calls `start_with_features(&Default::default())`, so the
// daemon keeps its saved settings and the launch's explicit toggles are only
// applied by a confirmed recovery restart.
func TestDaemonAutoStartPreservesSavedFeatureSettingsLikeRust(t *testing.T) {
	for _, root := range []*cli.RootOptions{
		nil,
		{},
		{ConfigOverrides: []string{"features.code_mode_host=true", "features.auth_elicitation=false"}},
		{EnableFeatures: []string{"mcp_oauth_refresh_coordination"}},
	} {
		if got := daemonAutoStartFeatures(root); got != nil {
			t.Fatalf("daemonAutoStartFeatures(%#v) = %#v, want nil", root, got)
		}
	}
}

// FeatureOverridesEqualForTest compares override sets without importing the
// daemon package just for one assertion.
func FeatureOverridesEqualForTest(a map[string]bool, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for name, enabled := range a {
		if other, ok := b[name]; !ok || other != enabled {
			return false
		}
	}
	return true
}

// TestDaemonStartupExclusionLikeRust mirrors Rust daemon_startup::exclusion.
func TestDaemonStartupExclusionLikeRust(t *testing.T) {
	t.Setenv(appserver.CodexExecServerURLEnvVar, "")
	t.Setenv("CODEX_WORKLOAD_IDENTITY", "")
	t.Setenv("CODEX_WORKLOAD_IDENTITY_FILE", "")
	for _, testCase := range []struct {
		name           string
		root           *cli.RootOptions
		agentsOverview bool
		want           string
	}{
		{"plain launch", &cli.RootOptions{}, false, ""},
		{"no-daemon", &cli.RootOptions{Shared: cli.SharedOptions{NoDaemon: true}}, false, "--no-daemon"},
		// Rust #46529: the worktree configuration is resolved before daemon
		// selection, so --worktree stays eligible for the shared server;
		// --no-daemon still wins over it.
		{"worktree", &cli.RootOptions{Shared: cli.SharedOptions{Worktree: true}}, false, ""},
		{"no-daemon wins over worktree", &cli.RootOptions{Shared: cli.SharedOptions{NoDaemon: true, Worktree: true}}, false, "--no-daemon"},
		{"oss", &cli.RootOptions{Shared: cli.SharedOptions{OSS: true}}, false, "--oss"},
		{"local provider", &cli.RootOptions{Shared: cli.SharedOptions{OSSProvider: "ollama"}}, false, "--oss"},
		{"profile", &cli.RootOptions{Shared: cli.SharedOptions{Profile: "work"}}, false, "--profile"},
		{"strict config", &cli.RootOptions{StrictConfig: true}, false, "--strict-config"},
		{"bypass hook trust", &cli.RootOptions{Shared: cli.SharedOptions{DangerouslyBypassHookTrust: true}}, false, "--dangerously-bypass-hook-trust"},
		{"audited override", &cli.RootOptions{ConfigOverrides: []string{"features.worktrees=true"}}, false, ""},
		{"arbitrary override", &cli.RootOptions{ConfigOverrides: []string{"model='test'"}}, false, "command-line configuration overrides (-c, --enable, --disable, or --search)"},
		// The agents overview is never excluded by the launch profile: it always
		// uses the shared server (Rust `cli.agents_overview => None`).
		{"agents overview ignores the profile exclusion", &cli.RootOptions{Shared: cli.SharedOptions{Profile: "work"}}, true, ""},
		{"agents overview still honors no-daemon", &cli.RootOptions{Shared: cli.SharedOptions{NoDaemon: true}}, true, "--no-daemon"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := daemonStartupExclusion(testCase.root, testCase.agentsOverview); got != testCase.want {
				t.Fatalf("daemonStartupExclusion() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestDaemonStartupExclusionEnvironmentLikeRust covers the two environment
// inputs: executor selection and workload identity.
func TestDaemonStartupExclusionEnvironmentLikeRust(t *testing.T) {
	t.Setenv(appserver.CodexExecServerURLEnvVar, "ws://127.0.0.1:1")
	if got := daemonStartupExclusion(&cli.RootOptions{}, false); !strings.Contains(got, "CODEX_EXEC_SERVER_URL") {
		t.Fatalf("executor selection exclusion = %q", got)
	}
	t.Setenv(appserver.CodexExecServerURLEnvVar, "")
	t.Setenv(auth.OpenAIFederationRuleIDEnv, "rule-1")
	if got := daemonStartupExclusion(&cli.RootOptions{}, false); got != "workload identity" {
		t.Fatalf("workload identity exclusion = %q, want workload identity", got)
	}
}

// TestLocalDaemonEndpointForLaunchLikeRust mirrors Rust
// app_server_target_for_launch: an explicit remote wins, an eligible launch
// targets a reachable local daemon, and an unreachable daemon or ineligible
// launch stays embedded.
func TestLocalDaemonEndpointForLaunchLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	socketPath := defaultLocalDaemonSocketPath()
	if strings.TrimSpace(socketPath) == "" {
		t.Fatal("default daemon socket path is empty")
	}
	reachable := func(string) error { return nil }
	unreachable := func(string) error { return errors.New("connection refused") }

	endpoint := localDaemonEndpointForLaunchWithProbe(&cli.RootOptions{}, false, socketPath, reachable)
	if endpoint == nil || endpoint.Kind != appserverdaemon.RemoteEndpointUnixSocket {
		t.Fatalf("eligible reachable daemon endpoint = %#v, want a unix-socket endpoint", endpoint)
	}
	if got := localDaemonEndpointForLaunchWithProbe(&cli.RootOptions{}, false, "", reachable); got != nil {
		t.Fatalf("daemon endpoint without a socket = %#v, want embedded", got)
	}
	if got := localDaemonEndpointForLaunchWithProbe(&cli.RootOptions{}, false, socketPath, unreachable); got != nil {
		t.Fatalf("unreachable daemon endpoint = %#v, want embedded fallback", got)
	}
	for name, root := range map[string]*cli.RootOptions{
		"--no-daemon":  {Shared: cli.SharedOptions{NoDaemon: true}},
		"--oss":        {Shared: cli.SharedOptions{OSS: true}},
		"--profile":    {Shared: cli.SharedOptions{Profile: "work"}},
		"strict":       {StrictConfig: true},
		"bad override": {ConfigOverrides: []string{"model='test'"}},
	} {
		if got := localDaemonEndpointForLaunchWithProbe(root, false, socketPath, reachable); got != nil {
			t.Fatalf("%s reused a daemon: %#v", name, got)
		}
	}
	// Rust #46529: a worktree launch may attach to an existing daemon.
	worktree := &cli.RootOptions{Shared: cli.SharedOptions{Worktree: true}}
	if got := localDaemonEndpointForLaunchWithProbe(worktree, false, socketPath, reachable); got == nil ||
		got.Kind != appserverdaemon.RemoteEndpointUnixSocket {
		t.Fatalf("--worktree endpoint = %#v, want the existing daemon", got)
	}
	// The default probe reports no daemon for a socket nothing listens on.
	if got := localDaemonEndpointForLaunch(&cli.RootOptions{}, false, socketPath); got != nil {
		t.Fatalf("default probe accepted a missing daemon: %#v", got)
	}
}

// TestNoDaemonRejectionsLikeRust mirrors Rust #46088's incompatible command
// combinations.
func TestNoDaemonRejectionsLikeRust(t *testing.T) {
	var stderr bytes.Buffer
	err := runInteractive(context.Background(), &cli.RootOptions{
		Remote: "unix:///tmp/codex.sock",
		Shared: cli.SharedOptions{NoDaemon: true},
	}, strings.NewReader(""), io.Discard, &stderr)
	if err == nil {
		t.Fatal("interactive --no-daemon --remote unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), "--no-daemon cannot be used with --remote.") {
		t.Fatalf("interactive rejection = %q", stderr.String())
	}

	// 0.166 rejects --no-daemon for the agents overview outright, and the
	// rejection is printed as an ERROR line before the remote combination is
	// even considered.
	var agentsStderr strings.Builder
	if err := runAgentsCommandWithIO(context.Background(), &cli.AgentsOptions{
		NoDaemon: true,
		Remote:   "unix:///tmp/codex.sock",
	}, nil, strings.NewReader(""), io.Discard, &agentsStderr); err == nil || !strings.Contains(agentsStderr.String(), "cannot be used with codex agents") {
		t.Fatalf("agents --no-daemon error = %v stderr = %q", err, agentsStderr.String())
	}

	if err := runSessionQueue(&cli.QueueOptions{
		Thread:  "thread-1",
		Message: "hi",
		Shared:  cli.SharedOptions{NoDaemon: true},
	}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "cannot be used with codex queue") {
		t.Fatalf("queue --no-daemon error = %v", err)
	}

	// The session commands share the interactive validation.
	_, err = resolveSessionRemoteEndpoint(&cli.SessionOptions{
		Target: "thread-1",
		Remote: "unix:///tmp/codex.sock",
		Shared: cli.SharedOptions{NoDaemon: true},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "--no-daemon cannot be used with --remote.") {
		t.Fatalf("session --no-daemon --remote error = %v", err)
	}
	// An explicit remote without --no-daemon still resolves.
	if _, err := resolveSessionRemoteEndpoint(&cli.SessionOptions{Target: "thread-1", Remote: "unix:///tmp/codex.sock"}, nil); err != nil {
		t.Fatalf("session --remote error = %v", err)
	}
}

// TestInteractiveDaemonAutoStartLikeRust mirrors Rust #46117: with
// `features.daemon_auto_start` enabled the launch starts the shared server and
// requires a successful connection, surfacing the --no-daemon guidance instead
// of silently falling back to embedded mode. Without the opt-in the launch only
// reuses a daemon that already answers.
func TestInteractiveDaemonAutoStartLikeRust(t *testing.T) {
	originalFeature := daemonAutoStartFeature
	originalStart := daemonAutoStartStart
	t.Cleanup(func() {
		daemonAutoStartFeature = originalFeature
		daemonAutoStartStart = originalStart
	})
	// Discovery must not see a daemon running under the developer's real home.
	t.Setenv("CODEX_HOME", t.TempDir())

	started := 0
	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) {
		started++
		return "/tmp/auto-started.sock", nil
	}
	endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{})
	if err != nil {
		t.Fatalf("auto-start endpoint error = %v", err)
	}
	if endpoint == nil || endpoint.Kind != appserverdaemon.RemoteEndpointUnixSocket || endpoint.SocketPath != "/tmp/auto-started.sock" {
		t.Fatalf("auto-start endpoint = %#v", endpoint)
	}
	if started != 1 {
		t.Fatalf("daemon starts = %d, want 1", started)
	}

	// An ineligible launch never starts the server.
	endpoint, err = interactiveDaemonEndpoint(&cli.RootOptions{Shared: cli.SharedOptions{NoDaemon: true}})
	if err != nil || endpoint != nil {
		t.Fatalf("--no-daemon auto-start endpoint = %#v, err = %v", endpoint, err)
	}
	if started != 1 {
		t.Fatalf("--no-daemon started the daemon: %d starts", started)
	}

	// A failed start is fatal with the guidance, without an embedded fallback.
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) {
		started++
		return "", errors.New("managed standalone Codex install not found")
	}
	endpoint, err = interactiveDaemonEndpoint(&cli.RootOptions{})
	if err == nil || endpoint != nil {
		t.Fatalf("failed auto-start returned endpoint=%#v err=%v, want a fatal error", endpoint, err)
	}
	if !strings.Contains(err.Error(), daemonFailureHint) {
		t.Fatalf("failed auto-start error = %v, want the --no-daemon guidance", err)
	}

	// Without the opt-in the launch only reuses an already-running daemon.
	daemonAutoStartFeature = func(*cli.RootOptions) bool { return false }
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) {
		t.Fatal("an unopted launch must not start the daemon")
		return "", nil
	}
	if endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{}); err != nil || endpoint != nil {
		t.Fatalf("unopted launch = %#v, err = %v; want embedded", endpoint, err)
	}
}

// TestDaemonAutoStartAllowsWorktreeLikeRust mirrors Rust #46529: the worktree
// configuration is resolved before daemon selection, so a --worktree launch
// takes the same automatic-start path as any other launch. --no-daemon still
// disables automatic startup for every launch.
func TestDaemonAutoStartAllowsWorktreeLikeRust(t *testing.T) {
	originalFeature := daemonAutoStartFeature
	originalStart := daemonAutoStartStart
	originalReason := daemonAutoStartExclusionReason
	t.Cleanup(func() {
		daemonAutoStartFeature = originalFeature
		daemonAutoStartStart = originalStart
		daemonAutoStartExclusionReason = originalReason
	})
	t.Setenv("CODEX_HOME", t.TempDir())
	daemonAutoStartExclusionReason = ""

	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	if !daemonAutoStartRequested(&cli.RootOptions{}) {
		t.Fatal("a plain launch must request automatic startup")
	}
	worktree := &cli.RootOptions{Shared: cli.SharedOptions{Worktree: true}}
	if !daemonAutoStartRequested(worktree) {
		t.Fatal("a --worktree launch must request automatic startup")
	}
	// --no-daemon still disables automatic startup for any launch.
	if daemonAutoStartRequested(&cli.RootOptions{Shared: cli.SharedOptions{NoDaemon: true, Worktree: true}}) {
		t.Fatal("a --no-daemon launch must not request automatic startup")
	}

	starts := 0
	daemonAutoStartStart = func(root *cli.RootOptions) (string, error) {
		starts++
		if root == nil || !root.Shared.Worktree {
			t.Fatalf("auto-start root = %#v, want the --worktree launch", root)
		}
		return t.TempDir() + "/codex.sock", nil
	}
	endpoint, err := interactiveDaemonEndpoint(worktree)
	if err != nil || endpoint == nil {
		t.Fatalf("--worktree endpoint = %#v, err = %v; want the started shared server", endpoint, err)
	}
	if starts != 1 {
		t.Fatalf("--worktree automatic start calls = %d, want 1", starts)
	}
	if got := interactiveDaemonSelectionReason(worktree, endpoint, false); got != "auto_start" {
		t.Fatalf("--worktree selection reason = %q, want auto_start", got)
	}
	if got := daemonAutoStartExclusionWarning(worktree); got != "" {
		t.Fatalf("--worktree warning = %q, want none", got)
	}

	// --no-daemon keeps a worktree launch embedded and never starts the server.
	daemonAutoStartExclusionReason = ""
	if endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{Shared: cli.SharedOptions{NoDaemon: true, Worktree: true}}); err != nil || endpoint != nil {
		t.Fatalf("--no-daemon --worktree endpoint = %#v, err = %v; want embedded", endpoint, err)
	}
	if starts != 1 {
		t.Fatalf("--no-daemon --worktree automatic start calls = %d, want 1", starts)
	}
}

// TestDaemonAutoStartExclusionWarningLikeRust mirrors Rust #46117's launch
// warning: a launch that opted into auto-start but was excluded reports why the
// shared server is not used, `--no-daemon` is never reported as an exclusion,
// and an opted-out launch says nothing.
func TestDaemonAutoStartExclusionWarningLikeRust(t *testing.T) {
	originalFeature := daemonAutoStartFeature
	originalReason := daemonAutoStartExclusionReason
	t.Cleanup(func() {
		daemonAutoStartFeature = originalFeature
		daemonAutoStartExclusionReason = originalReason
	})
	t.Setenv(appserver.CodexExecServerURLEnvVar, "")

	daemonAutoStartFeature = func(*cli.RootOptions) bool { return false }
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{Shared: cli.SharedOptions{OSS: true}}); got != "" {
		t.Fatalf("unopted warning = %q, want none", got)
	}
	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{}); got != "" {
		t.Fatalf("eligible launch warning = %q, want none", got)
	}
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{Shared: cli.SharedOptions{NoDaemon: true}}); got != "" {
		t.Fatalf("--no-daemon warning = %q, want none", got)
	}
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{Remote: "unix:///tmp/codex.sock"}); got != "" {
		t.Fatalf("explicit remote warning = %q, want none", got)
	}
	want := "Running without the shared background server: --oss requires embedded mode."
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{Shared: cli.SharedOptions{OSS: true}}); got != want {
		t.Fatalf("--oss warning = %q, want %q", got, want)
	}
	want = "Running without the shared background server: --profile requires embedded mode."
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{Shared: cli.SharedOptions{Profile: "work"}}); got != want {
		t.Fatalf("--profile warning = %q, want %q", got, want)
	}
}

// TestInteractiveDaemonAutoStartFallsBackUnderRestrictiveLauncherLikeRust mirrors
// Rust #48491: when the Windows launcher forbids detaching the shared background
// server, automatic startup uses the embedded server and reports the exclusion,
// while every other launch failure stays fatal.
func TestInteractiveDaemonAutoStartFallsBackUnderRestrictiveLauncherLikeRust(t *testing.T) {
	originalFeature := daemonAutoStartFeature
	originalStart := daemonAutoStartStart
	originalReason := daemonAutoStartExclusionReason
	t.Cleanup(func() {
		daemonAutoStartFeature = originalFeature
		daemonAutoStartStart = originalStart
		daemonAutoStartExclusionReason = originalReason
	})

	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) {
		return "", &appserverdaemon.DetachedLaunchRestrictedError{}
	}
	endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{})
	if err != nil || endpoint != nil {
		t.Fatalf("restricted auto-start = %#v, err = %v; want embedded", endpoint, err)
	}
	want := "Running without the shared background server: this Windows launcher requires embedded mode."
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{}); got != want {
		t.Fatalf("restriction warning = %q, want %q", got, want)
	}

	// A different launch failure keeps the fatal --no-daemon guidance and never
	// reports the launcher exclusion.
	daemonAutoStartExclusionReason = ""
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) {
		return "", errors.New("managed standalone Codex install not found")
	}
	if _, err := interactiveDaemonEndpoint(&cli.RootOptions{}); err == nil || !strings.Contains(err.Error(), daemonFailureHint) {
		t.Fatalf("unrelated failure error = %v, want the --no-daemon guidance", err)
	}
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{}); got != "" {
		t.Fatalf("unrelated failure warning = %q, want none", got)
	}
}

// TestDaemonAutoStartSkipsWSLDrvfsHomeLikeRust mirrors Rust #50555: an
// automatic launch whose CODEX_HOME lives on a Windows-mounted WSL filesystem
// stays embedded and reports the exclusion, while other homes are unaffected.
func TestDaemonAutoStartSkipsWSLDrvfsHomeLikeRust(t *testing.T) {
	originalFeature := daemonAutoStartFeature
	originalDetector := daemonWSLDrvfsDetector
	originalReason := daemonAutoStartExclusionReason
	t.Cleanup(func() {
		daemonAutoStartFeature = originalFeature
		daemonWSLDrvfsDetector = originalDetector
		daemonAutoStartExclusionReason = originalReason
	})
	t.Setenv(appserver.CodexExecServerURLEnvVar, "")

	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	daemonWSLDrvfsDetector = func(string) bool { return true }
	daemonAutoStartExclusionReason = ""
	endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{})
	if err != nil || endpoint != nil {
		t.Fatalf("WSL DrvFS auto-start = %#v, err = %v; want embedded", endpoint, err)
	}
	want := "Running without the shared background server: a Windows-mounted WSL CODEX_HOME (DrvFS/9p) requires embedded mode."
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{}); got != want {
		t.Fatalf("WSL DrvFS warning = %q, want %q", got, want)
	}

	// A home on a normal filesystem is not excluded.
	daemonWSLDrvfsDetector = func(string) bool { return false }
	daemonAutoStartExclusionReason = ""
	if got := daemonAutoStartExclusionWarning(&cli.RootOptions{}); got != "" {
		t.Fatalf("non-DrVFS home warning = %q, want none", got)
	}
}

// TestElevatedWindowsLaunchUsesEmbeddedModeLikeRust mirrors Rust #49855: an
// elevated local Windows session selects embedded mode before daemon discovery
// or startup, reports ELEVATED_LAUNCH_WARNING, and never starts the shared
// background server, while a non-elevated launch keeps the auto-start path.
func TestElevatedWindowsLaunchUsesEmbeddedModeLikeRust(t *testing.T) {
	originalFeature := daemonAutoStartFeature
	originalStart := daemonAutoStartStart
	originalDetector := daemonElevationDetector
	originalReason := daemonAutoStartExclusionReason
	t.Cleanup(func() {
		daemonAutoStartFeature = originalFeature
		daemonAutoStartStart = originalStart
		daemonElevationDetector = originalDetector
		daemonAutoStartExclusionReason = originalReason
	})
	t.Setenv(appserver.CodexExecServerURLEnvVar, "")

	starts := 0
	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) {
		starts++
		return "", errors.New("the elevated launch must not start the shared server")
	}
	daemonAutoStartExclusionReason = ""

	// Elevated: embedded, without touching the daemon start path.
	daemonElevationDetector = func() bool { return true }
	endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{})
	if err != nil || endpoint != nil {
		t.Fatalf("elevated endpoint = %#v, err = %v; want embedded", endpoint, err)
	}
	if starts != 0 {
		t.Fatalf("elevated launch started the shared server %d times, want 0", starts)
	}
	if got := daemonStartupWarning(&cli.RootOptions{}); got != elevatedLaunchWarning {
		t.Fatalf("elevated warning = %q, want %q", got, elevatedLaunchWarning)
	}

	// Non-elevated: the auto-start path is untouched.
	daemonElevationDetector = func() bool { return false }
	daemonAutoStartStart = func(*cli.RootOptions) (string, error) {
		return "/tmp/codex-elevated-parity.sock", nil
	}
	endpoint, err = interactiveDaemonEndpoint(&cli.RootOptions{})
	if err != nil || endpoint == nil {
		t.Fatalf("non-elevated endpoint = %#v, err = %v; want the shared server", endpoint, err)
	}
	if got := daemonStartupWarning(&cli.RootOptions{}); got != "" {
		t.Fatalf("non-elevated warning = %q, want none", got)
	}
}

// TestElevatedWindowsLaunchEmbeddedWithoutAutoStartLikeRust pins that the Rust
// #49855 guard is independent of `features.daemon_auto_start`: an elevated
// session stays embedded and warns even when auto-start is disabled, so it
// cannot attach to a running shared server, while --no-daemon and the agents
// overview keep their own behavior (the `!cli.no_daemon` / `!cli.agents_overview`
// halves of the Rust guard).
func TestElevatedWindowsLaunchEmbeddedWithoutAutoStartLikeRust(t *testing.T) {
	originalFeature := daemonAutoStartFeature
	originalDetector := daemonElevationDetector
	originalReason := daemonAutoStartExclusionReason
	t.Cleanup(func() {
		daemonAutoStartFeature = originalFeature
		daemonElevationDetector = originalDetector
		daemonAutoStartExclusionReason = originalReason
	})
	t.Setenv(appserver.CodexExecServerURLEnvVar, "")

	daemonAutoStartFeature = func(*cli.RootOptions) bool { return false }
	daemonElevationDetector = func() bool { return true }
	daemonAutoStartExclusionReason = ""

	endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{})
	if err != nil || endpoint != nil {
		t.Fatalf("elevated endpoint = %#v, err = %v; want embedded", endpoint, err)
	}
	if got := daemonStartupWarning(&cli.RootOptions{}); got != elevatedLaunchWarning {
		t.Fatalf("elevated warning = %q, want %q", got, elevatedLaunchWarning)
	}

	// The elevated warning wins over an auto-start exclusion warning.
	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	daemonAutoStartExclusionReason = wslDrvfsExclusion
	if got := daemonStartupWarning(&cli.RootOptions{}); got != elevatedLaunchWarning {
		t.Fatalf("precedence warning = %q, want %q", got, elevatedLaunchWarning)
	}

	// --no-daemon is its own exclusion: no elevated warning.
	daemonAutoStartExclusionReason = ""
	noDaemon := &cli.RootOptions{Shared: cli.SharedOptions{NoDaemon: true}}
	if daemonElevatedLaunchExcluded(noDaemon, false) {
		t.Fatal("--no-daemon launch counted as the elevated exclusion")
	}
	if got := daemonStartupWarning(noDaemon); got != "" {
		t.Fatalf("--no-daemon warning = %q, want none", got)
	}

	// The agents overview keeps needing the shared server.
	if daemonElevatedLaunchExcluded(&cli.RootOptions{}, true) {
		t.Fatal("agents overview launch counted as the elevated exclusion")
	}
}
