package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"codex_go/execserver"
	"codex_go/network"
	"codex_go/sandbox"
	"github.com/coder/websocket"
)

func TestUnifiedExecHelperProcess(t *testing.T) {
	separator := -1
	for index, arg := range os.Args {
		if arg == "--unified-exec-helper" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "echo":
		fmt.Fprintln(os.Stdout, "READY")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			fmt.Fprintln(os.Stdout, "ECHO:"+line)
			if line == "quit" {
				os.Exit(0)
			}
		}
		os.Exit(0)
	case "long":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "immediate":
		fmt.Fprint(os.Stdout, "DONE")
		os.Exit(7)
	default:
		os.Exit(2)
	}
}

func TestUnifiedExecManagerReusesTTYSessionViaWriteStdinLikeRust(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(1, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	request := &ShellRequest{
		Command:         unifiedExecHelperCommand("echo"),
		HookCommand:     "interactive helper",
		CWD:             t.TempDir(),
		TTY:             true,
		YieldTimeMS:     unifiedExecMinYieldMS,
		TimeoutMS:       15_000,
		MaxOutputTokens: intPtr(100),
	}
	opened, err := manager.Exec(context.Background(), request, "exec-call")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if opened.ProcessID == nil || opened.HasExitCode || !strings.Contains(opened.Stdout, "READY") {
		t.Fatalf("opened = %#v", opened)
	}
	if _, err := manager.Exec(context.Background(), request, "over-limit"); !errors.Is(err, ErrUnifiedExecProcessLimit) {
		t.Fatalf("Exec(over limit) error = %v, want ErrUnifiedExecProcessLimit", err)
	}

	write := NewWriteStdinExecutor(manager, intPtr(50))
	continued, err := write.Execute(context.Background(), &Invocation{
		CallID:   "write-call-1",
		ToolName: PlainName(DefaultWriteStdinToolName),
		Payload: Payload{Kind: PayloadFunction, Arguments: fmt.Sprintf(
			`{"session_id":%d,"chars":"hello\n","yield_time_ms":250,"max_output_tokens":1000}`,
			*opened.ProcessID,
		)},
	})
	if err != nil {
		t.Fatalf("write hello error = %v", err)
	}
	if continued.Data["process_id"] != *opened.ProcessID || !strings.Contains(continued.Body, "ECHO:hello") {
		t.Fatalf("continued = %#v", continued)
	}
	if _, ok := write.PostToolUsePayload(nil, continued); ok {
		t.Fatal("PostToolUsePayload(running) ok = true, want false")
	}

	completed, err := write.Execute(context.Background(), &Invocation{
		CallID:   "write-call-2",
		ToolName: PlainName(DefaultWriteStdinToolName),
		Payload: Payload{Kind: PayloadFunction, Arguments: fmt.Sprintf(
			`{"session_id":%d,"chars":"quit\n","yield_time_ms":1000}`,
			*opened.ProcessID,
		)},
	})
	if err != nil {
		t.Fatalf("write quit error = %v", err)
	}
	if completed.Data["process_id"] != nil || completed.Data["exit_code"] != 0 || !strings.Contains(completed.Body, "ECHO:quit") {
		t.Fatalf("completed = %#v", completed)
	}
	post, ok := write.PostToolUsePayload(nil, completed)
	if !ok || post.ToolUseID != "exec-call" || post.ToolInput.(map[string]any)["command"] != "interactive helper" {
		t.Fatalf("PostToolUsePayload() = %#v, %v", post, ok)
	}
	if _, err := manager.WriteStdin(context.Background(), &WriteStdinArgs{SessionID: *opened.ProcessID}, nil); !errors.Is(err, ErrUnifiedExecUnknownProcess) {
		t.Fatalf("WriteStdin(after exit) error = %v, want unknown process", err)
	}

	finished, err := manager.Exec(context.Background(), &ShellRequest{
		Command:     unifiedExecHelperCommand("immediate"),
		CWD:         t.TempDir(),
		YieldTimeMS: 1_000,
	}, "finished-call")
	if err != nil {
		t.Fatalf("Exec(immediate) error = %v", err)
	}
	if finished.ProcessID != nil || !finished.HasExitCode || finished.ExitCode != 7 || !strings.Contains(finished.Stdout, "DONE") {
		t.Fatalf("finished = %#v", finished)
	}
}

func TestUnifiedExecManagerPausesCollectionForOutOfBandElicitation(t *testing.T) {
	manager := NewUnifiedExecManager()
	defer manager.Close()
	manager.SetThreadElicitationPaused("thread-1", true)
	type result struct {
		value *ShellResult
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := manager.Exec(context.Background(), &ShellRequest{
			Command: unifiedExecHelperCommand("immediate"), CWD: t.TempDir(), YieldTimeMS: 1000,
			UnifiedExecThreadID: "thread-1", UnifiedExecTurnID: "turn-1",
		}, "paused-call")
		done <- result{value: value, err: err}
	}()
	select {
	case got := <-done:
		t.Fatalf("collect returned while paused: %#v", got)
	case <-time.After(100 * time.Millisecond):
	}
	manager.SetThreadElicitationPaused("thread-1", false)
	select {
	case got := <-done:
		if got.err != nil || got.value == nil || !got.value.HasExitCode || got.value.ExitCode != 7 {
			t.Fatalf("result=%#v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("collect did not resume")
	}
}

func TestUnifiedExecLifecycleEventsMatchRust(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(1, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	events := make(chan UnifiedExecEvent, 16)
	result, err := manager.Exec(context.Background(), &ShellRequest{
		Command:              unifiedExecHelperCommand("immediate"),
		HookCommand:          "immediate helper",
		CWD:                  t.TempDir(),
		YieldTimeMS:          1_000,
		UnifiedExecEventSink: func(event UnifiedExecEvent) { events <- event },
	}, "event-call")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !result.UnifiedExecEvented || result.ExitCode != 7 {
		t.Fatalf("result = %#v", result)
	}

	var observed []UnifiedExecEvent
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			observed = append(observed, event)
			if event.Kind == UnifiedExecEventEnd {
				if len(observed) < 3 || observed[0].Kind != UnifiedExecEventBegin {
					t.Fatalf("events = %#v", observed)
				}
				var delta strings.Builder
				for _, item := range observed {
					if item.Kind == UnifiedExecEventOutputDelta {
						delta.WriteString(item.Output)
					}
				}
				if !strings.Contains(delta.String(), "DONE") {
					t.Fatalf("delta events = %#v", observed)
				}
				if event.CallID != "event-call" || event.HookCommand != "immediate helper" || event.ExitCode != 7 || !strings.Contains(event.Output, "DONE") {
					t.Fatalf("end event = %#v", event)
				}
				if event.Duration < unifiedExecTrailingOutputGrace {
					t.Fatalf("end duration = %s, want trailing output grace", event.Duration)
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for end event; observed = %#v", observed)
		}
	}
}

func TestUnifiedExecRemoteExecServerSessionReusesStdinLikeRust(t *testing.T) {
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 3*time.Second)
	ttyAvailable, probeErr := execserver.TTYOutputAvailable(probeCtx)
	cancelProbe()
	if probeErr != nil || !ttyAvailable {
		t.Skipf("host PTY output is unavailable: %v", probeErr)
	}
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	var listenOutput lockedUnifiedExecBuffer
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- execserver.NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &listenOutput)
	}()
	remoteURL := waitUnifiedExecServerURL(t, &listenOutput)

	manager := NewUnifiedExecManagerWithOptions(2, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	opened, err := manager.Exec(context.Background(), &ShellRequest{
		Command:                  unifiedExecHelperCommand("echo"),
		HookCommand:              "remote echo helper",
		CWD:                      t.TempDir(),
		TTY:                      true,
		YieldTimeMS:              unifiedExecMinYieldMS,
		TimeoutMS:                15_000,
		UnifiedExecRemoteURL:     remoteURL,
		UnifiedExecEnvironmentID: "remote",
		UnifiedExecThreadID:      "thread-remote",
		UnifiedExecTurnID:        "turn-remote",
	}, "call-remote")
	if err != nil {
		t.Fatalf("remote Exec() error = %v", err)
	}
	if opened.ProcessID == nil || !strings.Contains(opened.Stdout, "READY") {
		t.Fatalf("opened = %#v", opened)
	}
	listed := manager.ListProcesses("thread-remote")
	if len(listed) != 1 || listed[0].ProcessID != *opened.ProcessID || listed[0].TurnID != "turn-remote" {
		t.Fatalf("remote ListProcesses() = %#v", listed)
	}
	continued, err := manager.WriteStdin(context.Background(), &WriteStdinArgs{
		SessionID:   *opened.ProcessID,
		Chars:       "hello\n",
		YieldTimeMS: 1_000,
	}, nil)
	if err != nil || continued.ProcessID == nil || !strings.Contains(continued.Stdout, "ECHO:hello") {
		t.Fatalf("remote write hello = %#v, %v", continued, err)
	}
	completed, err := manager.WriteStdin(context.Background(), &WriteStdinArgs{
		SessionID:   *opened.ProcessID,
		Chars:       "quit\n",
		YieldTimeMS: 2_000,
	}, nil)
	if err != nil || completed.ProcessID != nil || !completed.HasExitCode || completed.ExitCode != 0 || !strings.Contains(completed.Stdout, "ECHO:quit") {
		t.Fatalf("remote write quit = %#v, %v", completed, err)
	}

	cancelServer()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}

func TestUnifiedExecConsumesPushedRemoteEventsAndRecoversReplayLikeRust(t *testing.T) {
	type scenario struct {
		name      string
		legacy    bool
		replay    bool
		denied    bool
		wantReads int
		wantBytes int
	}
	for _, test := range []scenario{
		{name: "complete", wantBytes: len("complete")},
		{name: "direct-denied", denied: true},
		{name: "legacy-exit", legacy: true, denied: true, wantReads: 1},
		{name: "replay-gap", replay: true, wantReads: 1, wantBytes: 600},
	} {
		t.Run(test.name, func(t *testing.T) {
			readCount := make(chan int, 1)
			var eventMu sync.Mutex
			var observedEvents []UnifiedExecEvent
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
				if err != nil {
					return
				}
				defer conn.Close(websocket.StatusNormalClosure, "")
				reads := 0
				defer func() { readCount <- reads }()
				readRequest := func() map[string]any {
					_, data, readErr := conn.Read(context.Background())
					if readErr != nil {
						return nil
					}
					var request map[string]any
					if json.Unmarshal(data, &request) != nil {
						return nil
					}
					return request
				}
				writeJSON := func(value any) bool {
					data, marshalErr := json.Marshal(value)
					if marshalErr != nil {
						return false
					}
					return conn.Write(context.Background(), websocket.MessageText, data) == nil
				}
				initialize := readRequest()
				if initialize == nil || !writeJSON(map[string]any{"id": initialize["id"], "result": map[string]any{"sessionId": "test"}}) {
					return
				}
				if initialized := readRequest(); initialized == nil {
					return
				}
				start := readRequest()
				if start == nil {
					return
				}
				params, _ := start["params"].(map[string]any)
				processID, _ := params["processId"].(string)
				managed, _ := params["managedNetwork"].(map[string]any)
				ports, _ := managed["loopbackPorts"].([]any)
				if params["enforceManagedNetwork"] != true || len(ports) != 2 || ports[0] != float64(43123) || ports[1] != float64(48081) || managed["allowLocalBinding"] != true {
					t.Errorf("process/start managed network params = %#v", params)
				}
				sendOutput := func(seq int, output []byte) bool {
					return writeJSON(map[string]any{"method": execserver.MethodProcessOutput, "params": map[string]any{
						"processId": processID,
						"seq":       seq,
						"stream":    "stdout",
						"chunk":     base64.StdEncoding.EncodeToString(output),
					}})
				}
				if test.replay {
					for seq := 1; seq <= 600; seq++ {
						if !sendOutput(seq, []byte("x")) {
							return
						}
					}
					if !writeJSON(map[string]any{"method": execserver.MethodProcessExited, "params": map[string]any{
						"processId": processID, "seq": 601, "exitCode": 0, "sandboxDenied": false,
					}}) {
						return
					}
				}
				if !writeJSON(map[string]any{"id": start["id"], "result": map[string]any{"processId": processID}}) {
					return
				}
				if !test.replay {
					if test.name == "complete" && !sendOutput(1, []byte("complete")) {
						return
					}
					exitParams := map[string]any{"processId": processID, "seq": 1, "exitCode": 1}
					if test.name == "complete" {
						exitParams["seq"] = 2
						exitParams["exitCode"] = 0
						exitParams["sandboxDenied"] = false
					} else if !test.legacy {
						exitParams["sandboxDenied"] = true
					}
					if !writeJSON(map[string]any{"method": execserver.MethodProcessExited, "params": exitParams}) {
						return
					}
					if test.name == "complete" && !writeJSON(map[string]any{"method": execserver.MethodProcessClosed, "params": map[string]any{"processId": processID, "seq": 3}}) {
						return
					}
				}
				for {
					request := readRequest()
					if request == nil {
						return
					}
					if request["method"] != execserver.MethodProcessRead {
						continue
					}
					reads++
					chunks := []map[string]any{}
					nextSeq := 3
					closed := true
					exitCode := 1
					if test.replay {
						chunks = make([]map[string]any, 0, 600)
						for seq := 1; seq <= 600; seq++ {
							chunks = append(chunks, map[string]any{"seq": seq, "stream": "stdout", "chunk": base64.StdEncoding.EncodeToString([]byte("x"))})
						}
						nextSeq = 602
						closed = false
						exitCode = 0
					}
					if !writeJSON(map[string]any{"id": request["id"], "result": map[string]any{
						"chunks": chunks, "nextSeq": nextSeq, "exited": true, "exitCode": exitCode,
						"closed": closed, "failure": nil, "sandboxDenied": test.denied,
					}}) {
						return
					}
					if test.replay {
						_ = writeJSON(map[string]any{"method": execserver.MethodProcessClosed, "params": map[string]any{"processId": processID, "seq": 602}})
					}
				}
			}))
			defer server.Close()
			manager := NewUnifiedExecManagerWithOptions(2, unifiedExecMinEmptyPollYieldMS)
			defer manager.Close()
			result, err := manager.Exec(context.Background(), &ShellRequest{
				Command:               []string{"ignored"},
				CWD:                   t.TempDir(),
				YieldTimeMS:           2_000,
				UnifiedExecRemoteURL:  "ws" + strings.TrimPrefix(server.URL, "http"),
				EnforceManagedNetwork: true,
				ManagedNetwork: &network.ProxyManagedNetworkSandboxContext{
					LoopbackPorts:     []uint16{43123, 48081},
					AllowLocalBinding: true,
				},
				UnifiedExecEventSink: func(event UnifiedExecEvent) {
					eventMu.Lock()
					observedEvents = append(observedEvents, event)
					eventMu.Unlock()
				},
			}, "pushed-call")
			if err != nil {
				t.Fatalf("Exec() error = %v", err)
			}
			if !result.HasExitCode || result.ProcessID != nil || len(result.Stdout) != test.wantBytes {
				t.Fatalf("result = %#v", result)
			}
			select {
			case got := <-readCount:
				if got != test.wantReads {
					t.Fatalf("process/read count = %d, want %d", got, test.wantReads)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("fake exec-server did not observe client close")
			}
			time.Sleep(unifiedExecEarlyExitGrace + unifiedExecTrailingOutputGrace)
			eventMu.Lock()
			eventsSnapshot := append([]UnifiedExecEvent(nil), observedEvents...)
			eventMu.Unlock()
			if test.denied {
				if len(eventsSnapshot) != 0 {
					t.Fatalf("sandbox denial events = %#v, want none", eventsSnapshot)
				}
			} else if len(eventsSnapshot) < 2 || eventsSnapshot[0].Kind != UnifiedExecEventBegin || eventsSnapshot[len(eventsSnapshot)-1].Kind != UnifiedExecEventEnd {
				t.Fatalf("successful pushed events = %#v", eventsSnapshot)
			}
		})
	}
}

