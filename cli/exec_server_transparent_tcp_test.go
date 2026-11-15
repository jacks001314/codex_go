package cli

import (
	"strings"
	"testing"
)

func TestExecServerTransparentTCPFlagsLikeRust(t *testing.T) {
	parsed, err := Parse([]string{
		"exec-server",
		"--linux-sandbox-transparent-tcp",
		"--tcp-upstream-proxy", "127.0.0.1:8080",
		"--listen", "stdio",
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !parsed.ExecServer.LinuxSandboxTransparentTCP {
		t.Fatal("LinuxSandboxTransparentTCP = false")
	}
	proxy := parsed.ExecServer.TcpUpstreamProxy
	if proxy == nil || proxy.Host != "127.0.0.1" || proxy.Port != 8080 {
		t.Fatalf("TcpUpstreamProxy = %#v", proxy)
	}
}

func TestExecServerTcpUpstreamProxyRejectsInvalidValueLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"exec-server", "--tcp-upstream-proxy", "127.0.0.1:0"},
		{"exec-server", "--tcp-upstream-proxy=missing-port"},
	} {
		if _, err := Parse(args); err == nil || !strings.Contains(err.Error(), "--tcp-upstream-proxy <HOST:PORT>") {
			t.Fatalf("Parse(%v) error = %v, want the clap value diagnostic", args, err)
		}
	}
}

func TestExecServerTcpUpstreamProxyRejectsDuplicateLikeRust(t *testing.T) {
	_, err := Parse([]string{
		"exec-server",
		"--tcp-upstream-proxy", "127.0.0.1:8080",
		"--tcp-upstream-proxy", "127.0.0.1:8081",
		"--listen", "stdio",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be used multiple times") {
		t.Fatalf("Parse() error = %v, want a duplicate-argument diagnostic", err)
	}
}
