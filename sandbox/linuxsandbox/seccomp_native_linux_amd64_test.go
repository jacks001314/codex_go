package linuxsandbox

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Each filter is irreversible and TSYNC applies it to all threads. Keep it in
// a child test process so other tests and the parent retain their own policy.
func TestNativeNetworkSeccomp(t *testing.T) {
	mode := os.Getenv("CODEX_GO_SECCOMP_TEST_MODE")
	if mode == "" {
		for _, name := range []string{"restricted", "proxy", "proxy_unix", "vm"} {
			t.Run(name, func(t *testing.T) {
				command := exec.Command(os.Args[0], "-test.run=^TestNativeNetworkSeccomp$", "-test.v")
				command.Env = append(os.Environ(), "CODEX_GO_SECCOMP_TEST_MODE="+name)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("native %s: %v\n%s", name, err, output)
				}
			})
		}
		return
	}
	selected, allowUnix := linuxNetworkSeccompRestricted, false
	switch mode {
	case "restricted":
	case "proxy":
		selected = linuxNetworkSeccompProxyRouted
	case "proxy_unix":
		selected, allowUnix = linuxNetworkSeccompProxyRouted, true
	case "vm":
		selected = linuxNetworkSeccompVmSocketRestricted
	default:
		t.Fatalf("unknown test mode %q", mode)
	}
	if err := loadNetworkSeccomp(networkSeccompGroups(selected, allowUnix)); err != nil {
		t.Fatal(err)
	}
	if unix.Getpid() <= 0 {
		t.Fatal("ordinary syscall blocked")
	}
	denied := []uintptr{425, 426, 427}
	if selected != linuxNetworkSeccompVmSocketRestricted {
		denied = append(denied, 249)
	}
	if selected == linuxNetworkSeccompRestricted {
		denied = append(denied, 42, 43, 288, 49, 50, 52, 51, 48, 44, 299, 307, 54, 55, 537, 538, 541, 542)
	}
	for _, nr := range denied {
		for _, number := range []uintptr{nr, nr | 0x40000000} {
			_, _, errno := unix.Syscall6(number, ^uintptr(0), 0, 0, 0, 0, 0)
			if errno != unix.EPERM {
				t.Errorf("syscall %#x errno=%v, want EPERM", number, errno)
			}
		}
	}
	families := []int{unix.AF_UNIX, unix.AF_INET, unix.AF_INET6, unix.AF_NETLINK, unix.AF_VSOCK}
	for _, family := range families {
		deny := family != unix.AF_UNIX
		if selected == linuxNetworkSeccompProxyRouted {
			deny = family != unix.AF_INET && family != unix.AF_INET6 && !(family == unix.AF_UNIX && allowUnix)
		}
		if selected == linuxNetworkSeccompVmSocketRestricted {
			deny = family == unix.AF_VSOCK
		}
		if !deny {
			continue
		}
		for _, nr := range []uintptr{unix.SYS_SOCKET, unix.SYS_SOCKET | 0x40000000} {
			fd, _, errno := unix.Syscall(nr, uintptr(family), unix.SOCK_STREAM, 0)
			if errno == 0 {
				_ = unix.Close(int(fd))
			}
			if errno != unix.EPERM {
				t.Errorf("socket family=%d nr=%#x errno=%v", family, nr, errno)
			}
		}
	}
}

// A thread with a divergent filter tree makes TSYNC return its positive TID,
// not an errno. Verify the loader rejects that result in an isolated process.
func TestNativeSeccompRejectsUnsynchronizedThread(t *testing.T) {
	if os.Getenv("CODEX_GO_SECCOMP_DIVERGENT_TEST") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestNativeSeccompRejectsUnsynchronizedThread$", "-test.v")
		command.Env = append(os.Environ(), "CODEX_GO_SECCOMP_DIVERGENT_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("divergent filter: %v\n%s", err, output)
		}
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ready := make(chan error, 1)
	release := make(chan struct{})
	defer close(release)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		filter := []unix.SockFilter{{Code: 0x06, K: unix.SECCOMP_RET_ALLOW}}
		program := unix.SockFprog{Len: 1, Filter: &filter[0]}
		err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)
		if err == nil {
			_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, 0, uintptr(unsafe.Pointer(&program)))
			runtime.KeepAlive(filter)
			if errno != 0 {
				err = errno
			}
		}
		ready <- err
		<-release
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	err := loadNetworkSeccomp(networkSeccompGroups(linuxNetworkSeccompRestricted, false))
	if err == nil || !strings.Contains(err.Error(), "thread synchronization failed") {
		t.Fatalf("unsynchronized filter result=%v", err)
	}
}
