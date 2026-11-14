//go:build linux

package linuxsandbox

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The baseline prevents request_key from invoking a host helper and makes an
// inherited EPERM filter unable to hide a missing policy rule.
func TestRequestKeyNetworkPolicyLikeRust(t *testing.T) {
	const probeEnv = "CODEX_GO_REQUEST_KEY_PROBE"
	if mode := os.Getenv(probeEnv); mode != "" {
		baseline := []unix.SockFilter{
			{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
			{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: uint32(unix.SYS_REQUEST_KEY), Jf: 1},
			{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)},
			{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
		}
		program := unix.SockFprog{Len: uint16(len(baseline)), Filter: &baseline[0]}
		if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&program)))
		if errno != 0 {
			t.Fatalf("install baseline: %v", errno)
		}
		probe := func() syscall.Errno { _, _, err := unix.Syscall6(unix.SYS_REQUEST_KEY, 0, 0, 0, 0, 0, 0); return err }
		if got := probe(); got != unix.ENOSYS {
			t.Fatalf("baseline: got %v want ENOSYS", got)
		}
		selected := linuxNetworkSeccompRestricted
		want := syscall.EPERM
		switch mode {
		case "proxy":
			selected = linuxNetworkSeccompProxyRouted
		case "vm":
			selected = linuxNetworkSeccompVmSocketRestricted
			want = syscall.ENOSYS
		}
		if err := installLinuxNetworkSeccompFilter(selected, nil); err != nil {
			t.Fatal(err)
		}
		if got := probe(); got != want {
			t.Fatalf("%s: got %v want %v", mode, got, want)
		}
		return
	}
	for _, mode := range []string{"restricted", "proxy", "vm"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestRequestKeyNetworkPolicyLikeRust$")
			cmd.Env = append(os.Environ(), probeEnv+"="+mode)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("probe failed: %v\n%s", err, out)
			}
		})
	}
}
