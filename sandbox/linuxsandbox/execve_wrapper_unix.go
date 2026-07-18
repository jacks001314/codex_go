//go:build !windows

package linuxsandbox

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"

	json "github.com/goccy/go-json"
	"golang.org/x/sys/unix"
)

const (
	escalateSocketEnvVar = "CODEX_ESCALATE_SOCKET"
	execWrapperEnvVar    = "EXEC_WRAPPER"
	maxFDsPerMessage     = 16
)

type execveEscalateRequest struct {
	File    string            `json:"file"`
	Argv    []string          `json:"argv"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
}

type execveEscalateResponse struct {
	Action execveEscalateAction `json:"action"`
}

type execveEscalateAction struct {
	Kind   string
	Reason *string
}

func (a *execveEscalateAction) UnmarshalJSON(data []byte) error {
	var tag string
	if err := json.Unmarshal(data, &tag); err == nil {
		switch tag {
		case "Run", "Escalate":
			a.Kind = tag
			a.Reason = nil
			return nil
		default:
			return fmt.Errorf("unknown escalate action %q", tag)
		}
	}
	var tagged map[string]struct {
		Reason *string `json:"reason"`
	}
	if err := json.Unmarshal(data, &tagged); err != nil {
		return err
	}
	if deny, ok := tagged["Deny"]; ok {
		a.Kind = "Deny"
		a.Reason = deny.Reason
		return nil
	}
	return fmt.Errorf("unknown escalate action payload")
}

type execveSuperExecMessage struct {
	FDs []int `json:"fds"`
}

type execveSuperExecResult struct {
	ExitCode int `json:"exit_code"`
}

func RunExecveWrapperHelper(args []string, stdout, stderr io.Writer) int {
	_ = stdout
	if len(args) == 0 {
		fmt.Fprintln(stderr, "codex-execve-wrapper requires COMMAND")
		return 1
	}
	exitCode, err := runShellEscalationExecveWrapper(args[0], args[1:], stderr)
	if err != nil {
		fmt.Fprintln(stderr, "codex-execve-wrapper:", err)
		return 1
	}
	return exitCode
}

func runShellEscalationExecveWrapper(file string, argv []string, stderr io.Writer) (int, error) {
	handshakeFD, err := escalationSocketFDFromEnv()
	if err != nil {
		return 1, err
	}
	defer unix.Close(handshakeFD)

	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return 1, fmt.Errorf("failed to create escalation socket pair: %w", err)
	}
	serverFD := pair[0]
	clientFD := pair[1]
	unix.CloseOnExec(serverFD)
	unix.CloseOnExec(clientFD)
	defer unix.Close(clientFD)

	if err := sendDatagramWithFDs(handshakeFD, []byte{0}, []int{serverFD}); err != nil {
		unix.Close(serverFD)
		return 1, fmt.Errorf("failed to send handshake datagram: %w", err)
	}
	unix.Close(serverFD)

	workdir, err := os.Getwd()
	if err != nil {
		return 1, fmt.Errorf("failed to get current directory: %w", err)
	}
	if err := sendStreamFrame(clientFD, &execveEscalateRequest{
		File:    file,
		Argv:    append([]string(nil), argv...),
		Workdir: workdir,
		Env:     filteredExecveWrapperEnv(),
	}, nil); err != nil {
		return 1, fmt.Errorf("failed to send EscalateRequest: %w", err)
	}

	var response execveEscalateResponse
	if _, err := receiveStreamFrame(clientFD, &response); err != nil {
		return 1, fmt.Errorf("failed to receive EscalateResponse: %w", err)
	}
	switch response.Action.Kind {
	case "Escalate":
		return runEscalatedExecve(clientFD)
	case "Run":
		return 1, unix.Exec(file, argv, os.Environ())
	case "Deny":
		if response.Action.Reason != nil {
			fmt.Fprintf(stderr, "Execution denied: %s\n", *response.Action.Reason)
		} else {
			fmt.Fprintln(stderr, "Execution denied")
		}
		return 1, nil
	default:
		return 1, fmt.Errorf("unknown escalate action %q", response.Action.Kind)
	}
}

func runEscalatedExecve(clientFD int) (int, error) {
	fds, err := duplicateStdioFDs()
	if err != nil {
		return 1, err
	}
	defer closeFDs(fds)

	if err := sendStreamFrame(clientFD, &execveSuperExecMessage{FDs: []int{0, 1, 2}}, fds); err != nil {
		return 1, fmt.Errorf("failed to send SuperExecMessage: %w", err)
	}
	var result execveSuperExecResult
	if _, err := receiveStreamFrame(clientFD, &result); err != nil {
		return 1, err
	}
	return result.ExitCode, nil
}

func escalationSocketFDFromEnv() (int, error) {
	raw := os.Getenv(escalateSocketEnvVar)
	fd, err := strconv.Atoi(raw)
	if err != nil || fd < 0 {
		return -1, fmt.Errorf("%s is not a valid file descriptor: %s", escalateSocketEnvVar, raw)
	}
	return fd, nil
}

func filteredExecveWrapperEnv() map[string]string {
	out := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := cutExecveEnv(entry)
		if !ok || key == escalateSocketEnvVar || key == execWrapperEnvVar {
			continue
		}
		out[key] = value
	}
	return out
}

func cutExecveEnv(entry string) (string, string, bool) {
	for i := 0; i < len(entry); i++ {
		if entry[i] == '=' {
			return entry[:i], entry[i+1:], true
		}
	}
	return "", "", false
}

func duplicateStdioFDs() ([]int, error) {
	sources := []int{0, 1, 2}
	out := make([]int, 0, len(sources))
	for _, source := range sources {
		fd, err := unix.Dup(source)
		if err != nil {
			closeFDs(out)
			return nil, fmt.Errorf("failed to duplicate fd %d for escalation transfer: %w", source, err)
		}
		out = append(out, fd)
	}
	return out, nil
}

func closeFDs(fds []int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}

func sendStreamFrame(fd int, msg any, fds []int) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if uint64(len(payload)) > uint64(^uint32(0)) {
		return fmt.Errorf("message too large: %d", len(payload))
	}
	frame := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return writeAllWithFDs(fd, frame, fds)
}

func receiveStreamFrame(fd int, out any) ([]int, error) {
	header := make([]byte, 4)
	fds, err := readAllWithFDs(fd, header)
	if err != nil {
		return nil, err
	}
	payloadLen := binary.LittleEndian.Uint32(header)
	payload := make([]byte, int(payloadLen))
	if len(payload) > 0 {
		moreFDs, err := readAllWithFDs(fd, payload)
		if err != nil {
			closeFDs(fds)
			return nil, err
		}
		fds = append(fds, moreFDs...)
	}
	if err := json.Unmarshal(payload, out); err != nil {
		closeFDs(fds)
		return nil, err
	}
	return fds, nil
}

func sendDatagramWithFDs(fd int, payload []byte, fds []int) error {
	oob := rightsMessage(fds)
	for {
		n, err := unix.SendmsgN(fd, payload, oob, nil, 0)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n != len(payload) {
			return io.ErrShortWrite
		}
		return nil
	}
}

func writeAllWithFDs(fd int, payload []byte, fds []int) error {
	includeFDs := len(fds) > 0
	for len(payload) > 0 {
		oob := []byte(nil)
		if includeFDs {
			oob = rightsMessage(fds)
		}
		n, err := unix.SendmsgN(fd, payload, oob, nil, 0)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
		includeFDs = false
	}
	return nil
}

func readAllWithFDs(fd int, dst []byte) ([]int, error) {
	fds := []int(nil)
	filled := 0
	captureRights := true
	for filled < len(dst) {
		oob := make([]byte, unix.CmsgSpace(4*maxFDsPerMessage))
		if !captureRights {
			oob = nil
		}
		n, oobn, _, _, err := unix.Recvmsg(fd, dst[filled:], oob, 0)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			closeFDs(fds)
			return nil, err
		}
		if n == 0 {
			closeFDs(fds)
			return nil, io.ErrUnexpectedEOF
		}
		if captureRights && oobn > 0 {
			rights, err := parseRights(oob[:oobn])
			if err != nil {
				closeFDs(fds)
				return nil, err
			}
			fds = append(fds, rights...)
		}
		captureRights = false
		filled += n
	}
	return fds, nil
}

func rightsMessage(fds []int) []byte {
	if len(fds) == 0 {
		return nil
	}
	return unix.UnixRights(fds...)
}

func parseRights(oob []byte) ([]int, error) {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, message := range messages {
		rights, err := unix.ParseUnixRights(&message)
		if err != nil {
			closeFDs(fds)
			return nil, err
		}
		fds = append(fds, rights...)
	}
	return fds, nil
}
