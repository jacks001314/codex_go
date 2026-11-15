//go:build linux

package linuxsandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// runTransparentTCPWorkload serves the userspace relay and runs the sandboxed
// command as its child. The command receives the seccomp filter while the relay
// keeps the TUN data path alive for the whole sandbox lifetime.
func runTransparentTCPWorkload(cmd *linuxSandboxCommand, routing linuxProxyRouting, socksBridge *net.TCPAddr) error {
	relay, err := prepareTransparentTCPRelay(cmd.TransparentTCPFDs, socksBridge)
	if err != nil {
		return err
	}
	defer relay.close()
	if err := relay.start(context.Background()); err != nil {
		return err
	}
	if err := applyLinuxPermissionProfile(cmd.PermissionProfile, cmd.SandboxPolicyCWD, false, true, routing, cmd.ManagedNetwork); err != nil {
		return fmt.Errorf("error applying Linux sandbox restrictions: %w", err)
	}
	return runTransparentTCPCommand(cmd)
}

func runTransparentTCPCommand(cmd *linuxSandboxCommand) error {
	if len(cmd.Command) == 0 {
		return errors.New("no command specified to execute")
	}
	program := cmd.Command[0]
	if !strings.ContainsRune(program, '/') {
		resolved, err := exec.LookPath(program)
		if err != nil {
			return err
		}
		program = resolved
	}
	child := exec.Command(program, cmd.Command[1:]...)
	child.Dir = cmd.CommandCWD
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		return err
	}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			if child.Process != nil {
				_ = child.Process.Signal(sig)
			}
		}
	}()
	err := child.Wait()
	if err == nil {
		return &transparentTCPChildExitError{}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code < 0 {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				code = 128 + int(status.Signal())
			}
		}
		return &transparentTCPChildExitError{code: code}
	}
	return fmt.Errorf("failed to wait for sandboxed command: %w", err)
}

// prepareTransparentTCPResolverMount validates the command-local resolver and
// returns the memfd bubblewrap binds over /etc/resolv.conf.
func prepareTransparentTCPResolverMount(cmd *linuxSandboxCommand) (*os.File, string, error) {
	logical := "/etc/resolv.conf"
	target, err := filepath.EvalSymlinks(logical)
	if err != nil {
		return nil, "", fmt.Errorf("transparent TCP requires readable /etc/resolv.conf: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", errors.New("transparent TCP requires readable /etc/resolv.conf")
	}
	readable, err := transparentTCPResolverReadable(cmd.PermissionProfile.Filesystem, cmd.SandboxPolicyCWD, logical, target)
	if err != nil {
		return nil, "", err
	}
	if !readable {
		return nil, "", errors.New("transparent TCP requires readable /etc/resolv.conf")
	}
	resolver, err := createTransparentTCPResolver()
	if err != nil {
		return nil, "", err
	}
	return resolver, target, nil
}

func transparentTCPResolverReadable(policy *linuxFilesystemPolicy, cwd, logical, target string) (bool, error) {
	resolved, err := policy.resolveForEnforcement(cwd, true)
	if err != nil {
		return false, err
	}
	for _, path := range []string{logical, target} {
		for _, denied := range resolved.UnreadableRoots {
			if pathWithinRoot(path, denied) {
				return false, nil
			}
		}
		if policy != nil && policy.hasFullDiskReadAccess() {
			continue
		}
		allowed := false
		for _, root := range resolved.ReadableRoots {
			if pathWithinRoot(path, root) {
				allowed = true
				break
			}
		}
		if !allowed && policy != nil && policy.includePlatformDefaults() {
			for _, root := range linuxPlatformDefaultReadRoots {
				if pathWithinRoot(path, root) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			return false, nil
		}
	}
	return true, nil
}

// insertTransparentTCPResolverArgs binds the resolver descriptor right before
// bubblewrap's command separator so it wins over every earlier root bind.
func insertTransparentTCPResolverArgs(args []string, target string) ([]string, error) {
	separator := -1
	for index, argument := range args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		return nil, errors.New("bubblewrap argv is missing command separator")
	}
	out := make([]string, 0, len(args)+5)
	out = append(out, args[:separator]...)
	out = append(out, "--perms", "0444", "--ro-bind-data", "4", target)
	out = append(out, args[separator:]...)
	return out, nil
}