func TestUnifiedExecRecoversDisconnectedExecServerSessionLikeRust(t *testing.T) {
	oldTimeout := unifiedExecRemoteRecoveryTimeout
	oldRetry := unifiedExecRemoteRecoveryRetry
	unifiedExecRemoteRecoveryTimeout = 2 * time.Second
	unifiedExecRemoteRecoveryRetry = 20 * time.Millisecond
	defer func() {
		unifiedExecRemoteRecoveryTimeout = oldTimeout
		unifiedExecRemoteRecoveryRetry = oldRetry
	}()
	var connectionMu sync.Mutex
	connectionCount := 0
	serverErrors := make(chan error, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, &websocket.AcceptOptions{})
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		connectionMu.Lock()
		connectionCount++
		connection := connectionCount
		connectionMu.Unlock()
		readRequest := func() (map[string]any, error) {
			_, data, readErr := conn.Read(context.Background())
			if readErr != nil {
				return nil, readErr
			}
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil {
				return nil, err
			}
			return value, nil
		}
		writeJSON := func(value any) error {
			data, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				return marshalErr
			}
			return conn.Write(context.Background(), websocket.MessageText, data)
		}
		initialize, err := readRequest()
		if err != nil {
			serverErrors <- err
			return
		}
		params, _ := initialize["params"].(map[string]any)
		if connection == 1 {
			if _, ok := params["resumeSessionId"]; ok {
				serverErrors <- errors.New("first connection unexpectedly resumed a session")
				return
			}
		} else if params["resumeSessionId"] != "recover-session" {
			serverErrors <- fmt.Errorf("resumeSessionId = %#v", params["resumeSessionId"])
			return
		}
		if err := writeJSON(map[string]any{"id": initialize["id"], "result": map[string]any{"sessionId": "recover-session"}}); err != nil {
			serverErrors <- err
			return
		}
		if _, err := readRequest(); err != nil { // initialized
			serverErrors <- err
			return
		}
		if connection == 1 {
			start, err := readRequest()
			if err != nil {
				serverErrors <- err
				return
			}
			startParams, _ := start["params"].(map[string]any)
			processID, _ := startParams["processId"].(string)
			if err := writeJSON(map[string]any{"id": start["id"], "result": map[string]any{"processId": processID}}); err != nil {
				serverErrors <- err
				return
			}
			if err := writeJSON(map[string]any{"method": execserver.MethodProcessOutput, "params": map[string]any{
				"processId": processID,
				"seq":       1,
				"stream":    "stdout",
				"chunk":     base64.StdEncoding.EncodeToString([]byte("before-")),
			}}); err != nil {
				serverErrors <- err
				return
			}
			_ = conn.Close(websocket.StatusInternalError, "disconnect for recovery")
			return
		}
		read, err := readRequest()
		if err != nil {
			serverErrors <- err
			return
		}
		if read["method"] != execserver.MethodProcessRead {
			serverErrors <- fmt.Errorf("recovery request method = %#v", read["method"])
			return
		}
		readParams, _ := read["params"].(map[string]any)
		if readParams["afterSeq"] != float64(1) {
			serverErrors <- fmt.Errorf("recovery afterSeq = %#v", readParams["afterSeq"])
			return
		}
		if err := writeJSON(map[string]any{"id": read["id"], "result": map[string]any{
			"chunks": []map[string]any{{
				"seq":    2,
				"stream": "stdout",
				"chunk":  base64.StdEncoding.EncodeToString([]byte("after")),
			}},
			"nextSeq":       5,
			"exited":        true,
			"exitCode":      0,
			"closed":        true,
			"sandboxDenied": false,
		}}); err != nil {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}))
	defer server.Close()

	manager := NewUnifiedExecManagerWithOptions(1, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	result, err := manager.Exec(context.Background(), &ShellRequest{
		Command:              []string{"ignored"},
		CWD:                  t.TempDir(),
		YieldTimeMS:          5_000,
		UnifiedExecRemoteURL: "ws" + strings.TrimPrefix(server.URL, "http"),
	}, "recover-call")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !result.HasExitCode || result.ExitCode != 0 || result.ProcessID != nil || result.Stdout != "before-after" {
		t.Fatalf("recovered result = %#v", result)
	}
	select {
	case err := <-serverErrors:
		if err != nil {
			t.Fatalf("recovery server error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery server did not finish")
	}
	connectionMu.Lock()
	connections := connectionCount
	connectionMu.Unlock()
	if connections != 2 {
		t.Fatalf("connection count = %d, want 2", connections)
	}
}

func TestUnifiedExecSandboxContextUsesPortablePathURIsLikeRust(t *testing.T) {
	cwd := t.TempDir()
	writable := filepath.Join(cwd, "write root")
	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.WritableRoots = []string{writable}
	context, err := unifiedExecSandboxContext(&ShellRequest{
		CWD:               cwd,
		PermissionProfile: &profile,
	})
	if err != nil {
		t.Fatalf("unifiedExecSandboxContext() error = %v", err)
	}
	if !strings.HasPrefix(context.CWD, "file:") || context.WindowsSandboxLevel != "disabled" {
		t.Fatalf("context = %#v", context)
	}
	var profileWire map[string]any
	if err := json.Unmarshal(context.Permissions, &profileWire); err != nil {
		t.Fatalf("Unmarshal(permissions) error = %v", err)
	}
	fileSystem, _ := profileWire["file_system"].(map[string]any)
	entries, _ := fileSystem["entries"].([]any)
	found := false
	for _, value := range entries {
		entry, _ := value.(map[string]any)
		pathObject, _ := entry["path"].(map[string]any)
		path, _ := pathObject["path"].(string)
		if pathObject["type"] == "path" && strings.HasPrefix(path, "file:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("permissions = %s, want portable path URI", context.Permissions)
	}
}

type lockedUnifiedExecBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

type fakeUnifiedExecSandboxProcess struct {
	output *io.PipeWriter
	closed bool
}

func (p *fakeUnifiedExecSandboxProcess) Wait() (int, error) {
	_, _ = io.WriteString(p.output, "sandboxed-output")
	_ = p.output.Close()
	return 9, nil
}

func (p *fakeUnifiedExecSandboxProcess) Terminate() error { return nil }

func (p *fakeUnifiedExecSandboxProcess) Close() error {
	p.closed = true
	return nil
}

func TestUnifiedExecManagerUsesPersistentWindowsSandboxSessionLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows sandbox session selection is Windows-only")
	}
	previous := startUnifiedExecWindowsSandbox
	defer func() { startUnifiedExecWindowsSandbox = previous }()
	reader, writer := io.Pipe()
	fake := &fakeUnifiedExecSandboxProcess{output: writer}
	startUnifiedExecWindowsSandbox = func(req *ShellRequest) (*startedUnifiedExecSandboxCommand, error) {
		if req == nil || req.PermissionProfile == nil || req.PermissionProfile.Disabled {
			t.Fatalf("sandbox request = %#v", req)
		}
		return &startedUnifiedExecSandboxCommand{process: fake, readers: []io.ReadCloser{reader}}, nil
	}
	manager := NewUnifiedExecManager()
	defer manager.Close()
	profile := sandbox.WorkspaceWritePermissionProfile()
	result, err := manager.Exec(context.Background(), &ShellRequest{
		Command:           []string{"powershell", "-Command", "exit 9"},
		HookCommand:       "exit 9",
		CWD:               t.TempDir(),
		PermissionProfile: &profile,
		YieldTimeMS:       2_000,
	}, "sandbox-call")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !result.HasExitCode || result.ExitCode != 9 || result.ProcessID != nil || result.Stdout != "sandboxed-output" || !fake.closed {
		t.Fatalf("result = %#v, fake closed = %t", result, fake.closed)
	}
}

