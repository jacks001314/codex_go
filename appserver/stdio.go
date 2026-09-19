package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codex_go/session"
)

type StdioServer struct {
	router *RuntimeRouter
}

type StdioOptions struct {
	CodexHome      string
	StoreRoot      string
	RuntimeOptions *RuntimeRouterOptions
}

func NewStdioServer(router *RuntimeRouter) *StdioServer {
	// Rust stamps `rpc.transport = "stdio"` on the request span.
	router.SetRequestTransport("stdio")
	return &StdioServer{router: router}
}

func NewDefaultStdioServer(options *StdioOptions) *StdioServer {
	if options == nil {
		options = &StdioOptions{}
	}
	codexHome := strings.TrimSpace(options.CodexHome)
	if codexHome == "" {
		codexHome = ".gcode"
	}
	storeRoot := strings.TrimSpace(options.StoreRoot)
	if storeRoot == "" {
		storeRoot = filepath.Join(codexHome, "sessions")
	}
	store := session.NewStore(storeRoot)
	prepared, ownedStateRuntime, err := prepareSharedStateRuntime(context.Background(), codexHome, options.RuntimeOptions)
	if ownedStateRuntime != nil {
		prepared.closeStateRuntimeOnRouterClose = true
	}
	if prepared != nil && prepared.logDBInstallation != nil {
		prepared.closeLogDBInstallationOnRouterClose = true
	}
	router := NewDefaultRuntimeRouterWithOptions(store, codexHome, prepared)
	router.startupErr = err
	return NewStdioServer(router)
}

func (s *StdioServer) Serve(stdin io.Reader, stdout io.Writer) error {
	if s == nil || s.router == nil {
		return errors.New("app-server stdio router is not configured")
	}
	defer s.router.Close()
	if err := s.router.StartupError(); err != nil {
		return err
	}
	return serveJSONLineConnection(s.router, stdin, stdout)
}

// stdioShutdownWatchdogTimeout bounds app-server teardown after EOF or SIGTERM
// (Rust #44523).
const stdioShutdownWatchdogTimeout = 45 * time.Second

// ServeWithShutdown runs the standalone app-server stdio connection with
// EOF/SIGTERM cleanup and a bounded shutdown watchdog. On Unix, SIGTERM cancels
// the connection so cleanup runs (owned commands terminate, session-end hooks
// execute); if teardown stalls past the watchdog the process exits with status
// 1 instead of hanging.
func (s *StdioServer) ServeWithShutdown(stdin io.Reader, stdout io.Writer) error {
	if s == nil || s.router == nil {
		return errors.New("app-server stdio router is not configured")
	}
	if err := s.router.StartupError(); err != nil {
		return err
	}
	watchdog := &stdioShutdownWatchdog{timeout: stdioShutdownWatchdogTimeout}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopSignals := installStdioShutdownSignals(ctx, cancel, watchdog.arm)
	defer stopSignals()
	err := serveJSONLineConnectionContext(s.router, ctx, stdin, stdout, watchdog.arm)
	s.router.Close()
	return err
}

type stdioShutdownWatchdog struct {
	timeout time.Duration
	once    sync.Once
	// onTimeout overrides the default exit behavior in tests.
	onTimeout func()
}

// arm starts the shutdown deadline. The first EOF or SIGTERM wins so a later
// signal never extends the deadline.
func (w *stdioShutdownWatchdog) arm() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		time.AfterFunc(w.timeout, func() {
			if w.onTimeout != nil {
				w.onTimeout()
				return
			}
			fmt.Fprintln(os.Stderr, "app-server shutdown timed out; exiting")
			os.Exit(1)
		})
	})
}

func serveJSONLineConnection(router *RuntimeRouter, stdin io.Reader, stdout io.Writer) error {
	return serveJSONLineConnectionContext(router, context.Background(), stdin, stdout, nil)
}

// serveJSONLineConnectionContext serves a JSON-line connection until stdin
// reaches EOF or ctx is cancelled (Rust #44523). onShutdown runs once when the
// read loop ends so callers can arm a bounded shutdown watchdog before cleanup.
func serveJSONLineConnectionContext(router *RuntimeRouter, ctx context.Context, stdin io.Reader, stdout io.Writer, onShutdown func()) error {
	if router == nil {
		return errors.New("app-server json-line router is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	connectionID := "stdio"
	var writeMu sync.Mutex
	var errMu sync.Mutex
	var writeErr error
	setWriteErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		defer errMu.Unlock()
		if writeErr == nil {
			writeErr = err
		}
	}
	writeJSONLine := func(value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err = stdout.Write(append(data, '\n'))
		return err
	}
	router.SetNotificationSink(connectionNotificationSink{
		connectionID: connectionID,
		send: func(notification *Notification) {
			setWriteErr(writeJSONLine(notification))
		},
	})
	router.SetDeferredGoalNotifications(true)
	defer router.SetDeferredGoalNotifications(false)
	router.SetServerRequestSink(connectionServerRequestSink{
		connectionID: connectionID,
		send: func(request *ServerRequest) {
			setWriteErr(writeJSONLine(request))
		},
	})
	defer router.SetNotificationSink(nil)
	defer router.SetServerRequestSink(nil)

	var requests sync.WaitGroup
	started := make(chan struct{})
	close(started)
	lineCh := make(chan string)
	scanErrCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			select {
			case lineCh <- scanner.Text():
			case <-ctx.Done():
				scanErrCh <- nil
				return
			}
		}
		scanErrCh <- scanner.Err()
	}()
	var scanErr error
