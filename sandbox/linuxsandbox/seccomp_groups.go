package linuxsandbox

import seccomp "github.com/elastic/go-seccomp-bpf"

type linuxNetworkSeccompMode int

const (
	linuxNetworkSeccompNone linuxNetworkSeccompMode = iota
	linuxNetworkSeccompRestricted
	linuxNetworkSeccompProxyRouted
	// linuxNetworkSeccompVmSocketRestricted mirrors the upstream
	// `NetworkSeccompMode::VmSocketRestricted` mode: it denies `AF_VSOCK`
	// sockets while leaving ordinary networking untouched.
	linuxNetworkSeccompVmSocketRestricted
)

// networkSeccompGroups builds Linux ABI socket restrictions shared by both ABIs.
func networkSeccompGroups(mode linuxNetworkSeccompMode, allowUnixSockets bool) []seccomp.SyscallGroup {
	groups := []seccomp.SyscallGroup{}
	if mode != linuxNetworkSeccompVmSocketRestricted {
		groups = append(groups, seccomp.SyscallGroup{Action: seccomp.ActionErrno, Names: []string{
			"ptrace",
			"process_vm_readv",
			"process_vm_writev",
			// Host key helpers can escape network namespace restrictions.
			"request_key",
		}})
	}
	groups = append(groups, seccomp.SyscallGroup{Action: seccomp.ActionErrno, Names: []string{
		"io_uring_setup",
		"io_uring_enter",
		"io_uring_register",
	}})
	switch mode {
	case linuxNetworkSeccompRestricted:
		groups = append(groups,
			seccomp.SyscallGroup{Action: seccomp.ActionErrno, Names: []string{
				"connect",
				"accept",
				"accept4",
				"bind",
				"listen",
				"getpeername",
				"getsockname",
				"shutdown",
				"sendto",
				"sendmmsg",
				"recvmmsg",
				"getsockopt",
				"setsockopt",
			}},
			seccomp.SyscallGroup{Action: seccomp.ActionErrno, NamesWithCondtions: []seccomp.NameWithConditions{
				{Name: "socket", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.NotEqual, Value: 1}}},
				{Name: "socketpair", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.NotEqual, Value: 1}}},
			}},
		)
	case linuxNetworkSeccompProxyRouted:
		// IP sockets stay allowed inside the isolated namespace so the command
		// can reach the local routing bridge. A standalone AF_UNIX socket is
		// denied unless the effective managed-network policy grants
		// `dangerously_allow_all_unix_sockets`; AF_UNIX socket pairs stay
		// allowed either way (see the socketpair rule below).
		deniedSocketFamilies := []seccomp.Condition{
			{Argument: 0, Operation: seccomp.NotEqual, Value: 2},
			{Argument: 0, Operation: seccomp.NotEqual, Value: 10},
		}
		if allowUnixSockets {
			deniedSocketFamilies = append(deniedSocketFamilies, seccomp.Condition{Argument: 0, Operation: seccomp.NotEqual, Value: 1})
		}
		groups = append(groups,
			seccomp.SyscallGroup{Action: seccomp.ActionErrno, NamesWithCondtions: []seccomp.NameWithConditions{
				{Name: "socket", Conditions: deniedSocketFamilies},
				{Name: "socketpair", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.NotEqual, Value: 1}}},
			}},
		)
	case linuxNetworkSeccompVmSocketRestricted:
		// Deny only the VM socket family; every other family, including
		// `AF_UNIX`, keeps the behaviour of an unrestricted network policy.
		groups = append(groups, seccomp.SyscallGroup{Action: seccomp.ActionErrno, NamesWithCondtions: []seccomp.NameWithConditions{
			{Name: "socket", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.Equal, Value: 40}}},
			{Name: "socketpair", Conditions: []seccomp.Condition{{Argument: 0, Operation: seccomp.Equal, Value: 40}}},
		}})
	}
	return groups
}
