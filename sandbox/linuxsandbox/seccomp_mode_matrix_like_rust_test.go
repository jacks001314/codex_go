package linuxsandbox

import (
	"encoding/binary"
	"testing"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
	"golang.org/x/net/bpf"
)

// seccompModeProbe builds the compiled filter for one network mode and returns a
// probe that runs one syscall number through it.
func seccompModeProbe(t *testing.T, mode linuxNetworkSeccompMode, allowUnixSockets bool) func(nr uint32, domain uint32) uint32 {
	t.Helper()
	instructions, err := compileNetworkSeccomp(networkSeccompGroups(mode, allowUnixSockets), "amd64")
	if err != nil {
		t.Fatalf("compileNetworkSeccomp: %v", err)
	}
	vm, err := bpf.NewVM(instructions)
	if err != nil {
		t.Fatalf("bpf.NewVM: %v", err)
	}
	abi := uint32(arch.X86_64.ID)
	return func(nr uint32, domain uint32) uint32 {
		t.Helper()
		data := make([]byte, 64)
		binary.BigEndian.PutUint32(data, nr)
		binary.BigEndian.PutUint32(data[4:], abi)
		binary.BigEndian.PutUint32(data[16:], domain)
		result, err := vm.Run(data)
		if err != nil {
			t.Fatalf("probe %#x: %v", nr, err)
		}
		return uint32(result)
	}
}

func amd64SyscallNumber(t *testing.T, name string) uint32 {
	t.Helper()
	info, err := arch.GetInfo("amd64")
	if err != nil {
		t.Fatal(err)
	}
	number, ok := info.SyscallNames[name]
	if !ok {
		t.Fatalf("amd64 has no syscall %q", name)
	}
	return uint32(number)
}

// Mirrors Rust #61256 seccomp_tests.rs::request_key_respects_network_policy at
// the BPF level: both socket-restricting modes deny request_key on the native
// and x32 numbers, while VmSocketRestricted leaves it untouched. io_uring stays
// denied in every mode (Rust seccomp.rs), so a missing group shows up here.
func TestRequestKeySeccompMatrixLikeRust(t *testing.T) {
	denied := uint32(seccomp.ActionErrno) | 1
	cases := []struct {
		name string
		mode linuxNetworkSeccompMode
		want uint32
	}{
		{name: "restricted", mode: linuxNetworkSeccompRestricted, want: denied},
		{name: "proxy_routed", mode: linuxNetworkSeccompProxyRouted, want: denied},
		{name: "proxy_routed_transparent_tcp", mode: linuxNetworkSeccompProxyRoutedTransparentTCP, want: denied},
		{name: "vm_socket_restricted", mode: linuxNetworkSeccompVmSocketRestricted, want: uint32(seccomp.ActionAllow)},
	}
	requestKey := amd64SyscallNumber(t, "request_key")
	ioUringSetup := amd64SyscallNumber(t, "io_uring_setup")
	for _, tc := range cases {
		probe := seccompModeProbe(t, tc.mode, false)
		for _, nr := range []uint32{requestKey, requestKey | 0x40000000} {
			if got := probe(nr, 0); got != tc.want {
				t.Errorf("%s request_key %#x = %#x, want %#x", tc.name, nr, got, tc.want)
			}
		}
		for _, nr := range []uint32{ioUringSetup, ioUringSetup | 0x40000000} {
			if got := probe(nr, 0); got != denied {
				t.Errorf("%s io_uring_setup %#x = %#x, want EPERM", tc.name, nr, got)
			}
		}
	}
}

// Mirrors Rust #61206's per-mode socket probes on the real mode groups: the
// native and x32 numbers resolve identically, Restricted keeps only AF_UNIX
// sockets, and ProxyRouted lets IP sockets reach the local bridge.
func TestNetworkSeccompModeSocketMatrixLikeRust(t *testing.T) {
	denied := uint32(seccomp.ActionErrno) | 1
	allowed := uint32(seccomp.ActionAllow)
	socket := amd64SyscallNumber(t, "socket")
	socketpair := amd64SyscallNumber(t, "socketpair")
	connect := amd64SyscallNumber(t, "connect")

	restricted := seccompModeProbe(t, linuxNetworkSeccompRestricted, false)
	for _, nr := range []uint32{socket, socket | 0x40000000} {
		if got := restricted(nr, 2); got != denied {
			t.Errorf("restricted socket(AF_INET) %#x = %#x, want EPERM", nr, got)
		}
		if got := restricted(nr, 1); got != allowed {
			t.Errorf("restricted socket(AF_UNIX) %#x = %#x, want allowed", nr, got)
		}
	}
	for _, nr := range []uint32{socketpair, socketpair | 0x40000000} {
		if got := restricted(nr, 2); got != denied {
			t.Errorf("restricted socketpair(AF_INET) %#x = %#x, want EPERM", nr, got)
		}
	}
	for _, nr := range []uint32{connect, connect | 0x40000000} {
		if got := restricted(nr, 0); got != denied {
			t.Errorf("restricted connect %#x = %#x, want EPERM", nr, got)
		}
	}

	proxyRouted := seccompModeProbe(t, linuxNetworkSeccompProxyRouted, false)
	for _, nr := range []uint32{socket, socket | 0x40000000} {
		if got := proxyRouted(nr, 2); got != allowed {
			t.Errorf("proxy_routed socket(AF_INET) %#x = %#x, want allowed", nr, got)
		}
		if got := proxyRouted(nr, 10); got != allowed {
			t.Errorf("proxy_routed socket(AF_INET6) %#x = %#x, want allowed", nr, got)
		}
		if got := proxyRouted(nr, 1); got != denied {
			t.Errorf("proxy_routed socket(AF_UNIX) %#x = %#x, want EPERM", nr, got)
		}
	}
	for _, nr := range []uint32{connect, connect | 0x40000000} {
		if got := proxyRouted(nr, 0); got != allowed {
			t.Errorf("proxy_routed connect %#x = %#x, want allowed", nr, got)
		}
	}

	vmSocket := seccompModeProbe(t, linuxNetworkSeccompVmSocketRestricted, false)
	for _, nr := range []uint32{socket, socket | 0x40000000} {
		if got := vmSocket(nr, 40); got != denied {
			t.Errorf("vm_socket socket(AF_VSOCK) %#x = %#x, want EPERM", nr, got)
		}
		if got := vmSocket(nr, 1); got != allowed {
			t.Errorf("vm_socket socket(AF_UNIX) %#x = %#x, want allowed", nr, got)
		}
	}
}
