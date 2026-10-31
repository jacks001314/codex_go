package cli

import (
	"strings"
	"testing"
)

// Mirrors Rust #42781 exec_server_defaults_preserve_local_and_noise_modes.
func TestExecServerDefaultsPreserveNoiseTransportLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"exec-server", "--listen", "stdio"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := parsed.ExecServer.RemoteTransport; got != "noise" {
		t.Fatalf("RemoteTransport = %q, want noise", got)
	}
	if parsed.ExecServer.AWSSigV4 {
		t.Fatal("--aws-sigv4 must default to false")
	}
	if got := parsed.ExecServer.AWSService; got != "execute-api" {
		t.Fatalf("AWSService = %q, want execute-api", got)
	}
}

// Mirrors Rust #42781 exec_server_parses_direct_aws_options.
func TestExecServerParsesDirectAWSOptionsLikeRust(t *testing.T) {
	parsed, err := Parse([]string{
		"exec-server",
		"--remote", "https://registry.example.com",
		"--environment-id", "env-1",
		"--remote-transport", "direct",
		"--aws-sigv4",
		"--aws-profile", "development",
		"--aws-region", "us-west-2",
		"--aws-service", "execute-api",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	options := parsed.ExecServer
	if options.RemoteTransport != "direct" || !options.AWSSigV4 {
		t.Fatalf("transport options = %+v", options)
	}
	if options.AWSProfile != "development" || options.AWSRegion != "us-west-2" || options.AWSService != "execute-api" {
		t.Fatalf("aws options = %+v", options)
	}
}

// Mirrors Rust #42781 exec_server_transport_and_aws_options_require_registration_arguments.
func TestExecServerTransportAndAWSOptionsRequireRemoteLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"exec-server", "--remote-transport", "direct", "--aws-sigv4"},
		{"exec-server", "--aws-sigv4"},
		{"exec-server", "--aws-profile", "development"},
		{"exec-server", "--aws-region", "us-west-2"},
		{"exec-server", "--aws-service", "execute-api"},
	} {
		if _, err := Parse(args); err == nil {
			t.Fatalf("%v: expected an error", args)
		}
	}
}

// Mirrors Rust #42781 exec_server_rejects_missing_or_conflicting_remote_options.
func TestExecServerRejectsMissingOrConflictingRemoteOptionsLikeRust(t *testing.T) {
	base := []string{"exec-server", "--remote", "https://registry.example.com", "--environment-id", "env-1"}
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{append(append([]string{}, base...), "--aws-sigv4"), "--remote-transport direct"},
		{append(append([]string{}, base...), "--remote-transport", "direct"), "--aws-sigv4"},
		{append(append([]string{}, base...), "--remote-transport", "direct", "--aws-sigv4", "--use-agent-identity-auth"), "conflicts with --use-agent-identity-auth"},
		{append(append([]string{}, base...), "--remote-transport", "direct", "--aws-sigv4", "--executor-credential-stdin"), "conflicts with --executor-credential-stdin"},
	} {
		_, err := Parse(testCase.args)
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%v: error = %v, want %q", testCase.args, err, testCase.want)
		}
	}
	// Go rejects the forward/--remote combination outright, which subsumes
	// Rust's direct-transport forwarding check.
	if _, err := Parse([]string{
		"exec-server", "forward", "--connect", "ws://127.0.0.1:7777",
		"--remote", "https://registry.example.com", "--environment-id", "env-1",
		"--remote-transport", "direct", "--aws-sigv4",
	}); err == nil || !strings.Contains(err.Error(), "cannot be combined with --remote") {
		t.Fatalf("direct forward error = %v", err)
	}
	if _, err := Parse([]string{"exec-server", "--remote", "https://registry.example.com", "--environment-id", "env-1", "--remote-transport", "noise"}); err != nil {
		t.Fatalf("noise transport with --remote: %v", err)
	}
}
