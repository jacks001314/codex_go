//go:build linux

package linuxsandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	json "github.com/goccy/go-json"
	"golang.org/x/sys/unix"
)

const proxyBwrapExecArg = "--proxy-bwrap-exec"

// hostProxyBridges belongs to the outer launcher process. Re-exec gives bwrap
// a separate descriptor table; neither listeners nor active upstream sockets
// can leak into the sandbox or keep serving after this process dies.
type hostProxyBridges struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	directory           string
	listeners           []net.Listener
	mu                  sync.Mutex
	closed              bool
	cleanupBySupervisor bool
	conns               map[net.Conn]struct{}
	wg                  sync.WaitGroup
}

func prepareHostProxyRouteSpec(ctx context.Context, network *linuxManagedNetworkContext) (string, *hostProxyBridges, error) {
	ctx, cancel := context.WithCancel(ctx)
	b := &hostProxyBridges{ctx: ctx, cancel: cancel, conns: make(map[net.Conn]struct{})}
	complete := false
	defer func() {
		if !complete {
			b.close()
		}
	}()
	if network == nil {
		network = &linuxManagedNetworkContext{}
	}
	routes, configured, err := planProxyRoutesFromEnv(network.LoopbackPorts)
	if err != nil {
		return "", nil, err
	}
	if len(routes) == 0 && len(network.LoopbackPorts) != 0 {
		if configured {
			return "", nil, errors.New("managed proxy mode requires a proxy endpoint on an allowed loopback port")
		}
		return "", nil, errors.New("managed proxy mode requires proxy environment variables")
	}
	if len(routes) != 0 {
		b.directory, err = createProxySocketDir()
		if err != nil {
			return "", nil, err
		}
	}
	byEndpoint := make(map[string]string)
	spec := proxyRouteSpec{}
	for _, route := range routes {
		path, ok := byEndpoint[route.Endpoint]
		if !ok {
			path = filepath.Join(b.directory, fmt.Sprintf("proxy-route-%d.sock", len(byEndpoint)))
			listener, err := net.Listen("unix", path)
			if err != nil {
				return "", nil, err
			}
			b.listeners = append(b.listeners, listener)
			b.wg.Add(1)
			go b.serve(listener, route.Endpoint)
			byEndpoint[route.Endpoint] = path
		}
		spec.Routes = append(spec.Routes, proxyRouteEntry{EnvKey: route.EnvKey, UDSPath: path})
	}
	data, err := json.Marshal(&spec)
	if err != nil {
		return "", nil, err
	}
	complete = true
	return string(data), b, nil
}

func (b *hostProxyBridges) track(conn net.Conn) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		_ = conn.Close()
		return false
	}
	b.conns[conn] = struct{}{}
	return true
}

func (b *hostProxyBridges) release(conn net.Conn) {
	_ = conn.Close()
	b.mu.Lock()
	delete(b.conns, conn)
	b.mu.Unlock()
}

func (b *hostProxyBridges) serve(listener net.Listener, endpoint string) {
	defer b.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
				// Descriptor exhaustion must not permanently disable routing.
				select {
				case <-b.ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
					continue
				}
			}
			return
		}
		if !b.track(conn) {
			return
		}
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			defer b.release(conn)
			upstream, err := (&net.Dialer{}).DialContext(b.ctx, "tcp", endpoint)
			if err != nil {
				return
			}
			if !b.track(upstream) {
				return
			}
			defer b.release(upstream)
			proxyBidirectional(conn, upstream)
		}()
	}
}

func (b *hostProxyBridges) close() {
	b.cancel()
	for _, listener := range b.listeners {
		_ = listener.Close()
	}
	b.mu.Lock()
	b.closed = true
	for conn := range b.conns {
		_ = conn.Close()
	}
	b.mu.Unlock()
	b.wg.Wait()
	if b.directory != "" && !b.cleanupBySupervisor {
		// This directory was created by this invocation and is not a user path.
		_ = os.RemoveAll(b.directory)
	}
}

type proxyBwrapPayload struct {
	Parent  int
	Command *linuxSandboxCommand
}

type linuxSandboxChildExit struct{ status syscall.WaitStatus }

func (e *linuxSandboxChildExit) Error() string { return "sandbox process exited" }