func (b *lockedUnifiedExecBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *lockedUnifiedExecBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func waitUnifiedExecServerURL(t *testing.T, output *lockedUnifiedExecBuffer) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if value := strings.TrimSpace(output.String()); strings.HasPrefix(value, "ws://") {
			return value
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("exec-server URL not reported: %q", output.String())
	return ""
}

func TestSplitUnifiedExecValidUTF8PrefixMatchesRustChunkBoundary(t *testing.T) {
	input := append([]byte(strings.Repeat("a", unifiedExecOutputDeltaMaxBytes-1)), []byte("界")...)
	prefix, rest, ok := splitUnifiedExecValidUTF8Prefix(input, unifiedExecOutputDeltaMaxBytes)
	if !ok || len(prefix) != unifiedExecOutputDeltaMaxBytes-1 || string(rest) != "界" {
		t.Fatalf("split = %d bytes, rest %q, ok=%v", len(prefix), rest, ok)
	}
	if !utf8.Valid(prefix) || !utf8.Valid(rest) {
		t.Fatalf("split produced invalid UTF-8: prefix=%q rest=%q", prefix, rest)
	}
}

func TestUnifiedExecYieldClampsMatchRust(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(1, 20_000)
	if got := manager.clampWriteYield(0, true); got != unifiedExecMinEmptyPollYieldMS {
		t.Fatalf("empty poll yield = %d", got)
	}
	if got := manager.clampWriteYield(99_000, true); got != 20_000 {
		t.Fatalf("bounded empty poll yield = %d", got)
	}
	if got := manager.clampWriteYield(1, false); got != unifiedExecMinYieldMS {
		t.Fatalf("non-empty min yield = %d", got)
	}
	if got := manager.clampWriteYield(99_000, false); got != unifiedExecMaxYieldMS {
		t.Fatalf("non-empty max yield = %d", got)
	}
	initialFloor := unifiedExecMinYieldMS
	if runtime.GOOS == "windows" {
		initialFloor = unifiedExecWindowsInitialYieldMS
	}
	if got := clampUnifiedExecInitialYield(1); got != initialFloor {
		t.Fatalf("initial yield = %d, want %d", got, initialFloor)
	}
}

func TestUnifiedExecHeadTailBufferCapsAndPreservesEdgesLikeRust(t *testing.T) {
	buffer := newUnifiedExecHeadTailBuffer(10)
	_, _ = buffer.Write([]byte("hello"))
	_, _ = buffer.Write([]byte(" middle "))
	_, _ = buffer.Write([]byte("world"))
	got, omitted := buffer.Drain()
	if string(got) != "helloworld" || omitted == 0 {
		t.Fatalf("Drain() = %q omitted=%d", got, omitted)
	}
	second, secondOmitted := buffer.Drain()
	if len(second) != 0 || secondOmitted != 0 {
		t.Fatalf("second Drain() = %q omitted=%d", second, secondOmitted)
	}
}

func TestUnifiedExecProcessAllocationReservesParallelSlot(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(1, unifiedExecMinEmptyPollYieldMS)
	start := make(chan struct{})
	results := make(chan error, 16)
	for range 16 {
		go func() {
			<-start
			_, err := manager.allocateProcessID()
			results <- err
		}()
	}
	close(start)
	successes := 0
	for range 16 {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrUnifiedExecProcessLimit) {
			t.Fatalf("allocateProcessID() error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful allocations = %d, want 1", successes)
	}
}

func TestUnifiedExecPruningPolicyMatchesRustFixtures(t *testing.T) {
	now := time.Now()
	meta := func(exitedAt map[int]bool) []unifiedExecProcessMeta {
		ages := []time.Duration{40, 30, 20, 19, 18, 17, 16, 15, 14, 13}
		out := make([]unifiedExecProcessMeta, 0, len(ages))
		for index, age := range ages {
			id := index + 1
			out = append(out, unifiedExecProcessMeta{ID: id, LastUsed: now.Add(-age * time.Second), Exited: exitedAt[id]})
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		exited map[int]bool
		want   int
	}{
		{name: "prefers exited outside recent", exited: map[int]bool{2: true}, want: 2},
		{name: "falls back to lru", exited: map[int]bool{}, want: 1},
		{name: "protects recent exited", exited: map[int]bool{3: true, 10: true}, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := unifiedExecProcessIDToPrune(meta(tc.exited))
			if !ok || got != tc.want {
				t.Fatalf("candidate = %d, %v, want %d", got, ok, tc.want)
			}
		})
	}
	if _, ok := unifiedExecProcessIDToPrune(meta(map[int]bool{})[:8]); ok {
		t.Fatal("eight most recent entries should all be protected")
	}
	interacting := meta(map[int]bool{})
	interacting[0].Interacting = true
	if got, ok := unifiedExecProcessIDToPrune(interacting); !ok || got != 2 {
		t.Fatalf("candidate with interacting lru = %d, %v, want 2", got, ok)
	}
}

func TestUnifiedExecWriteStdinSerializesPerSessionAndRunsAcrossSessions(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(2, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	open := func(callID string) int {
		result, err := manager.Exec(context.Background(), &ShellRequest{
			Command:         unifiedExecHelperCommand("echo"),
			CWD:             t.TempDir(),
			TTY:             true,
			YieldTimeMS:     unifiedExecMinYieldMS,
			TimeoutMS:       15_000,
			MaxOutputTokens: intPtr(100),
		}, callID)
		if err != nil || result.ProcessID == nil {
			t.Fatalf("Exec(%s) = %#v, %v", callID, result, err)
		}
		return *result.ProcessID
	}
	first := open("first")
	second := open("second")

	run := func(sessionIDs []int) time.Duration {
		start := make(chan struct{})
		errs := make(chan error, len(sessionIDs))
		started := time.Now()
		for index, sessionID := range sessionIDs {
			go func(index int, sessionID int) {
				<-start
				_, err := manager.WriteStdin(context.Background(), &WriteStdinArgs{
					SessionID:   sessionID,
					Chars:       fmt.Sprintf("message-%d\n", index),
					YieldTimeMS: unifiedExecMinYieldMS,
				}, nil)
				errs <- err
			}(index, sessionID)
		}
		close(start)
		for range sessionIDs {
			if err := <-errs; err != nil {
				t.Fatalf("WriteStdin() error = %v", err)
			}
		}
		return time.Since(started)
	}

	parallel := run([]int{first, second})
	serialized := run([]int{first, first})
	if serialized < parallel+200*time.Millisecond {
		t.Fatalf("same-session duration = %v, cross-session duration = %v; want serialized interaction", serialized, parallel)
	}
}

func TestWriteStdinPostToolUseKeepsParallelSessionMetadataSeparateLikeRust(t *testing.T) {
	executor := NewWriteStdinExecutor(nil, nil)
	output := func(eventCallID string, hookCommand string, response string) *Output {
		return &Output{Data: map[string]any{
			"exit_code":     0,
			"event_call_id": eventCallID,
			"hook_command":  hookCommand,
			"hook_response": response,
		}}
	}

	postB, okB := executor.PostToolUsePayload(&Invocation{CallID: "write-call-b"}, output(
		"exec-call-b", "sleep 1; echo beta", "beta\n",
	))
	postA, okA := executor.PostToolUsePayload(&Invocation{CallID: "write-call-a"}, output(
		"exec-call-a", "sleep 2; echo alpha", "alpha\n",
	))
	if !okA || !okB {
		t.Fatalf("PostToolUsePayload() ok = %v, %v", okA, okB)
	}
	if postB.ToolUseID != "exec-call-b" || postB.ToolInput.(map[string]any)["command"] != "sleep 1; echo beta" || postB.ToolResponse != "beta\n" {
		t.Fatalf("post B = %#v", postB)
	}
	if postA.ToolUseID != "exec-call-a" || postA.ToolInput.(map[string]any)["command"] != "sleep 2; echo alpha" || postA.ToolResponse != "alpha\n" {
		t.Fatalf("post A = %#v", postA)
	}
}

func TestUnifiedExecAllocationDoesNotPruneInteractingProcess(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(10, unifiedExecMinEmptyPollYieldMS)
	now := time.Now()
	for id := 1; id <= 10; id++ {
		manager.processes[id] = &unifiedExecProcess{
			id:       id,
			lastUsed: now.Add(time.Duration(id) * time.Second),
		}
	}
	manager.nextID = 11
	manager.processes[1].interactions.Add(1)

	id, err := manager.allocateProcessID()
	if err != nil || id != 11 {
		t.Fatalf("allocateProcessID() = %d, %v", id, err)
	}
	if manager.processes[1] == nil {
		t.Fatal("interacting process was pruned")
	}
	if manager.processes[2] != nil {
		t.Fatal("least-recent non-interacting process was not pruned")
	}
}

func TestUnifiedExecBackgroundProcessListAndTerminateAreThreadScopedLikeRust(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(2, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	open := func(threadID string, callID string) int {
		result, err := manager.Exec(context.Background(), &ShellRequest{
			Command:             unifiedExecHelperCommand("long"),
			HookCommand:         "long helper " + threadID,
			CWD:                 t.TempDir(),
			YieldTimeMS:         unifiedExecMinYieldMS,
			TimeoutMS:           15_000,
			UnifiedExecThreadID: threadID,
			UnifiedExecTurnID:   "turn-" + threadID,
		}, callID)
		if err != nil || result.ProcessID == nil {
			t.Fatalf("Exec(%s) = %#v, %v", threadID, result, err)
		}
		return *result.ProcessID
	}
	processA := open("thread-a", "call-a")
	processB := open("thread-b", "call-b")

	listedA := manager.ListProcesses("thread-a")
	if len(listedA) != 1 || listedA[0].ProcessID != processA || listedA[0].ItemID != "call-a" || listedA[0].TurnID != "turn-thread-a" {
		t.Fatalf("ListProcesses(thread-a) = %#v", listedA)
	}
	if manager.TerminateProcess("thread-a", processB) {
		t.Fatal("cross-thread terminate unexpectedly succeeded")
	}
	if !manager.TerminateProcess("thread-a", processA) {
		t.Fatal("TerminateProcess(thread-a) = false")
	}
	if got := manager.ListProcesses("thread-a"); len(got) != 0 {
		t.Fatalf("ListProcesses(thread-a) after terminate = %#v", got)
	}
	manager.TerminateAll("thread-b")
	if got := manager.ListProcesses("thread-b"); len(got) != 0 {
		t.Fatalf("ListProcesses(thread-b) after clean = %#v", got)
	}
}

func TestUnifiedExecNonTTYCtrlCMatchesRustPlatformBehavior(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(1, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	opened, err := manager.Exec(context.Background(), &ShellRequest{
		Command:     unifiedExecHelperCommand("long"),
		CWD:         t.TempDir(),
		YieldTimeMS: unifiedExecMinYieldMS,
		TimeoutMS:   15_000,
	}, "interrupt-call")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if opened.ProcessID == nil {
		t.Fatalf("opened = %#v, want running process", opened)
	}
	result, err := manager.WriteStdin(context.Background(), &WriteStdinArgs{
		SessionID:   *opened.ProcessID,
		Chars:       unifiedExecInterrupt,
		YieldTimeMS: 2_000,
	}, nil)
	if runtime.GOOS == "windows" {
		if err == nil || !strings.Contains(err.Error(), "not supported on windows") {
			t.Fatalf("WriteStdin(Ctrl-C) = %#v, %v", result, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("WriteStdin(Ctrl-C) error = %v", err)
	}
	if result.ProcessID != nil || !result.HasExitCode || result.ExitCode != 130 {
		t.Fatalf("interrupt result = %#v, want exit 130", result)
	}
}

func unifiedExecHelperCommand(mode string) []string {
	return []string{os.Args[0], "-test.run=^TestUnifiedExecHelperProcess$", "--", "--unified-exec-helper", mode}
}
