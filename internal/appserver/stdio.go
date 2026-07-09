package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codex_go/internal/session"
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
	return &StdioServer{router: router}
}

func NewDefaultStdioServer(options *StdioOptions) *StdioServer {
	if options == nil {
		options = &StdioOptions{}
	}
	codexHome := strings.TrimSpace(options.CodexHome)
	if codexHome == "" {
		codexHome = ".codex"
	}
	storeRoot := strings.TrimSpace(options.StoreRoot)
	if storeRoot == "" {
		storeRoot = filepath.Join(codexHome, "sessions")
	}
	store := session.NewStore(storeRoot)
	return NewStdioServer(NewDefaultRuntimeRouterWithOptions(store, codexHome, options.RuntimeOptions))
}

func (s *StdioServer) Serve(stdin io.Reader, stdout io.Writer) error {
	if s == nil || s.router == nil {
		return errors.New("app-server stdio router is not configured")
	}
	defer s.router.Close()
	return serveJSONLineConnection(s.router, stdin, stdout)
}

func serveJSONLineConnection(router *RuntimeRouter, stdin io.Reader, stdout io.Writer) error {
	if router == nil {
		return errors.New("app-server json-line router is not configured")
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
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
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
				}
			}
		}(request)
	}
	scanErr := scanner.Err()
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
		return ErrorResponse(raw.ID, -32600, fmt.Sprintf("invalid request: %v", err), nil), nil
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
