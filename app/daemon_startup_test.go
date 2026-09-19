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

	if err := runAgentsCommandWithIO(context.Background(), &cli.AgentsOptions{NoDaemon: true}, nil, strings.NewReader(""), io.Discard); err == nil || !strings.Contains(err.Error(), "cannot be used with codex agents") {
		t.Fatalf("agents --no-daemon error = %v", err)
	}

	if err := runSessionQueue(&cli.QueueOptions{
		Thread:  "thread-1",
		Message: "hi",
		Shared:  cli.SharedOptions{NoDaemon: true},
	}, nil, io.Discard); err == nil || !strings.Contains(err.Error(), "cannot be used with codex queue") {
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

	started := 0
	daemonAutoStartFeature = func() bool { return true }
	daemonAutoStartStart = func() (string, error) {
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
	daemonAutoStartStart = func() (string, error) {
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
	daemonAutoStartFeature = func() bool { return false }
	daemonAutoStartStart = func() (string, error) {
		t.Fatal("an unopted launch must not start the daemon")
		return "", nil
	}
	if endpoint, err := interactiveDaemonEndpoint(&cli.RootOptions{}); err != nil || endpoint != nil {
		t.Fatalf("unopted launch = %#v, err = %v; want embedded", endpoint, err)
	}
}
