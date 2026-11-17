package tool

import (
	"runtime"
	"testing"

	"codex_go/execserver"
	"codex_go/network"
)

// TestUnifiedExecTransparentTCPTrimLikeRust pins Rust #61851
// exec_server_params_for_request: a local managed context can only capture on a
// sandboxed Linux launch, while a remote proxy only needs a sandboxed launch.
func TestUnifiedExecTransparentTCPTrimLikeRust(t *testing.T) {
	newInputs := func() (*execserver.ManagedNetworkSandboxContext, *execserver.RemoteNetworkProxyLaunchConfig) {
		return &execserver.ManagedNetworkSandboxContext{TransparentTCP: true},
			&execserver.RemoteNetworkProxyLaunchConfig{Proxy: execserver.RemoteNetworkProxyConfig{TransparentTCP: true}}
	}

	managed, proxy := newInputs()
	managed, proxy = trimUnifiedExecTransparentTCP(true, managed, proxy)
	wantManaged := runtime.GOOS == "linux"
	if managed.TransparentTCP != wantManaged {
		t.Fatalf("sandboxed managed transparent = %v, want %v", managed.TransparentTCP, wantManaged)
	}
	if !proxy.Proxy.TransparentTCP {
		t.Fatal("sandboxed remote proxy lost transparent capture")
	}

	managed, proxy = newInputs()
	managed, proxy = trimUnifiedExecTransparentTCP(false, managed, proxy)
	if managed.TransparentTCP {
		t.Fatal("unsandboxed managed context kept transparent capture")
	}
	if proxy.Proxy.TransparentTCP {
		t.Fatal("unsandboxed remote proxy kept transparent capture")
	}

	original := &execserver.RemoteNetworkProxyLaunchConfig{Proxy: execserver.RemoteNetworkProxyConfig{TransparentTCP: true}}
	trimUnifiedExecTransparentTCP(false, nil, original)
	if !original.Proxy.TransparentTCP {
		t.Fatal("trim mutated the caller's launch config")
	}
}

// TestUnifiedExecManagedNetworkContextTransparentTCPLikeRust pins the prepared
// context projection.
func TestUnifiedExecManagedNetworkContextTransparentTCPLikeRust(t *testing.T) {
	context := unifiedExecManagedNetworkContext(&network.ProxyManagedNetworkSandboxContext{TransparentTCP: true})
	if context == nil || !context.TransparentTCP {
		t.Fatalf("managed network context = %#v", context)
	}
}
