package linuxsandbox

import (
	"encoding/binary"
	"testing"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
	"golang.org/x/net/bpf"
)

func TestNetworkSeccompProductionModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode linuxNetworkSeccompMode
		unix bool
	}{
		{"restricted", linuxNetworkSeccompRestricted, false},
		{"proxy", linuxNetworkSeccompProxyRouted, false},
		{"proxy_unix", linuxNetworkSeccompProxyRouted, true},
		{"vm", linuxNetworkSeccompVmSocketRestricted, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ins, err := compileNetworkSeccomp(networkSeccompGroups(tc.mode, tc.unix), "amd64")
			if err != nil {
				t.Fatal(err)
			}
			vm, err := bpf.NewVM(ins)
			if err != nil {
				t.Fatal(err)
			}
			check := func(nr, domain uint32, deny bool) {
				t.Helper()
				for _, number := range []uint32{nr, nr | 0x40000000} {
					data := make([]byte, 64)
					binary.BigEndian.PutUint32(data, number)
					binary.BigEndian.PutUint32(data[4:], uint32(arch.X86_64.ID))
					binary.BigEndian.PutUint32(data[16:], domain)
					got, err := vm.Run(data)
					if err != nil {
						t.Fatal(err)
					}
					want := uint32(seccomp.ActionAllow)
					if deny {
						want = uint32(seccomp.ActionErrno) | 1
					}
					if uint32(got) != want {
						t.Errorf("nr=%#x domain=%d: got %#x want %#x", number, domain, got, want)
					}
				}
			}
			for _, nr := range []uint32{425, 426, 427} {
				check(nr, 0, true)
			}
			for _, nr := range []uint32{101, 310, 311, 521, 539, 540, 249} {
				check(nr, 0, tc.mode != linuxNetworkSeccompVmSocketRestricted)
			}
			for _, nr := range []uint32{42, 43, 288, 49, 50, 52, 51, 48, 44, 299, 307, 54, 55, 537, 538, 541, 542} {
				check(nr, 0, tc.mode == linuxNetworkSeccompRestricted)
			}
			check(39, 0, false)
			for _, domain := range []uint32{1, 2, 10, 16, 40} {
				denySocket := domain != 1
				denyPair := domain != 1
				if tc.mode == linuxNetworkSeccompProxyRouted {
					denySocket = domain != 2 && domain != 10 && !(domain == 1 && tc.unix)
				}
				if tc.mode == linuxNetworkSeccompVmSocketRestricted {
					denySocket = domain == 40
					denyPair = domain == 40
				}
				check(41, domain, denySocket)
				check(53, domain, denyPair)
			}
		})
	}
}
