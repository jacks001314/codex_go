package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/remotecontrol"
)

// TestRemoteControlUsesManagedDaemonLikeRust pins Rust #50803
// (8f82b8a31c, cli/src/remote_control_cmd.rs::run's bare-command branch): a
// plain `codex remote-control` starts or reuses the managed daemon, enables
// remote control on the daemon's socket and reports the daemon backend, while
// `--no-daemon` (explicit or top-level), `--json`, ineligible launches, a daemon
// without a backend and a restrictive Windows launcher keep the foreground
// server.
func TestRemoteControlUsesManagedDaemonLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	restoreRemoteControlSeams(t)

	backend := appserverdaemon.BackendPID
	daemonStart := func() (*appserverdaemon.LifecycleOutput, error) {
		return &appserverdaemon.LifecycleOutput{
			Status:           appserverdaemon.StatusStarted,
			Backend:          &backend,
			SocketPath:       "/tmp/rc-daemon.sock",
			ManagedCodexPath: "/opt/codex/bin/codex",
		}, nil
	}
	daemonStartWithoutBackend := func() (*appserverdaemon.LifecycleOutput, error) {
		return &appserverdaemon.LifecycleOutput{Status: appserverdaemon.StatusStarted, SocketPath: "/tmp/rc-daemon.sock"}, nil
	}

	tests := []struct {
		name            string
		args            []string
		eligible        bool
		start           func() (*appserverdaemon.LifecycleOutput, error)
		wantDaemonStart bool
		wantForeground  bool
		wantError       string
	}{
		{
			name:            "eligible launch uses the daemon",
			args:            []string{"remote-control"},
			eligible:        true,
			start:           daemonStart,
			wantDaemonStart: true,
		},
		{
			name:           "explicit --no-daemon stays foreground",
			args:           []string{"remote-control", "--no-daemon"},
			eligible:       true,
			wantForeground: true,
		},
		{
			name:           "top-level --no-daemon stays foreground",
			args:           []string{"--no-daemon", "remote-control"},
			eligible:       true,
			wantForeground: true,
		},
		{
			name:           "--json stays foreground",
			args:           []string{"remote-control", "--json"},
			eligible:       true,
			wantForeground: true,
		},
		{
			name:           "ineligible launch stays foreground",
			args:           []string{"remote-control"},
			eligible:       false,
			wantForeground: true,
		},
		{
			name:            "daemon without a backend falls back to foreground",
			args:            []string{"remote-control"},
			eligible:        true,
			start:           daemonStartWithoutBackend,
			wantDaemonStart: true,
			wantForeground:  true,
		},
		{
			name:     "detached launch restriction falls back to foreground",
			args:     []string{"remote-control"},
			eligible: true,
			start: func() (*appserverdaemon.LifecycleOutput, error) {
				return nil, &appserverdaemon.DetachedLaunchRestrictedError{}
			},
			wantDaemonStart: true,
			wantForeground:  true,
		},
		{
			name:      "--no-daemon with a subcommand is rejected",
			args:      []string{"remote-control", "--no-daemon", "start"},
			eligible:  true,
			wantError: "`--no-daemon` cannot be used with a remote-control subcommand",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			daemonStarted := false
			foreground := false
			enabledSocket := ""
			remoteControlDaemonEligibleFor = func(root *cli.RootOptions) bool { return test.eligible }
			remoteControlDaemonStart = func() (*appserverdaemon.LifecycleOutput, error) {
				daemonStarted = true
				if test.start == nil {
					t.Fatal("the launch started the managed daemon")
				}
				return test.start()
			}
			remoteControlEnableOnSocket = func(socketPath string) (appserverdaemon.RemoteControlReadyStatus, error) {
				enabledSocket = socketPath
				return appserverdaemon.RemoteControlReadyStatus{
					Status:     remotecontrol.StatusConnected,
					ServerName: "box",
				}, nil
			}
			remoteControlRunForeground = func(ctx context.Context, opts cli.RemoteControlOptions, stdout io.Writer) error {
				foreground = true
				printRemoteControlProgress(stdout, opts.JSON, "Starting app-server with remote control enabled...")
				return nil
			}

			var stdout bytes.Buffer
			err := Run(context.Background(), test.args, strings.NewReader(""), &stdout, &bytes.Buffer{})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("Run error = %v, want %q", err, test.wantError)
				}
			} else if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			if daemonStarted != test.wantDaemonStart {
				t.Fatalf("daemon started = %v, want %v", daemonStarted, test.wantDaemonStart)
			}
			got := stdout.String()
			switch {
			case test.wantError != "":
				if foreground {
					t.Fatal("a rejected --no-daemon launch ran a server")
				}
			case test.wantForeground:
				if !foreground {
					t.Fatalf("stdout = %q, want the foreground server", got)
				}
			default:
				if foreground {
					t.Fatalf("stdout = %q, want the managed daemon", got)
				}
				if !daemonStarted {
					t.Fatal("the launch did not start the managed daemon")
				}
				if enabledSocket != "/tmp/rc-daemon.sock" {
					t.Fatalf("enable socket = %q, want the daemon's control socket", enabledSocket)
				}
				if !strings.Contains(got, "Starting app-server daemon with remote control enabled...") {
					t.Fatalf("stdout = %q, want the daemon progress line", got)
				}
				if !strings.Contains(got, "This machine is available for remote control as box.") {
					t.Fatalf("stdout = %q, want the daemon readiness line", got)
				}
			}
		})
	}
}

