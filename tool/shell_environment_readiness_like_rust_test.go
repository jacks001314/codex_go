package tool

import (
	"context"
	"strings"
	"testing"
)

// TestResolveUnifiedExecEnvironmentMessagesLikeRust covers Rust #50741/#50962's
// `resolve_tool_environment`: with `stable_environment_tools` off a selected but
// unusable environment keeps the tool-specific unavailable message, while the
// feature turns the same case into the shared waiting message.
func TestResolveUnifiedExecEnvironmentMessagesLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name    string
		check   *UnifiedExecEnvironmentCheck
		request string
		want    string
	}{
		{
			name:    "default off, primary",
			check:   &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}},
			request: "",
			want:    UnifiedExecUnavailableMessage,
		},
		{
			name:    "default off, selected but starting",
			check:   &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}},
			request: "remote",
			want:    "unknown turn environment id `remote`",
		},
		{
			name:    "default off, unselected",
			check:   &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}},
			request: "other",
			want:    "unknown turn environment id `other`",
		},
		{
			name:    "opted in, primary",
			check:   &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}, StableEnvironmentTools: true},
			request: "",
			want:    UnifiedUnavailableEnvironmentMessage,
		},
		{
			name:    "opted in, selected but starting",
			check:   &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}, StableEnvironmentTools: true},
			request: "remote",
			want:    UnifiedUnavailableEnvironmentMessage,
		},
		{
			name:    "opted in, unselected",
			check:   &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}, StableEnvironmentTools: true},
			request: "other",
			want:    "unknown turn environment id `other`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := NewShellExecutor(&ShellExecutorOptions{EnvironmentCheck: tc.check})
			environment, err := executor.resolveUnifiedExecEnvironment(tc.request)
			if environment != nil {
				t.Fatalf("resolve(%q) environment = %#v, want nil", tc.request, environment)
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("resolve(%q) error = %v, want %q", tc.request, err, tc.want)
			}
		})
	}
}

// TestResolveUnifiedExecEnvironmentFallsBackToImplicitLocalLikeRust keeps the
// implicit local executor usable: the turn resolves one usable environment and
// no executor record, which is Go's spelling of Rust's selected local
// environment.
func TestResolveUnifiedExecEnvironmentFallsBackToImplicitLocalLikeRust(t *testing.T) {
	check := &UnifiedExecEnvironmentCheck{ReadyEnvironmentCount: 1}
	executor := NewShellExecutor(&ShellExecutorOptions{EnvironmentCheck: check})
	for _, request := range []string{"", "local"} {
		environment, err := executor.resolveUnifiedExecEnvironment(request)
		if err != nil || environment != nil {
			t.Fatalf("resolve(%q) = %#v, %v; want the implicit local environment", request, environment, err)
		}
	}
	if _, err := executor.resolveUnifiedExecEnvironment("remote"); err == nil || err.Error() != "unknown turn environment id `remote`" {
		t.Fatalf("resolve(remote) error = %v, want unknown turn environment id", err)
	}
}

// TestWriteStdinEnvironmentCheckFollowsFeatureLikeRust covers Rust #50962's
// write_stdin change: the readiness check only runs while the default-off
// `stable_environment_tools` feature is on, so an in-flight session stays
// writable on the default path.
func TestWriteStdinEnvironmentCheckFollowsFeatureLikeRust(t *testing.T) {
	for _, stable := range []bool{false, true} {
		executor := NewWriteStdinExecutorWithOptions(&WriteStdinOptions{
			Manager: NewUnifiedExecManager(),
			EnvironmentCheck: &UnifiedExecEnvironmentCheck{
				SelectedEnvironmentIDs: []string{"remote"},
				ReadyEnvironmentCount:  0,
				StableEnvironmentTools: stable,
			},
		})
		invocation := &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"session_id":1}`}}
		_, err := executor.Execute(context.Background(), invocation)
		if err == nil {
			t.Fatalf("write_stdin with no session succeeded (stable_environment_tools=%v)", stable)
		}
		if got := strings.Contains(err.Error(), UnifiedUnavailableEnvironmentMessage); got != stable {
			t.Fatalf("write_stdin error = %v with stable_environment_tools=%v; waiting message present = %v", err, stable, got)
		}
	}
}

// TestShellExecutorWithoutReadinessFactsKeepsLegacyResolutionLikeRust pins the
// pre-#50962 behavior hosts without a readiness source keep: no configured
// environment runs on the implicit local executor.
func TestShellExecutorWithoutReadinessFactsKeepsLegacyResolutionLikeRust(t *testing.T) {
	executor := NewShellExecutor(&ShellExecutorOptions{})
	environment, err := executor.resolveUnifiedExecEnvironment("")
	if err != nil || environment != nil {
		t.Fatalf("resolve(\"\") = %#v, %v with no readiness facts", environment, err)
	}
	if _, err := executor.resolveUnifiedExecEnvironment("remote"); err == nil {
		t.Fatalf("resolve(remote) should be unknown without readiness facts")
	}
}

// TestShellExecutorRefusesLocalFallbackWithoutUsableEnvironmentLikeRust covers
// the execution half of Rust #50741/#50962: when the host reports that no
// selected environment is usable yet, a command fails with the environment
// error instead of silently running on this process (Rust's turn environments
// never fall back from an unready selection to the local executor).
func TestShellExecutorRefusesLocalFallbackWithoutUsableEnvironmentLikeRust(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{ExitCode: 0}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote"},
		},
	})
	output, err := executor.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo hi"}`},
	})
	if output != nil || err == nil || err.Error() != UnifiedExecUnavailableMessage {
		t.Fatalf("Execute() = %#v, %v; want %q", output, err, UnifiedExecUnavailableMessage)
	}
	if runner.request != nil {
		t.Fatalf("command ran locally despite no usable environment: %#v", runner.request)
	}

	// The feature keeps the call environment-checked but reports the shared
	// waiting message instead of the tool-specific one.
	executor = NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote"},
			StableEnvironmentTools: true,
		},
	})
	_, err = executor.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo hi"}`},
	})
	if err == nil || err.Error() != UnifiedUnavailableEnvironmentMessage {
		t.Fatalf("Execute() error = %v, want %q", err, UnifiedUnavailableEnvironmentMessage)
	}

	// A usable environment still runs the command.
	executor = NewShellExecutor(&ShellExecutorOptions{
		Runner:                  runner,
		UnifiedExecEnvironments: []UnifiedExecEnvironment{{ID: "remote", CWD: "/workspace", ExecServerURL: "ws://127.0.0.1:1"}},
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote"},
			ReadyEnvironmentCount:  1,
		},
	})
	if _, err := executor.Execute(context.Background(), &Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo hi"}`},
	}); err != nil {
		t.Fatalf("Execute() with a usable environment error = %v", err)
	}
}