readLoop:
	for {
		select {
		case <-ctx.Done():
			break readLoop
		case err := <-scanErrCh:
			scanErr = err
			break readLoop
		case rawLine := <-lineCh:
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			response, request := decodeJSONLine(router, []byte(line))
			if request == nil {
				if response != nil {
					setWriteErr(writeJSONLine(response))
				}
				continue
			}
			request.ConnectionID = connectionID
			waitForPrevious := started
			started = make(chan struct{})
			startedForRequest := started
			requests.Add(1)
			go func(request *Request) {
				defer requests.Done()
				<-waitForPrevious
				if processID := commandExecProcessIDForStdioOrdering(request); processID != "" {
					go func() {
						waitForCommandExecRegistration(router, processID, 500*time.Millisecond)
						close(startedForRequest)
					}()
				} else if request.Method != MethodInitialize {
					close(startedForRequest)
				}
				response := router.Handle(request)
				if request.Method == MethodInitialize {
					close(startedForRequest)
				}
				if response != nil {
					setWriteErr(writeJSONLine(response))
					if request.Method == MethodInitialize && response.Error == nil {
						if notification := router.initializeRemoteControlStatusNotification(); notification != nil {
							setWriteErr(writeJSONLine(notification))
						}
						router.notifyWorkspaceRoutingToConnection(connectionID)
					}
					// Rust writes the thread/goal/* response before the
					// thread/goal/updated|cleared notification; flush any
					// notifications the handler deferred so they follow their
					// response on the wire.
					for _, notification := range router.FlushDeferredGoalNotifications(connectionID) {
						setWriteErr(writeJSONLine(notification))
					}
				}
			}(request)
		}
	}
	if onShutdown != nil {
		onShutdown()
	}
	requests.Wait()
	router.ConnectionClosed(connectionID)
	if scanErr != nil {
		return scanErr
	}
	errMu.Lock()
	defer errMu.Unlock()
	return writeErr
}

func (s *StdioServer) handleLine(data []byte) any {
	response, request := decodeJSONLine(s.router, data)
	if request == nil {
		return response
	}
	return s.router.Handle(request)
}

func (s *StdioServer) decodeLine(data []byte) (any, *Request) {
	return decodeJSONLine(s.router, data)
}

func decodeJSONLine(router *RuntimeRouter, data []byte) (any, *Request) {
	if response, ok := handleServerResponseLine(router, data); ok {
		return response, nil
	}
	if isClientNotificationLine(data) {
		return nil, nil
	}
	request, err := ParseRequest(data)
	if err != nil {
		var raw struct {
			ID RequestID `json:"id"`
		}
		_ = json.Unmarshal(data, &raw)
		return ErrorResponse(raw.ID, requestValidationErrorCode(err), fmt.Sprintf("invalid request: %v", err), nil), nil
	}
	return nil, request
}

func isClientNotificationLine(data []byte) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	if _, hasID := raw["id"]; hasID {
		return false
	}
	methodRaw, hasMethod := raw["method"]
	if !hasMethod {
		return false
	}
	var method string
	if err := json.Unmarshal(methodRaw, &method); err != nil {
		return false
	}
	return strings.TrimSpace(method) != ""
}

func commandExecProcessIDForStdioOrdering(request *Request) string {
	if request == nil || request.Method != MethodCommandExec {
		return ""
	}
	var params struct {
		ProcessID          *string `json:"processId,omitempty"`
		TTY                bool    `json:"tty,omitempty"`
		StreamStdin        bool    `json:"streamStdin,omitempty"`
		StreamStdoutStderr bool    `json:"streamStdoutStderr,omitempty"`
	}
	if err := request.DecodeParams(&params); err != nil {
		return ""
	}
	if params.ProcessID == nil || strings.TrimSpace(*params.ProcessID) == "" {
		return ""
	}
	if params.TTY || params.StreamStdin || params.StreamStdoutStderr {
		return strings.TrimSpace(*params.ProcessID)
	}
	return ""
}

func (s *StdioServer) waitForCommandExecRegistration(processID string, timeout time.Duration) {
	waitForCommandExecRegistration(s.router, processID, timeout)
}

func waitForCommandExecRegistration(router *RuntimeRouter, processID string, timeout time.Duration) {
	if router == nil || strings.TrimSpace(processID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := router.requireCommandExec().activeCommandExec(processID); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *StdioServer) handleServerResponseLine(data []byte) (any, bool) {
	return handleServerResponseLine(s.router, data)
}

func handleServerResponseLine(router *RuntimeRouter, data []byte) (any, bool) {
	var envelope struct {
		ID     RequestID       `json:"id"`
		Method *Method         `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  *ResponseError  `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, false
	}
	if envelope.Method != nil || envelope.ID.IsZero() || (len(envelope.Result) == 0 && envelope.Error == nil) {
		return nil, false
	}
	var response Response
	if err := json.Unmarshal(data, &response); err != nil {
		return ErrorResponse(envelope.ID, -32600, fmt.Sprintf("invalid response: %v", err), nil), true
	}
	if router != nil && router.resolveServerResponse(&response) {
		return nil, true
	}
	return nil, true
}
