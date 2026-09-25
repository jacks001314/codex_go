package execserver

// Stdio exec-server client transport.
//
// Rust parity: codex-exec-server's client_transport::connect_stdio_command.
// A stdio environment spawns its program and speaks line-delimited JSON-RPC
// over the child's stdin/stdout, which is how `environments.toml` entries that
// configure `program` are reached.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// stdioMaxLineBytes bounds one JSON-RPC frame on the stdio transport.
	stdioMaxLineBytes = 64 * 1024 * 1024
)

type stdioClientConnection struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	lines chan stdioLine
	done  chan struct{}

	writeMu   sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

type stdioLine struct {
	data []byte
	err  error
}

// spawnStdioClientConnection starts program and wires its stdio to a
// JSON-RPC connection.
func spawnStdioClientConnection(command *StdioExecServerCommand) (*stdioClientConnection, error) {
	if command == nil {
		return nil, errors.New("exec-server stdio command is required")
	}
	program := strings.TrimSpace(command.Program)
	if program == "" {
		return nil, errors.New("exec-server stdio command program is required")
	}
	child := exec.Command(program, command.Args...)
	if len(command.Env) > 0 {
		env := os.Environ()
		names := make([]string, 0, len(command.Env))
		for name := range command.Env {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			env = append(env, name+"="+command.Env[name])
		}
		child.Env = env
	}
	if strings.TrimSpace(command.CWD) != "" {
		child.Dir = command.CWD
	}
	// The child shares the process's stderr drain behavior: its own stderr is
	// discarded unless a caller wraps the command.
	child.Stderr = io.Discard
	stdin, err := child.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := child.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	connection := &stdioClientConnection{
		cmd:   child,
		stdin: stdin,
		lines: make(chan stdioLine, 16),
		done:  make(chan struct{}),
	}
	go connection.readLoop(stdout)
	return connection, nil
}

// readLoop publishes one JSON-RPC frame per line.
func (c *stdioClientConnection) readLoop(stdout io.ReadCloser) {
	defer close(c.lines)
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, err := readStdioLine(reader)
		if len(line) > 0 {
			select {
			case c.lines <- stdioLine{data: line}:
			case <-c.done:
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				select {
				case c.lines <- stdioLine{err: err}:
				case <-c.done:
				}
			}
			return
		}
	}
}

func readStdioLine(reader *bufio.Reader) ([]byte, error) {
	var buffer []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		buffer = append(buffer, chunk...)
		if len(buffer) > stdioMaxLineBytes {
			return nil, errors.New("exec-server stdio frame exceeds the maximum size")
		}
		if err == nil {
			return trimStdioFrame(buffer), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return trimStdioFrame(buffer), err
	}
}

func trimStdioFrame(buffer []byte) []byte {
	trimmed := buffer
	for len(trimmed) > 0 {
		last := trimmed[len(trimmed)-1]
		if last == '\n' || last == '\r' || last == ' ' || last == '\t' {
			trimmed = trimmed[:len(trimmed)-1]
			continue
		}
		break
	}
	if len(trimmed) == 0 {
		return nil
	}
	return append([]byte(nil), trimmed...)
}

func (c *stdioClientConnection) Read(ctx context.Context) ([]byte, error) {
	if c == nil {
		return nil, errors.New("exec-server stdio connection is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case line, ok := <-c.lines:
		if !ok {
			return nil, io.EOF
		}
		if line.err != nil {
			return nil, line.err
		}
		return line.data, nil
	case <-c.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *stdioClientConnection) Write(ctx context.Context, data []byte) error {
	if c == nil {
		return errors.New("exec-server stdio connection is closed")
	}
	select {
	case <-c.done:
		return io.EOF
	default:
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	frame := append(append([]byte(nil), data...), '\n')
	_, err := c.stdin.Write(frame)
	return err
}

// Close shuts the child down after draining, mirroring Rust's child-process
// drop policy (close stdin, then terminate).
func (c *stdioClientConnection) Close() error {
	return c.close(false)
}

// CloseNow terminates the child immediately.
func (c *stdioClientConnection) CloseNow() error {
	return c.close(true)
}

func (c *stdioClientConnection) close(force bool) error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		close(c.done)
		// Closing stdin signals EOF, which lets a well-behaved exec-server exit
		// on its own (Rust's child drop policy closes the pipes before killing).
		_ = c.stdin.Close()
		if c.cmd == nil || c.cmd.Process == nil {
			return
		}
		waited := make(chan struct{})
		go func() {
			_ = c.cmd.Wait()
			close(waited)
		}()
		if !force {
			// Give the child a brief window to exit on stdin EOF.
			select {
			case <-waited:
				return
			case <-time.After(2 * time.Second):
			}
		}
		// Termination is best effort: a child that already exited (or that the
		// platform refuses to terminate) leaves nothing to clean up.
		_ = c.cmd.Process.Kill()
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
		}
	})
	return c.closeErr
}

// dialStdioClientConnection spawns the configured program and initializes the
// exec-server session over its stdio.
func dialStdioClientConnection(
	ctx context.Context,
	command *StdioExecServerCommand,
	clientName string,
	resumeSessionID string,
	handleNotification func(string, json.RawMessage) error,
) (clientConnection, *InitializeResponse, error) {
	connection, err := spawnStdioClientConnection(command)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to spawn exec-server stdio command: %w", err)
	}
	initialized, err := initializeClientConnection(ctx, connection, clientName, resumeSessionID, handleNotification)
	if err != nil {
		_ = connection.CloseNow()
		return nil, nil, err
	}
	return connection, initialized, nil
}

// dialStdioCommandClient builds a client whose connection opener spawns the
// configured stdio program, so recoveries respawn it the way Rust's stdio
// reconnect strategy does.
func dialStdioCommandClient(ctx context.Context, clientName string, options DialClientOptions) (*Client, error) {
	client := &Client{
		clientName:   clientName,
		nextID:       1,
		nextHTTPID:   1,
		pending:      map[int64]chan clientCallResult{},
		sessions:     map[string]*clientProcessSession{},
		httpStreams:  map[string]*HTTPBodyStream{},
		inboundIDs:   map[int64]struct{}{},
		inboundSlots: make(chan struct{}, MaxInFlightServerRequests),
		done:         make(chan struct{}),
		// A stdio child cannot hand its session to a fresh process, so the
		// stdio transport has no reconnect strategy (Rust registers stdio
		// connections without ExecServerReconnectStrategy).
		reconnectDisabled: true,
	}
	client.open = func(openCtx context.Context, resumeSessionID string, handleNotification func(string, json.RawMessage) error) (clientConnection, *InitializeResponse, error) {
		return dialStdioClientConnection(openCtx, options.StdioCommand, clientName, resumeSessionID, handleNotification)
	}
	// A stdio connect never resumes a session (Rust passes
	// `resume_session_id: None` for stdio transports).
	conn, initialized, err := client.open(ctx, "", client.handleNotification)
	if err != nil {
		return nil, err
	}
	client.conn = conn
	client.sessionID = initialized.SessionID
	go client.readLoop(conn)
	return client, nil
}
