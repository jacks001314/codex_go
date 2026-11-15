package execserver

import (
	"context"
	"testing"

	"codex_go/network"
)

func TestExecServerTransparentTCPRuntimeOptionsLikeRust(t *testing.T) {
	parent := NewServerWithHTTPClient(nil)
	parent.SetLinuxSandboxTransparentTCP(true)
	parent.SetTcpUpstreamProxy(&network.TcpUpstreamProxy{Host: "127.0.0.1", Port: 8080})
	child := newSessionServerWithRuntimeOptions(nil, parent)
	if !child.linuxSandboxTransparentTCPValue() {
		t.Fatal("session server lost linux_sandbox_transparent_tcp")
	}
	proxy := child.tcpUpstreamProxyValue()
	if proxy == nil || proxy.Host != "127.0.0.1" || proxy.Port != 8080 {
		t.Fatalf("session server tcp upstream proxy = %#v", proxy)
	}
}

func TestMandatoryTCPUpstreamRequiresExecutorProxyLikeRust(t *testing.T) {
	server := NewServerWithHTTPClient(nil)
	server.SetTcpUpstreamProxy(&network.TcpUpstreamProxy{Host: "127.0.0.1", Port: 8080})
	_, _, _, err := server.prepareExecutorNetworkProxy(context.Background(), &ExecParams{
		EnforceManagedNetwork: true,
		ManagedNetwork:        &ManagedNetworkSandboxContext{LoopbackPorts: []uint16{3128}},
	})
	if err == nil {
		t.Fatal("prepareExecutorNetworkProxy() accepted a managed context without an executor proxy")
	}
	var failure *requestFailure
	if !asRequestFailure(err, &failure) || failure.code != -32602 {
		t.Fatalf("error = %v, want invalid params", err)
	}
	if failure.message != "a mandatory TCP upstream requires an executor-local managed proxy" {
		t.Fatalf("message = %q", failure.message)
	}
}

func asRequestFailure(err error, target **requestFailure) bool {
	failure, ok := err.(*requestFailure)
	if !ok {
		return false
	}
	*target = failure
	return true
}
