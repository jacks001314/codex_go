package cli

import (
	"testing"
)

// TestExecServerExecutorCredentialStdinFlagLikeRust covers Rust #54434
// (`16e578dd1d`, codex-rs/cli/src/exec_server_command.rs):
//
//	#[arg(long = "executor-credential-stdin", requires = "exec_server_remote",
//	      conflicts_with_all = ["use_agent_identity_auth", "aws_sigv4"])]
//	executor_credential_stdin: bool,
//
// and codex-rs/cli/src/exec_server_args_tests.rs: the flag defaults to off,
// `--remote <registry> --environment-id <id>` may combine it with
// `--exit-on-stdin-close`, a bare flag is rejected for the missing registry, and
// it conflicts with the account-style provider. The Go port has no
// `--aws-sigv4` flag, so only the Agent Identity conflict can be exercised.
func TestExecServerExecutorCredentialStdinFlagLikeRust(t *testing.T) {
	// exec_server_defaults_preserve_local_and_noise_modes: off by default.
	parsed, err := Parse([]string{"exec-server", "--listen", "stdio"})
	if err != nil {
		t.Fatalf("Parse(exec-server) error = %v", err)
	}
	if parsed.ExecServer.ExecutorCredentialStdin {
		t.Fatal("ExecutorCredentialStdin = true without the flag")
	}

	// exec_server_parses_executor_credential_options.
	registry := []string{"exec-server", "--remote", "https://registry.example.com", "--environment-id", "env-1"}
	for _, extra := range [][]string{
		{"--executor-credential-stdin"},
		{"--executor-credential-stdin", "--exit-on-stdin-close"},
	} {
		args := append(append([]string{}, registry...), extra...)
		parsed, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v) error = %v", extra, err)
		}
		if !parsed.ExecServer.ExecutorCredentialStdin {
			t.Fatalf("Parse(%v): ExecutorCredentialStdin = false, want true", extra)
		}
		wantExitOnStdinClose := len(extra) == 2
		if parsed.ExecServer.ExitOnStdinClose != wantExitOnStdinClose {
			t.Fatalf("Parse(%v): ExitOnStdinClose = %v, want %v", extra, parsed.ExecServer.ExitOnStdinClose, wantExitOnStdinClose)
		}
	}

	// exec_server_transport_and_aws_options_require_registration_arguments: the
	// scoped credential needs the registry endpoint.
	if _, err := Parse([]string{"exec-server", "--executor-credential-stdin"}); err == nil ||
		err.Error() != "--executor-credential-stdin requires --remote" {
		t.Fatalf("bare --executor-credential-stdin error = %v", err)
	}

	// exec_server_rejects_missing_or_conflicting_remote_options: scoped
	// credentials conflict with the account identity provider.
	_, err = Parse(append(append([]string{}, registry...), "--executor-credential-stdin", "--use-agent-identity-auth"))
	if err == nil || err.Error() != "--executor-credential-stdin conflicts with --use-agent-identity-auth" {
		t.Fatalf("scoped + --use-agent-identity-auth error = %v", err)
	}
}

// TestExecServerExecutorCredentialStdinInCompletionLikeRust mirrors the help
// snapshot update in Rust #54434
// (codex-rs/cli/src/snapshots/codex__exec_server_args_tests__exec_server_help_documents_remote_options.snap),
// which documents `--executor-credential-stdin` as part of the visible
// exec-server surface.
func TestExecServerExecutorCredentialStdinInCompletionLikeRust(t *testing.T) {
	words := flattenedCompletionWords(codexCompletionSpec())
	if !hasCompletionWord(words, "--executor-credential-stdin") {
		t.Fatalf("completion words missing %q", "--executor-credential-stdin")
	}
	if !hasCompletionWord(words, "--use-agent-identity-auth") {
		t.Fatalf("completion words missing %q", "--use-agent-identity-auth")
	}
}
