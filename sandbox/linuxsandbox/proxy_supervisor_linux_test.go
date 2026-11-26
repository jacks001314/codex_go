//go:build linux

package linuxsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type proxySupervisorFixture struct {
	command                       *exec.Cmd
	ready, release, count, output string
	upstream                      net.Listener
}

func newProxySupervisorFixture(t *testing.T) *proxySupervisorFixture {
	t.Helper()
	real, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("system bubblewrap is unavailable")
	}
	if out, err := exec.Command(real, "--unshare-pid", "--ro-bind", "/", "/", "--", "/bin/true").CombinedOutput(); err != nil {
		t.Skipf("native bubblewrap is unavailable: %v: %s", err, out)
	}
	upstream, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upstream.Close() })
	cwd, helperDir := t.TempDir(), t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(helperDir, LinuxSandboxArg0)); err != nil {
		t.Fatal(err)
	}
	f := &proxySupervisorFixture{
		ready: filepath.Join(cwd, "ready"), release: filepath.Join(cwd, "release"),
		count: filepath.Join(cwd, "count"), output: filepath.Join(cwd, "output"), upstream: upstream,
	}
	denied := filepath.Join(cwd, "denied-file")
	if err := os.WriteFile(denied, []byte("fixture-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"},{"path":{"type":"path","path":%q},"access":"write"},{"path":{"type":"path","path":%q},"access":"deny"}]},"network":"enabled"}`, cwd, denied)
	script := `if [ -s "$4" ]; then exit 85; fi; printf 'once\n' >> "$1"; : > "$2"; while [ ! -f "$3" ]; do /bin/sleep 0.01; done; while read -r key value rest; do case "$key" in CapEff:|CapPrm:|CapInh:|CapAmb:) printf '%s%s\n' "$key" "$value";; esac; done < /proc/self/status; printf 'payload\n'; exit 23`
	port := upstream.Addr().(*net.TCPAddr).Port
	args, err := CreateCommandArgsWithSandboxExeAndManagedNetwork(executable,
		[]string{"/bin/sh", "-c", script, "sh", f.count, f.ready, f.release, denied}, cwd, profile, cwd, false, true,
		fmt.Sprintf(`{"loopbackPorts":[%d]}`, port))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	f.command = exec.CommandContext(ctx, args[0], args[1:]...)
	f.command.Env = linuxSandboxE2EChildEnv(true, upstream.Addr().String(), helperDir)
	// Pin the actual distro bwrap, without wrappers or a user PATH override.
	f.command.Env = append(f.command.Env, "PATH="+helperDir+":"+filepath.Dir(real)+":/usr/bin:/bin")
	output, err := os.Create(f.output)
	if err != nil {
		t.Fatal(err)
	}
	f.command.Stdout, f.command.Stderr = output, output
	t.Cleanup(func() { _ = output.Close() })
	return f
}

func (f *proxySupervisorFixture) start(t *testing.T) {
	t.Helper()
	if err := f.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.command.ProcessState == nil {
			_ = f.command.Process.Kill()
			_ = f.command.Wait()
		}
	})
	t.Cleanup(func() {
		if t.Failed() {
			output, _ := os.ReadFile(f.output)
			t.Logf("native launcher output: %s", output)
		}
	})
	proxySupervisorEventually(t, func() bool {
		_, err := os.Stat(f.ready)
		return err == nil
	}, "sandbox payload readiness (output "+f.output+")")
}

func (f *proxySupervisorFixture) socket(t *testing.T) string {
	t.Helper()
	pattern := filepath.Join(proxySocketParentDir(), fmt.Sprintf("%s%d-%d-*", proxySocketDirPrefix, f.command.Process.Pid, os.Geteuid()), "*.sock")
	paths, err := filepath.Glob(pattern)
	if err != nil || len(paths) != 1 {
		t.Fatalf("owned proxy socket: %v, %v (pattern %s)", paths, err, pattern)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(paths[0])) })
	return paths[0]
}

func (f *proxySupervisorFixture) openConnection(t *testing.T, socket string) (net.Conn, <-chan struct{}) {
	t.Helper()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := f.upstream.Accept()
		accepted <- conn
	}()
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("bridge did not connect to native upstream")
	}
	if server == nil {
		t.Fatal("upstream accept failed")
	}
	t.Cleanup(func() { _ = server.Close() })
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, server)
		close(done)
	}()
	return conn, done
}

func TestProxySupervisorNativeLifecycleLikeRust(t *testing.T) {
	for _, transport := range []string{"explicit", "transparent"} {
		for _, mode := range []string{"exit-23", "term", "quit", "int", "hup", "launcher-killed"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				f := newProxySupervisorFixture(t)
				if transport == "transparent" {
					f.transparent(t)
				}
				f.start(t)
				socket := f.socket(t)
				conn, upstreamClosed := f.openConnection(t, socket)
				children := proxySupervisorChildren(t, f.command.Process.Pid)
				bwrapPID, cleanupPID := splitProxySupervisorChildren(t, children)
				if transport == "transparent" {
					nested := proxySupervisorChildren(t, bwrapPID)
					if len(nested) != 1 {
						t.Fatalf("transparent namespace children = %v", nested)
					}
					bwrapPID = nested[0]
				}
				executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", bwrapPID))
				if err != nil || filepath.Base(executable) != "bwrap" {
					t.Fatalf("actual child executable = %q, %v", executable, err)
				}
				assertProxyBridgeFDIsolation(t, f.command.Process.Pid, bwrapPID)
				if mode == "exit-23" {
					if err := os.WriteFile(f.release, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					sig := map[string]syscall.Signal{"term": syscall.SIGTERM, "quit": syscall.SIGQUIT, "int": syscall.SIGINT, "hup": syscall.SIGHUP, "launcher-killed": syscall.SIGKILL}[mode]
					if err := f.command.Process.Signal(sig); err != nil {
						t.Fatal(err)
					}
				}
				_ = f.command.Wait()
				status := f.command.ProcessState.Sys().(syscall.WaitStatus)
				if mode == "exit-23" {
					if !status.Exited() || status.ExitStatus() != 23 {
						t.Fatalf("wait status = %v, want exited 23", status)
					}
					output, _ := os.ReadFile(f.output)
					for _, key := range []string{"CapEff", "CapPrm", "CapInh", "CapAmb"} {
						if !strings.Contains(string(output), key+":0000000000000000") {
							t.Fatalf("workload retained capabilities: %s", output)
						}
					}
				} else {
					want := map[string]syscall.Signal{"term": syscall.SIGTERM, "quit": syscall.SIGQUIT, "int": syscall.SIGINT, "hup": syscall.SIGHUP, "launcher-killed": syscall.SIGKILL}[mode]
					if !status.Signaled() || status.Signal() != want {
						t.Fatalf("wait status = %v, want signaled %v", status, want)
					}
				}
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				if _, err := conn.Read(make([]byte, 1)); err == nil {
					t.Fatal("bridge kept serving after launcher exit")
				} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
					t.Fatalf("bridge connection survived launcher: %v", err)
				}
				select {
				case <-upstreamClosed:
				case <-time.After(3 * time.Second):
					t.Fatal("upstream socket survived launcher")
				}
				proxySupervisorEventually(t, func() bool { return !proxySupervisorRunning(bwrapPID) }, "bwrap termination after launcher exit")
				if cleanupPID != 0 {
					proxySupervisorEventually(t, func() bool { return !proxySupervisorRunning(cleanupPID) }, "mount cleanup supervisor exit after the workload")
				}
				if mode != "launcher-killed" {
					if _, err := os.Stat(filepath.Dir(socket)); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("route cleanup after bwrap wait: %v", err)
					}
				} else if cleanupPID != 0 {
					// Without the supervisor a SIGKILLed launcher can leave an idle
					// route directory behind; the detached supervisor must remove it.
					proxySupervisorEventually(t, func() bool {
						_, err := os.Stat(filepath.Dir(socket))
						return errors.Is(err, os.ErrNotExist)
					}, "detached synthetic mount cleanup after launcher SIGKILL")
				}
				count, err := os.ReadFile(f.count)
				if err != nil || string(count) != "once\n" {
					t.Fatalf("payload executions = %q, %v, want once", count, err)
				}
				t.Logf("real %s pid=%d; supervisor=%d; mount_cleanup_supervisor=%d; transport=%s mode=%s wait_status=%d; active bridge closed; command once", executable, bwrapPID, f.command.Process.Pid, cleanupPID, transport, mode, status)
			})
		}
	}

}

// TestProxySupervisorInheritedSIGCHLDIgnoredLikeRust covers a caller that
// ignores SIGCHLD and then execs the launcher. Rust resets SIGCHLD in its
// bubblewrap launch path so an inherited SIG_IGN cannot make the kernel reap the
// supervised stage. Go needs no equivalent: the runtime restores the default
// SIGCHLD disposition before main runs (observed on go1.26.3 linux/amd64), so the
// guarantee is that the launcher still reports the command's own exit status.
// This test locks that behaviour against any future raw fork/exec path.
func TestProxySupervisorInheritedSIGCHLDIgnoredLikeRust(t *testing.T) {
	f := newProxySupervisorFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Guard the fixture: ignoring SIGCHLD in Go and exec'ing must still leave the
	// signal ignored, or this case would pass without exercising the reset.
	probe := exec.Command(executable, "-test.run=^TestProxySupervisorSigchldIgnoredCaller$")
	probe.Env = append(probe.Env, sigchldIgnoredCallerEnv+"=probe")
	out, err := probe.Output()
	if err != nil {
		t.Fatalf("SIGCHLD ignore probe = %v: %s", err, out)
	}
	mask, err := sigchldIgnoredMask(string(out))
	if err != nil || mask&(1<<16) == 0 {
		t.Fatalf("SIGCHLD ignore precondition = %q, %v", out, err)
	}
	encoded, err := json.Marshal(f.command.Args)
	if err != nil {
		t.Fatal(err)
	}
	env := append(append([]string{}, f.command.Env...), sigchldIgnoredCallerEnv+"=exec:"+string(encoded))
	cmd := exec.Command(executable, "-test.run=^TestProxySupervisorSigchldIgnoredCaller$")
	cmd.Dir = f.command.Dir
	cmd.Env = env
	output, err := os.Create(f.output)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	cmd.Stdout, cmd.Stderr = output, output
	f.command = cmd
	f.start(t)
	if err := os.WriteFile(f.release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = f.command.Wait()
	status := f.command.ProcessState.Sys().(syscall.WaitStatus)
	if !status.Exited() || status.ExitStatus() != 23 {
		body, _ := os.ReadFile(f.output)
		t.Fatalf("wait status = %v, want exited 23; output %s", status, body)
	}
	count, err := os.ReadFile(f.count)
	if err != nil || string(count) != "once\n" {
		t.Fatalf("payload executions = %q, %v, want once", count, err)
	}
}

const sigchldIgnoredCallerEnv = "CODEX_PROXY_SIGCHLD_CALLER"

// sigchldIgnoredMask reads the ignored-signal mask out of /proc/self/status.
func sigchldIgnoredMask(status string) (uint64, error) {
	for _, line := range strings.Split(status, "\n") {
		value, ok := strings.CutPrefix(line, "SigIgn:")
		if !ok {
			continue
		}
		return strconv.ParseUint(strings.TrimSpace(value), 16, 64)
	}
	return 0, errors.New("missing SigIgn entry")
}

// TestProxySupervisorSigchldIgnoredCaller ignores SIGCHLD and either reports the
// inherited disposition or execs the launcher, like a daemon that runs with
// SIGCHLD=SIG_IGN.
func TestProxySupervisorSigchldIgnoredCaller(t *testing.T) {
	mode := os.Getenv(sigchldIgnoredCallerEnv)
	if mode == "" {
		return
	}
	signal.Ignore(syscall.SIGCHLD)
	if mode == "probe" {
		if err := syscall.Exec("/bin/cat", []string{"cat", "/proc/self/status"}, os.Environ()); err != nil {
			os.Exit(91)
		}
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(mode, "exec:")), &args); err != nil || len(args) == 0 {
		os.Exit(90)
	}
	if !signal.Ignored(syscall.SIGCHLD) {
		os.Exit(92)
	}
	if err := syscall.Exec(args[0], args, os.Environ()); err != nil {
		os.Exit(93)
	}
}

func TestProxySupervisorHandoffFailuresLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"{", `{}`, `{"Parent":1}`, fmt.Sprintf(`{"Parent":%d,"Command":null}`, os.Getpid()), fmt.Sprintf(`{"Parent":%d,"Command":{"AllowNetworkForProxy":true,"ApplySeccompThenExec":true}}`, os.Getpid())} {
		t.Run(body, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "handoff")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := io.WriteString(file, body); err != nil {
				t.Fatal(err)
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, proxyBwrapExecArg)
			cmd.ExtraFiles = []*os.File{file, file}
			cmd.Env = append(os.Environ(), linuxSandboxE2EHelperEnv+"=1")
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), "supervised bubblewrap:") {
				t.Fatalf("invalid handoff: error %v, output %q", err, out)
			}
		})
	}
}

func TestProxySupervisorCanceledBeforeHandoffLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runManagedProxySandbox(ctx, &linuxSandboxCommand{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled setup = %v, want context.Canceled", err)
	}
}

func TestProxySupervisorNativeExecFailureLikeRust(t *testing.T) {
	f := newProxySupervisorFixture(t)
	for index, arg := range f.command.Args {
		if arg == "--" {
			f.command.Args = append(f.command.Args[:index+1], "/definitely-missing-codex-sandbox-command")
			break
		}
	}
	err := f.command.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("native exec failure = %v, want exit 1", err)
	}
	paths, err := filepath.Glob(filepath.Join(proxySocketParentDir(), fmt.Sprintf("%s%d-%d-*", proxySocketDirPrefix, f.command.Process.Pid, os.Geteuid())))
	if err != nil || len(paths) != 0 {
		t.Fatalf("routes survived native exec failure: %v, %v", paths, err)
	}
	if _, err := os.Stat(f.count); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("payload ran after failed exec: %v", err)
	}
	output, err := os.ReadFile(f.output)
	if err != nil || !strings.Contains(string(output), "no such file") {
		t.Fatalf("native failure diagnostic = %q, %v", output, err)
	}
}

func TestProxySupervisorNativeCallerExitLikeRust(t *testing.T) {
	f := newProxySupervisorFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(f.command.Args)
	if err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(t.TempDir(), "supervisor-pid")
	parent := exec.Command(executable, "-test.run=^TestProxySupervisorCallerExitProcess$")
	parent.Env = append(f.command.Env, "CODEX_PROXY_CALLER_ARGS="+string(args), "CODEX_PROXY_CALLER_PID="+pidPath)
	parent.Stdout, parent.Stderr = f.command.Stdout, f.command.Stderr
	input, err := parent.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if parent.ProcessState == nil {
			_ = parent.Process.Kill()
			_ = parent.Wait()
		}
	})
	proxySupervisorEventually(t, func() bool { _, err := os.Stat(f.ready); return err == nil }, "caller-owned sandbox readiness")
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		t.Fatal(err)
	}

	// Reuse the socket assertions with the supervisor PID, not the intermediate
	// caller's PID. It deliberately exits without waiting for its launcher.
	f.command.Process, err = os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.command.Process.Kill(); _ = f.command.Process.Release() })
	socket := f.socket(t)
	conn, upstreamClosed := f.openConnection(t, socket)
	children := proxySupervisorChildren(t, pid)
	bwrapPID, cleanupPID := splitProxySupervisorChildren(t, children)
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(); err != nil {
		t.Fatalf("caller exit = %v", err)
	}
	proxySupervisorEventually(t, func() bool { return !proxySupervisorRunning(pid) && !proxySupervisorRunning(bwrapPID) }, "caller death to stop supervisor and bwrap")
	if cleanupPID != 0 {
		proxySupervisorEventually(t, func() bool { return !proxySupervisorRunning(cleanupPID) }, "caller death to stop the mount cleanup supervisor")
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("proxy survived its caller")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("proxy kept a live connection after caller exit")
	}
	select {
	case <-upstreamClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream survived caller exit")
	}
	t.Logf("caller %d exited; supervisor %d and real bwrap %d stopped; active bridge closed; mount_cleanup_supervisor=%d", parent.Process.Pid, pid, bwrapPID, cleanupPID)
}

// TestProxySupervisorCallerExitProcess is a test-only executor process. The
// actual launcher and bwrap remain the production implementations.
func TestProxySupervisorCallerExitProcess(t *testing.T) {
	if os.Getenv("CODEX_PROXY_CALLER_PID") == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("CODEX_PROXY_CALLER_ARGS")), &args); err != nil || len(args) == 0 {
		os.Exit(90)
	}
	child := exec.Command(args[0], args[1:]...)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(91)
	}
	if err := os.WriteFile(os.Getenv("CODEX_PROXY_CALLER_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		os.Exit(92)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestProxySupervisorNativeContextCancellationLikeRust(t *testing.T) {
	f := newProxySupervisorFixture(t)
	args := f.command.Args[1:]
	cmd, err := parseLinuxSandboxCommand(args, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	// This case invokes the production supervisor directly so cancellation is
	// a real context transition, not a shell wrapper or an injected bwrap.
	for _, entry := range f.command.Env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && (proxyEnvKeys[strings.ToUpper(key)] || key == "PATH" || key == linuxSandboxE2EHelperEnv) {
			t.Setenv(key, value)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- runManagedProxySandbox(ctx, cmd) }()
	proxySupervisorEventually(t, func() bool { _, err := os.Stat(f.ready); return err == nil }, "context-owned native workload")
	cancel()
	select {
	case err := <-finished:
		var exit *linuxSandboxChildExit
		if !errors.As(err, &exit) || !exit.status.Signaled() || exit.status.Signal() != syscall.SIGKILL {
			t.Fatalf("canceled workload = %v, want SIGKILL wait status", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("canceled supervisor did not reap its child")
	}
}

func proxySupervisorEventually(t *testing.T, predicate func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func proxySupervisorRunning(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	_, suffix, ok := strings.Cut(string(data), ") ")
	return ok && !strings.HasPrefix(suffix, "Z ")
}

func proxySupervisorChildren(t *testing.T, pid int) []int {
	t.Helper()
	files, err := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", pid))
	if err != nil {
		t.Fatal(err)
	}
	var result []int
	for _, path := range files {
		data, _ := os.ReadFile(path)
		for _, field := range strings.Fields(string(data)) {
			child, err := strconv.Atoi(field)
			if err != nil {
				t.Fatal(err)
			}
			result = append(result, child)
		}
	}
	return result
}

// splitProxySupervisorChildren separates the real bubblewrap stage from the
// opt-in synthetic mount cleanup supervisor.  Every other child would mean the
// launcher detached a bridge process, which the Rust supervisor never does.
func splitProxySupervisorChildren(t *testing.T, children []int) (bwrapPID, cleanupPID int) {
	t.Helper()
	var candidates []int
	for _, child := range children {
		cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", child))
		if strings.Contains(string(cmdline), mountCleanupSupervisorArg) {
			if cleanupPID != 0 {
				t.Fatalf("launcher children = %v, want at most one mount cleanup supervisor", children)
			}
			cleanupPID = child
			continue
		}
		candidates = append(candidates, child)
	}
	if len(candidates) != 1 {
		t.Fatalf("launcher children = %v, want one bwrap, no detached bridge", children)
	}
	return candidates[0], cleanupPID
}

func assertProxyBridgeFDIsolation(t *testing.T, supervisor, child int) {
	t.Helper()
	sockets := make(map[string]bool)
	for _, pid := range []int{supervisor, child} {
		files, _ := filepath.Glob(fmt.Sprintf("/proc/%d/fd/*", pid))
		for _, path := range files {
			link, _ := os.Readlink(path)
			if !strings.HasPrefix(link, "socket:[") {
				continue
			}
			if pid == supervisor {
				sockets[link] = true
			} else if sockets[link] {
				t.Fatalf("bridge descriptor %s leaked into bwrap pid %d", link, child)
			}
		}
	}
	if len(sockets) == 0 {
		t.Fatal("no bridge descriptors found in the supervisor")
	}
}

func TestProxySupervisorCallerMismatchBeforeSetupLikeRust(t *testing.T) {
	err := runManagedProxySandbox(context.Background(), &linuxSandboxCommand{callerPID: os.Getpid()})
	if err == nil || !strings.Contains(err.Error(), "caller exited before proxy setup") {
		t.Fatalf("caller mismatch = %v, want failure before setup", err)
	}
}

func TestTransparentTCPDataHandoffRejectsClosedFDLikeRust(t *testing.T) {
	_, _, err := preserveTransparentTCPDataFiles([]string{"bwrap", "--"}, []int{-1})
	if !errors.Is(err, syscall.EBADF) {
		t.Fatalf("closed data descriptor = %v, want EBADF", err)
	}
}

func (f *proxySupervisorFixture) transparent(t *testing.T) {
	t.Helper()
	file, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("native transparent TCP needs accessible /dev/net/tun: %v", err)
	}
	_ = file.Close()
	f.command.Args = append(f.command.Args[:1], append([]string{"--transparent-tcp"}, f.command.Args[1:]...)...)
	var env []string
	for _, entry := range f.command.Env {
		key, _, _ := strings.Cut(entry, "=")
		if !proxyEnvKeys[strings.ToUpper(key)] {
			env = append(env, entry)
		}
	}
	f.command.Env = append(env, "ALL_PROXY=socks5://"+f.upstream.Addr().String())
}

func TestProxySupervisorNativeHardenedLikeRust(t *testing.T) {
	for _, transparent := range []bool{false, true} {
		t.Run(fmt.Sprintf("transparent=%v", transparent), func(t *testing.T) {
			f := newProxySupervisorFixture(t)
			if transparent {
				f.transparent(t)
			}
			f.command.Env = append(f.command.Env, "CODEX_PROXY_HARDENED_HELPER=1")
			if err := os.WriteFile(f.release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			err := f.command.Run()
			var exit *exec.ExitError
			output, _ := os.ReadFile(f.output)
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("hardened helper: %v, output %s", err, output)
			}
			count, err := os.ReadFile(f.count)
			if err != nil || string(count) != "once\n" {
				t.Fatalf("hardened command executions: %q, %v", count, err)
			}
		})
	}
}
