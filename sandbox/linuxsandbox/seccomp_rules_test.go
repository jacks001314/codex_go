package linuxsandbox

import (
	"encoding/binary"
	"testing"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
	"golang.org/x/net/bpf"
)

func TestX32SeccompAliasesAndSocketConditions(t *testing.T) {
	groups := []seccomp.SyscallGroup{
		{Action: seccomp.ActionErrno, Names: []string{"ptrace", "process_vm_readv", "process_vm_writev", "recvmmsg", "sendmmsg", "setsockopt", "getsockopt", "request_key", "io_uring_setup"}},
		{Action: seccomp.ActionErrno, NamesWithCondtions: []seccomp.NameWithConditions{{Name: "socket", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.NotEqual, Value: 1}}}}},
	}
	instructions, err := compileNetworkSeccomp(groups, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	vm, err := bpf.NewVM(instructions)
	if err != nil {
		t.Fatal(err)
	}
	probe := func(nr, domain, abi uint32) uint32 {
		t.Helper()
		// x/net/bpf VM decodes each word in network byte order. The kernel
		// uses native-endian words at these same seccomp_data offsets.
		data := make([]byte, 64)
		binary.BigEndian.PutUint32(data, nr)
		binary.BigEndian.PutUint32(data[4:], abi)
		binary.BigEndian.PutUint32(data[16:], domain)
		result, err := vm.Run(data)
		if err != nil {
			t.Fatal(err)
		}
		return uint32(result)
	}
	denied := uint32(seccomp.ActionErrno) | 1
	for _, nr := range []uint32{101, 310, 311, 299, 307, 54, 55, 249, 425, 521, 537, 538, 539, 540, 541, 542} {
		for _, number := range []uint32{nr, nr | 0x40000000} {
			if got := probe(number, 0, uint32(arch.X86_64.ID)); got != denied {
				t.Errorf("syscall %#x = %#x, want EPERM", number, got)
			}
		}
	}
	for _, nr := range []uint32{41, 41 | 0x40000000} {
		if got := probe(nr, 2, uint32(arch.X86_64.ID)); got != denied {
			t.Errorf("non-Unix socket %#x = %#x", nr, got)
		}
		if got := probe(nr, 1, uint32(arch.X86_64.ID)); got != uint32(seccomp.ActionAllow) {
			t.Errorf("Unix socket %#x = %#x", nr, got)
		}
	}
	for _, nr := range []uint32{39, 39 | 0x40000000} {
		if got := probe(nr, 0, uint32(arch.X86_64.ID)); got != uint32(seccomp.ActionAllow) {
			t.Errorf("getpid %#x = %#x", nr, got)
		}
	}
	if got := probe(39, 0, 0); got != denied {
		t.Errorf("foreign ABI = %#x", got)
	}
}