func runManagedProxySandbox(ctx context.Context, cmd *linuxSandboxCommand) error {
	// Pdeathsig refers to the forking OS thread. Keep that thread alive until
	// the worker is reaped, rather than letting the Go runtime retire it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, stopWatching, err := watchProxySandboxCaller(ctx, cmd.callerPID)
	if err != nil {
		return err
	}
	defer stopWatching()
	spec, bridges, err := prepareHostProxyRouteSpec(ctx, cmd.ManagedNetwork)
	if err != nil {
		return fmt.Errorf("failed to prepare host proxy routing bridge: %w", err)
	}
	defer bridges.close()
	copy := *cmd
	copy.ProxyRouteSpec = spec
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create bubblewrap readiness pipe: %w", err)
	}
	defer readyReader.Close()
	defer readyWriter.Close()
	child, payload, err := proxyBwrapChild(&copy, readyWriter)
	if err != nil {
		return err
	}
	defer payload.Close()
	if err := child.Start(); err != nil {
		return fmt.Errorf("start supervised bubblewrap stage: %w", err)
	}
	var mountCleanup *mountCleanupSupervisor
	if mountCleanupSupervisorEnabled() && bridges.directory != "" {
		mountCleanup, err = startMountCleanupSupervisor(child.Process.Pid, []string{bridges.directory})
		if err != nil {
			// The supervised stage must not outlive a failed handoff; the route
			// directory stays with this process because ownership never moved.
			forwardProxySandboxSignal(child.Process.Pid, syscall.SIGKILL)
			_ = child.Wait()
			return fmt.Errorf("start mount cleanup supervisor: %w", err)
		}
		bridges.cleanupBySupervisor = true
	}
	_ = payload.Close()
	_ = readyWriter.Close()
	readDone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, readyReader); close(readDone) }()
	defer func() { _ = readyReader.Close(); <-readDone }()
	var ready <-chan struct{} = readDone
	var pending []syscall.Signal
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	canceled := ctx.Done()
	for {
		select {
		case sig := <-signals:
			if ready == nil {
				forwardProxySandboxSignal(child.Process.Pid, sig.(syscall.Signal))
			} else {
				// Coalesce each of the four signals until exec resets Go's
				// handlers, or the transparent stage installs its forwarders.
				found := false
				for _, previous := range pending {
					found = found || previous == sig
				}
				if !found {
					pending = append(pending, sig.(syscall.Signal))
				}
			}
		case <-ready:
			ready = nil
			for _, sig := range pending {
				forwardProxySandboxSignal(child.Process.Pid, sig)
			}
			pending = nil
		case <-canceled:
			forwardProxySandboxSignal(child.Process.Pid, syscall.SIGKILL)
			canceled = nil
		case err := <-waited:
			if mountCleanup != nil {
				if cleanupErr := mountCleanup.wait(); cleanupErr != nil {
					return fmt.Errorf("mount cleanup supervisor: %w", cleanupErr)
				}
			}
			if err == nil {
				return &linuxSandboxChildExit{}
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return &linuxSandboxChildExit{status: exit.Sys().(syscall.WaitStatus)}
			}
			return fmt.Errorf("wait for supervised bubblewrap stage: %w", err)
		}
	}
}

func proxyBwrapChild(cmd *linuxSandboxCommand, readyWriter *os.File) (*exec.Cmd, *os.File, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	fd, err := unix.MemfdCreate("codex-proxy-bwrap", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, nil, fmt.Errorf("create bubblewrap handoff: %w", err)
	}
	payload := os.NewFile(uintptr(fd), "codex-proxy-bwrap")
	if err := json.NewEncoder(payload).Encode(proxyBwrapPayload{Parent: os.Getpid(), Command: cmd}); err != nil {
		_ = payload.Close()
		return nil, nil, err
	}
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		_ = payload.Close()
		return nil, nil, err
	}
	child := exec.Command(executable, proxyBwrapExecArg)
	child.Args[0] = os.Args[0]
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	// ExtraFiles is the complete handoff. Bridge descriptors remain CLOEXEC.
	child.ExtraFiles = []*os.File{payload, readyWriter}
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	return child, payload, nil
}

func runProxyBwrapExec() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, err := unixFcntlSetCloexec(4); err != nil {
		return fmt.Errorf("protect bubblewrap readiness pipe: %w", err)
	}
	file := os.NewFile(3, "codex-proxy-bwrap")
	var payload proxyBwrapPayload
	err := json.NewDecoder(file).Decode(&payload)
	_ = file.Close()
	if err != nil {
		return fmt.Errorf("decode bubblewrap handoff: %w", err)
	}
	if payload.Parent <= 1 || payload.Parent != os.Getppid() {
		return errors.New("proxy supervisor exited before bubblewrap handoff")
	}
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0); err != nil {
		return fmt.Errorf("protect bubblewrap lifetime: %w", err)
	}
	if payload.Parent != os.Getppid() {
		return errors.New("proxy supervisor exited during bubblewrap handoff")
	}
	cmd := payload.Command
	if cmd == nil || cmd.PermissionProfile == nil || cmd.PermissionProfile.Filesystem == nil || len(cmd.Command) == 0 || !cmd.managedNetworkEnabled() || cmd.ApplySeccompThenExec || cmd.UseLegacyLandlock || strings.TrimSpace(cmd.ProxyRouteSpec) == "" {
		return errors.New("invalid supervised bubblewrap command")
	}
	if err := validateLinuxSandboxCommand(cmd); err != nil {
		return err
	}
	inner, err := buildInnerLinuxSandboxCommand(cmd)
	if err != nil {
		return err
	}
	return execBubblewrap(cmd, inner)
}

func forwardProxySandboxSignal(pid int, sig syscall.Signal) {
	if err := syscall.Kill(-pid, sig); errors.Is(err, syscall.ESRCH) {
		_ = syscall.Kill(pid, sig)
	}
}

// watchProxySandboxCaller tracks process lifetime rather than the executor's
// forking thread. A pidfd also pins identity across PID reuse. Old kernels and
// restricted pidfd policies fall back to checking this process's parent.
func watchProxySandboxCaller(ctx context.Context, parent int) (context.Context, func(), error) {
	if parent == 0 {
		parent = os.Getppid()
	}
	if parent <= 1 || os.Getppid() != parent {
		return nil, nil, errors.New("sandbox caller exited before proxy setup")
	}
	fd, err := unix.PidfdOpen(parent, 0)
	if err != nil && !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
		return nil, nil, fmt.Errorf("watch sandbox caller: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			if os.Getppid() != parent {
				cancel()
				return
			}
			if fd < 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
				}
				continue
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
			poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			_, err := unix.Poll(poll, 100)
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if err != nil || poll[0].Revents != 0 {
				cancel()
				return
			}
		}
	}()
	return ctx, func() {
		cancel()
		<-stopped
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}, nil
}
