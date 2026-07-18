//go:build !windows

package linuxsandbox

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func TestExecveWrapperDenyResponseReturnsOne(t *testing.T) {
	handshake, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatalf("Socketpair() error = %v", err)
	}
	clientHandshakeFD := handshake[0]
	serverHandshakeFD := handshake[1]
	defer unix.Close(serverHandshakeFD)
	t.Setenv(escalateSocketEnvVar, strconv.Itoa(clientHandshakeFD))
	t.Setenv(execWrapperEnvVar, "/tmp/codex-execve-wrapper")
	t.Setenv("CODEx_TEST_EXECVE_WRAPPER", "kept")

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- func() error {
			_, fds, err := receiveTestDatagramWithFDs(serverHandshakeFD)
			if err != nil {
				return err
			}
			defer closeFDs(fds)
			if len(fds) != 1 {
				return fmt.Errorf("handshake fds = %d, want 1", len(fds))
			}
			var request execveEscalateRequest
			receivedFDs, err := receiveStreamFrame(fds[0], &request)
			closeFDs(receivedFDs)
			if err != nil {
				return err
			}
			if request.File != "/bin/echo" {
				return fmt.Errorf("request file = %q", request.File)
			}
			if len(request.Argv) != 2 || request.Argv[0] != "echo" || request.Argv[1] != "hi" {
				return fmt.Errorf("request argv = %#v", request.Argv)
			}
			if request.Workdir == "" {
				return fmt.Errorf("request workdir is empty")
			}
			if _, ok := request.Env[escalateSocketEnvVar]; ok {
				return fmt.Errorf("request env retained %s", escalateSocketEnvVar)
			}
			if _, ok := request.Env[execWrapperEnvVar]; ok {
				return fmt.Errorf("request env retained %s", execWrapperEnvVar)
			}
			if request.Env["CODEx_TEST_EXECVE_WRAPPER"] != "kept" {
				return fmt.Errorf("request env did not retain ordinary key: %#v", request.Env)
			}
			return sendStreamFrame(fds[0], map[string]any{
				"action": map[string]any{
					"Deny": map[string]any{"reason": "blocked"},
				},
			}, nil)
		}()
	}()

	var stderr bytes.Buffer
	code, err := runShellEscalationExecveWrapper("/bin/echo", []string{"echo", "hi"}, &stderr)
	if err != nil {
		t.Fatalf("runShellEscalationExecveWrapper() error = %v", err)
	}
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if got := stderr.String(); got != "Execution denied: blocked\n" {
		t.Fatalf("stderr = %q", got)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestExecveWrapperEscalateTransfersStdioFDs(t *testing.T) {
	handshake, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatalf("Socketpair() error = %v", err)
	}
	clientHandshakeFD := handshake[0]
	serverHandshakeFD := handshake[1]
	defer unix.Close(serverHandshakeFD)
	t.Setenv(escalateSocketEnvVar, strconv.Itoa(clientHandshakeFD))

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- func() error {
			_, fds, err := receiveTestDatagramWithFDs(serverHandshakeFD)
			if err != nil {
				return err
			}
			defer closeFDs(fds)
			if len(fds) != 1 {
				return fmt.Errorf("handshake fds = %d, want 1", len(fds))
			}
			var request execveEscalateRequest
			receivedFDs, err := receiveStreamFrame(fds[0], &request)
			closeFDs(receivedFDs)
			if err != nil {
				return err
			}
			if err := sendStreamFrame(fds[0], map[string]any{"action": "Escalate"}, nil); err != nil {
				return err
			}
			var message execveSuperExecMessage
			stdioFDs, err := receiveStreamFrame(fds[0], &message)
			if err != nil {
				return err
			}
			defer closeFDs(stdioFDs)
			if len(message.FDs) != 3 || message.FDs[0] != 0 || message.FDs[1] != 1 || message.FDs[2] != 2 {
				return fmt.Errorf("SuperExecMessage fds = %#v", message.FDs)
			}
			if len(stdioFDs) != 3 {
				return fmt.Errorf("transferred fds = %d, want 3", len(stdioFDs))
			}
			for _, fd := range stdioFDs {
				if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
					return fmt.Errorf("received invalid fd %d: %w", fd, err)
				}
			}
			return sendStreamFrame(fds[0], &execveSuperExecResult{ExitCode: 42}, nil)
		}()
	}()

	code, err := runShellEscalationExecveWrapper("/bin/echo", []string{"echo"}, os.Stderr)
	if err != nil {
		t.Fatalf("runShellEscalationExecveWrapper() error = %v", err)
	}
	if code != 42 {
		t.Fatalf("exit code = %d, want 42", code)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func receiveTestDatagramWithFDs(fd int) ([]byte, []int, error) {
	payload := make([]byte, 8192)
	oob := make([]byte, unix.CmsgSpace(4*maxFDsPerMessage))
	n, oobn, _, _, err := unix.Recvmsg(fd, payload, oob, 0)
	if err != nil {
		return nil, nil, err
	}
	fds, err := parseRights(oob[:oobn])
	if err != nil {
		return nil, nil, err
	}
	return payload[:n], fds, nil
}
