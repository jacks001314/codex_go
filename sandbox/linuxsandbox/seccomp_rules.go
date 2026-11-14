package linuxsandbox

import (
	"fmt"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
	"golang.org/x/net/bpf"
)

// compileNetworkSeccomp preserves native rule conditions for x32 numbers,
// including the unflagged aliases accepted before Linux 5.4 (Rust #61206).
func compileNetworkSeccomp(groups []seccomp.SyscallGroup, architecture string) ([]bpf.Instruction, error) {
	info, err := arch.GetInfo(architecture)
	if err != nil {
		return nil, err
	}
	program := seccomp.NewProgram()
	for _, group := range groups {
		action, next := program.NewLabel(), program.NewLabel()
		var calls []seccomp.SyscallWithConditions
		add := func(name string, conditions []seccomp.ArgumentConditions) error {
			number, ok := info.SyscallNames[name]
			if !ok {
				return fmt.Errorf("unknown syscall %q for %s", name, architecture)
			}
			numbers := []uint32{uint32(number)}
			if info.ID == arch.X86_64.ID {
				numbers = append(numbers, uint32(number)|0x40000000)
				aliases := map[string]uint32{"ptrace": 521, "recvmmsg": 537, "sendmmsg": 538, "process_vm_readv": 539, "process_vm_writev": 540, "setsockopt": 541, "getsockopt": 542}
				if alias, ok := aliases[name]; ok {
					numbers = append(numbers, alias, alias|0x40000000)
				}
			}
			for _, nr := range numbers {
				calls = append(calls, seccomp.SyscallWithConditions{Num: nr, Conditions: conditions})
			}
			return nil
		}
		for _, name := range group.Names {
			if err := add(name, nil); err != nil {
				return nil, err
			}
		}
		for _, call := range group.NamesWithCondtions {
			if problems := call.Conditions.Validate(); len(problems) > 0 {
				return nil, fmt.Errorf("invalid syscall conditions: %v", problems)
			}
			if err := add(call.Name, []seccomp.ArgumentConditions{call.Conditions}); err != nil {
				return nil, err
			}
		}
		if len(calls) == 0 {
			continue
		}
		for i, call := range calls {
			call.Assemble(&program, i < len(calls)-1, action, next)
		}
		program.SetLabel(action)
		program.Ret(group.Action)
		program.SetLabel(next)
	}
	program.Ret(seccomp.ActionAllow)
	body, err := program.Assemble()
	if err != nil {
		return nil, err
	}
	// Reject foreign ABIs before reading their syscall numbers.
	header := []bpf.Instruction{
		bpf.LoadAbsolute{Off: 4, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: uint32(info.ID), SkipTrue: 1},
		bpf.RetConstant{Val: uint32(seccomp.ActionErrno) | 1},
		bpf.LoadAbsolute{Off: 0, Size: 4},
	}
	return append(header, body...), nil
}