// TestRemoteControlDaemonEligibilityLikeRust pins Rust #50803's daemon_eligible
// inputs: raw configuration overrides (`-c`, `--enable`, `--disable`), an
// executor selection, workload identity, an elevated launcher, a disabled
// daemon_auto_start feature and a Windows-mounted WSL CODEX_HOME all keep the
// launch on the foreground path.
func TestRemoteControlDaemonEligibilityLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	unsetEnv(t, appserver.CodexExecServerURLEnvVar)
	unsetEnv(t, auth.OpenAIFederationRuleIDEnv)
	unsetEnv(t, auth.OpenAIIdentityTokenFileEnv)

	if !remoteControlDaemonEligible(nil) {
		t.Fatal("a default launch is not daemon-eligible")
	}
	if !remoteControlDaemonEligible(&cli.RootOptions{}) {
		t.Fatal("a launch without overrides is not daemon-eligible")
	}
	for _, root := range []*cli.RootOptions{
		{ConfigOverrides: []string{"model=gpt-5"}},
		{EnableFeatures: []string{"web_search"}},
		{DisableFeatures: []string{"web_search"}},
	} {
		if remoteControlDaemonEligible(root) {
			t.Fatalf("launch with configuration overrides is daemon-eligible: %+v", root)
		}
	}
	t.Setenv(appserver.CodexExecServerURLEnvVar, "http://127.0.0.1:9")
	if remoteControlDaemonEligible(nil) {
		t.Fatal("an executor selection is daemon-eligible")
	}
	t.Setenv(auth.OpenAIFederationRuleIDEnv, "rule-one")
	unsetEnv(t, appserver.CodexExecServerURLEnvVar)
	if remoteControlDaemonEligible(nil) {
		t.Fatal("workload identity is daemon-eligible")
	}
	unsetEnv(t, auth.OpenAIFederationRuleIDEnv)

	writeDaemonAutoStartConfig(t, codexHome, false)
	if remoteControlDaemonEligible(nil) {
		t.Fatal("a launch with daemon_auto_start disabled is daemon-eligible")
	}

	previousWSL := daemonWSLDrvfsDetector
	t.Cleanup(func() { daemonWSLDrvfsDetector = previousWSL })
	daemonWSLDrvfsDetector = func(string) bool { return true }
	writeDaemonAutoStartConfig(t, codexHome, true)
	if remoteControlDaemonEligible(nil) {
		t.Fatal("a WSL DrvFS CODEX_HOME is daemon-eligible")
	}
}

func writeDaemonAutoStartConfig(t *testing.T, codexHome string, enabled bool) {
	t.Helper()
	value := "false"
	if enabled {
		value = "true"
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("[features]\ndaemon_auto_start = "+value+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	previous, present := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(name, previous)
			return
		}
		_ = os.Unsetenv(name)
	})
}

func restoreRemoteControlSeams(t *testing.T) {
	t.Helper()
	previousEligible := remoteControlDaemonEligibleFor
	previousStart := remoteControlDaemonStart
	previousEnable := remoteControlEnableOnSocket
	previousForeground := remoteControlRunForeground
	t.Cleanup(func() {
		remoteControlDaemonEligibleFor = previousEligible
		remoteControlDaemonStart = previousStart
		remoteControlEnableOnSocket = previousEnable
		remoteControlRunForeground = previousForeground
	})
}
