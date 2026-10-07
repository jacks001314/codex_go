package cli

import (
	"os"
	"strings"
	"testing"
)

// unsetEnvForTest removes an environment variable for the duration of the test
// so the "unset" state can be exercised deterministically.
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	previous, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, previous)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// TestExecServerProxyPrivateIPsViaUpstreamFlagAndEnvLikeRust covers Rust #48568
// (`b8d5e3f12e`, cli/src/exec_server_command.rs):
//
//	#[arg(long, env = "CODEX_EXEC_SERVER_PROXY_PRIVATE_IPS_VIA_UPSTREAM", global = true)]
//	proxy_private_ips_via_upstream: bool,
//
// The setting defaults to disabled, the flag takes no value (clap
// ArgAction::SetTrue), and the environment variable is used only when the flag
// is absent.
func TestExecServerProxyPrivateIPsViaUpstreamFlagAndEnvLikeRust(t *testing.T) {
	unsetEnvForTest(t, execServerProxyPrivateIPsViaUpstreamEnv)

	// State 1: neither the flag nor the environment variable is set.
	parsed, err := Parse([]string{"exec-server", "--listen", "stdio"})
	if err != nil {
		t.Fatalf("Parse(exec-server) error = %v", err)
	}
	if parsed.ExecServer.ProxyPrivateIPsViaUpstream {
		t.Fatal("private-IP upstream routing enabled without the flag or env")
	}

	// The flag itself enables the routing; it stays valid beside other flags.
	parsed, err = Parse([]string{"exec-server", "--proxy-private-ips-via-upstream", "--listen", "stdio"})
	if err != nil {
		t.Fatalf("Parse(--proxy-private-ips-via-upstream) error = %v", err)
	}
	if !parsed.ExecServer.ProxyPrivateIPsViaUpstream {
		t.Fatal("flag did not enable private-IP upstream routing")
	}
	if parsed.ExecServer.Listen != "stdio" {
		t.Fatalf("listen = %q", parsed.ExecServer.Listen)
	}

	// clap defines the flag on the exec-server subcommand, so the root command
	// rejects it (a global arg only propagates downward to nested subcommands).
	if _, err := Parse([]string{"--proxy-private-ips-via-upstream", "exec-server"}); err == nil {
		t.Fatal("root-level --proxy-private-ips-via-upstream should be rejected")
	}

	// State 2: the environment variable is set to a parseable boolean.
	for _, testCase := range []struct {
		value string
		want  bool
	}{
		{value: "true", want: true},
		{value: "1", want: true},
		{value: "TRUE", want: true},
		{value: "false", want: false},
		{value: "0", want: false},
	} {
		t.Setenv(execServerProxyPrivateIPsViaUpstreamEnv, testCase.value)
		parsed, err = Parse([]string{"exec-server"})
		if err != nil {
			t.Fatalf("Parse(env=%q) error = %v", testCase.value, err)
		}
		if parsed.ExecServer.ProxyPrivateIPsViaUpstream != testCase.want {
			t.Fatalf("env=%q: PrivateIPsViaUpstream = %v, want %v", testCase.value, parsed.ExecServer.ProxyPrivateIPsViaUpstream, testCase.want)
		}
	}

	// The flag wins over a false environment value (clap: CLI beats env).
	t.Setenv(execServerProxyPrivateIPsViaUpstreamEnv, "false")
	parsed, err = Parse([]string{"exec-server", "--proxy-private-ips-via-upstream"})
	if err != nil {
		t.Fatalf("Parse(flag + env=false) error = %v", err)
	}
	if !parsed.ExecServer.ProxyPrivateIPsViaUpstream {
		t.Fatal("flag did not override env=false")
	}

	// State 3: the environment variable is set but unparseable (empty string is
	// the same failure mode as the sibling exit-on-stdin-close variable).
	for _, value := range []string{"", "not-a-bool"} {
		t.Setenv(execServerProxyPrivateIPsViaUpstreamEnv, value)
		if _, err := Parse([]string{"exec-server"}); err == nil ||
			!strings.Contains(err.Error(), execServerProxyPrivateIPsViaUpstreamEnv) {
			t.Fatalf("env=%q: error = %v, want a %s parse error", value, err, execServerProxyPrivateIPsViaUpstreamEnv)
		}
	}
}
