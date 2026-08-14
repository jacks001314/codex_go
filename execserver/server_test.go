package execserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/utils"

	"github.com/coder/websocket"
)

const stdioInitialization = `{"id":0,"method":"initialize","params":{"clientName":"stdio-test"}}` + "\n" +
	`{"method":"initialized","params":{}}` + "\n"

func TestPrepareExecutorNetworkProxyValidatesCallbackParamsLikeRust(t *testing.T) {
	server := NewServer()
	zero := uint64(0)
	for _, test := range []struct {
		name   string
		params *ExecParams
		want   string
	}{
		{name: "zero-timeout", params: &ExecParams{ProcessID: "p", NetworkProxy: &RemoteNetworkProxyLaunchConfig{PolicyDecisionTimeoutMS: &zero}}, want: "network policy decision callback timeout must be nonzero"},
		{name: "empty-process", params: &ExecParams{NetworkProxy: &RemoteNetworkProxyLaunchConfig{PolicyDecisionTimeoutMS: uint64PtrForExecServerTest(1)}}, want: "callback-enabled process ID must be non-empty"},
		{name: "long-process", params: &ExecParams{ProcessID: strings.Repeat("p", MaxNetworkPolicyProcessIDBytes+1), NetworkProxy: &RemoteNetworkProxyLaunchConfig{PolicyDecisionTimeoutMS: uint64PtrForExecServerTest(1)}}, want: "callback-enabled process ID must be non-empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := server.prepareExecutorNetworkProxy(context.Background(), test.params)
			var failure *requestFailure
			if !errors.As(err, &failure) || failure.code != -32602 || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestPrepareExecutorNetworkProxyStartsExecutorLocalProxyAndClosesLikeRust(t *testing.T) {
	server := NewServer()
	environmentID := "remote-test"
	params := &ExecParams{
		ProcessID: "proxy-process",
		EnvPolicy: &ExecEnvPolicy{Inherit: "none", Set: map[string]string{"BASE": "kept"}},
		Env:       map[string]string{"HTTP_PROXY": "http://controller.invalid:9999"},
		Sandbox:   json.RawMessage(`{}`),
		NetworkProxy: &RemoteNetworkProxyLaunchConfig{
			Proxy:         RemoteNetworkProxyConfig{Enabled: true, EnableSOCKS5: false, Mode: string(network.ProxyModeFull)},
			EnvironmentID: &environmentID,
		},
	}
	preparedParams, prepared, policyCancel, err := server.prepareExecutorNetworkProxy(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if prepared == nil || policyCancel == nil {
		t.Fatalf("prepared proxy = %#v cancel=%v", prepared, policyCancel)
	}
	defer policyCancel()
	proxyURL := preparedParams.Env["HTTP_PROXY"]
	if proxyURL == "" || proxyURL == "http://controller.invalid:9999" || preparedParams.Env["BASE"] != "kept" || preparedParams.EnvPolicy != nil {
		t.Fatalf("prepared env = %#v", preparedParams.Env)
	}
	if preparedParams.ManagedNetwork == nil || len(preparedParams.ManagedNetwork.LoopbackPorts) != 1 || !preparedParams.EnforceManagedNetwork {
		t.Fatalf("prepared managed network = %#v", preparedParams)
	}
	address := strings.TrimPrefix(proxyURL, "http://")
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("executor-local proxy is not listening at %s: %v", address, err)
	}
	_ = conn.Close()
	if err := prepared.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, err = net.DialTimeout("tcp", address, 20*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("executor-local proxy still accepts connections after Close at %s", address)
}

func TestPrepareExecutorNetworkProxyWindowsNativeLaunchStripsControllerProxyLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native launches have the shared-ingress SID restriction")
	}
	server := NewServer()
	preparedParams, prepared, policyCancel, err := server.prepareExecutorNetworkProxy(context.Background(), &ExecParams{
		ProcessID: "native-process",
		EnvPolicy: &ExecEnvPolicy{Inherit: "none"},
		Env: map[string]string{
			"HTTP_PROXY":                         "http://controller.invalid:9999",
			"http_proxy":                         "http://controller.invalid:9999",
			"NO_PROXY":                           "localhost",
			network.ProxyActiveEnvKey:            "1",
			network.ProxyAllowLocalBindingEnvKey: "0",
			"KEEP":                               "yes",
		},
		NetworkProxy: &RemoteNetworkProxyLaunchConfig{Proxy: RemoteNetworkProxyConfig{Enabled: true, Mode: string(network.ProxyModeFull)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared != nil || policyCancel != nil || preparedParams.NetworkProxy != nil || preparedParams.ManagedNetwork != nil || preparedParams.EnforceManagedNetwork {
		t.Fatalf("native Windows prepared state = %#v proxy=%#v", preparedParams, prepared)
	}
	if preparedParams.Env["KEEP"] != "yes" || preparedParams.Env["HTTP_PROXY"] != "" || preparedParams.Env["http_proxy"] != "" || preparedParams.Env["NO_PROXY"] != "" || preparedParams.Env[network.ProxyActiveEnvKey] != "" {
		t.Fatalf("native Windows env = %#v", preparedParams.Env)
	}
}

func uint64PtrForExecServerTest(value uint64) *uint64 { return &value }

func TestExecServerFSHelperProcess(t *testing.T) {
	found := false
	for _, arg := range os.Args {
		if arg == FSHelperArg1 {
			found = true
			break
		}
	}
	if !found {
		return
	}
	os.Exit(RunFSHelper(os.Stdin, os.Stdout, os.Stderr))
}

func TestStdioInitializeAndEnvironmentInfo(t *testing.T) {
	input := `{"id":1,"method":"initialize","params":{"clientName":"test"}}` + "\n" +
		`{"method":"initialized","params":{}}` + "\n" +
		`{"id":2,"method":"environment/info","params":{}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"sessionId"`) || !strings.Contains(output, `"shell"`) {
		t.Fatalf("stdout = %q", output)
	}
	if !strings.Contains(output, `"environmentConfigRead":true`) {
		t.Fatalf("local environment info should advertise environmentConfigRead: %q", output)
	}
}

func TestLocalEnvironmentInfoReportsTemporaryDirectoriesAndCapability(t *testing.T) {
	info := localEnvironmentInfo()
	if info == nil {
		t.Fatal("localEnvironmentInfo() = nil")
	}
	if !info.Capabilities.EnvironmentConfigRead {
		t.Fatalf("EnvironmentConfigRead = false, want true (Rust 646f7c0a91)")
	}
	if !info.Capabilities.SandboxedFileStreaming {
		t.Fatalf("SandboxedFileStreaming = false, want true (Rust #38356)")
	}
	if len(info.TemporaryDirectories) == 0 {
		t.Fatalf("TemporaryDirectories empty, want TEMP/TMP (Rust 92fb33b758)")
	}
	seen := map[string]bool{}
	for _, dir := range info.TemporaryDirectories {
		if seen[dir] {
			t.Fatalf("duplicate temporary directory %q", dir)
		}
		seen[dir] = true
		if !strings.HasPrefix(dir, "file://") {
			t.Fatalf("temporary directory %q is not a file URI", dir)
		}
	}
}

func TestEnvironmentCapabilitiesSandboxedFileStreamingWire(t *testing.T) {
	raw := []byte(`{"shell":{"name":"bash","path":"/bin/bash"},"capabilities":{"sandboxedFileStreaming":true}}`)
	var info EnvironmentInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("Unmarshal EnvironmentInfo error = %v", err)
	}
	if !info.Capabilities.SandboxedFileStreaming {
		t.Fatalf("SandboxedFileStreaming = false, want true from wire")
	}
	legacy := []byte(`{"shell":{"name":"bash","path":"/bin/bash"},"capabilities":{}}`)
	var legacyInfo EnvironmentInfo
	if err := json.Unmarshal(legacy, &legacyInfo); err != nil {
		t.Fatalf("Unmarshal legacy EnvironmentInfo error = %v", err)
	}
	if legacyInfo.Capabilities.SandboxedFileStreaming {
		t.Fatal("legacy executor should default SandboxedFileStreaming to false")
	}
}

func TestLocalTemporaryDirectoriesDedupesAndSkipsRelativeOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows TEMP/TMP resolution only")
	}
	t.Setenv("TEMP", "C:\\Temp")
	t.Setenv("TMP", "C:\\Temp")
	t.Setenv("TMPDIR", "relative-temp")
	got := localTemporaryDirectories("C:\\work")
	if len(got) != 1 || got[0] != "file:///C:/Temp" {
		t.Fatalf("TemporaryDirectories = %#v, want [file:///C:/Temp]", got)
	}
}

func TestStdioInitializeAndEnvironmentStatus(t *testing.T) {
	input := `{"id":1,"method":"initialize","params":{"clientName":"test"}}` + "\n" +
		`{"method":"initialized","params":{}}` + "\n" +
		`{"id":2,"method":"environment/status","params":{}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"sessionId"`) || !strings.Contains(output, `"status":"ready"`) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestStdioMalformedJSONReportsRustErrorAndKeepsRunning(t *testing.T) {
	input := "not-json\n" + `{"id":1,"method":"initialize","params":{"clientName":"test"}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(lines[0], `"id":-1`) || !strings.Contains(lines[0], `"code":-32600`) || !strings.Contains(lines[0], "failed to parse JSON-RPC message from exec-server stdio") {
		t.Fatalf("malformed response = %s", lines[0])
	}
	if !strings.Contains(lines[1], `"id":1`) || !strings.Contains(lines[1], `"sessionId"`) {
		t.Fatalf("initialize response = %s", lines[1])
	}
}

func TestRequestIDAcceptsOnlyRustStringOrIntegerVariants(t *testing.T) {
	for _, invalid := range []string{
		`{"id":null,"method":"initialize","params":{"clientName":"test"}}`,
		`{"id":1.5,"method":"initialize","params":{"clientName":"test"}}`,
		`{"id":true,"method":"initialize","params":{"clientName":"test"}}`,
	} {
		out, ok := NewServer().handleLine(context.Background(), []byte(invalid))
		if !ok {
			t.Fatalf("handleLine(%s) produced no response", invalid)
		}
		response, responseOK := out.(response)
		if !responseOK || response.ID.value != -1 || response.Error == nil || response.Error.Code != -32600 {
			t.Fatalf("handleLine(%s) = %#v", invalid, out)
		}
	}
	for _, valid := range []string{
		`{"id":1,"method":"initialize","params":{"clientName":"test"}}`,
		`{"id":"request-1","method":"initialize","params":{"clientName":"test"}}`,
	} {
		out, ok := NewServer().handleLine(context.Background(), []byte(valid))
		if !ok {
			t.Fatalf("handleLine(%s) produced no response", valid)
		}
		response, responseOK := out.(response)
		if !responseOK || response.Error != nil {
			t.Fatalf("handleLine(%s) = %#v", valid, out)
		}
	}
}

func TestConnectionInitializationSequenceAndResumeValidationMatchRust(t *testing.T) {
	server := NewServer()
	input := `{"id":1,"method":"environment/info","params":{}}` + "\n" +
		`{"id":2,"method":"initialize","params":{"clientName":"test"}}` + "\n" +
		`{"id":3,"method":"environment/info","params":{}}` + "\n" +
		`{"method":"initialized","params":{}}` + "\n" +
		`{"id":4,"method":"environment/info","params":{}}` + "\n" +
		`{"id":5,"method":"environment/status","params":{}}` + "\n" +
		`{"id":6,"method":"initialize","params":{"clientName":"test"}}` + "\n"
	var stdout bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	for _, message := range []string{
		"client must call initialize before using environment info methods",
		"client must send initialized before using environment info methods",
		"initialize may only be sent once per connection",
		`"id":4,"result":{"shell"`,
		`"id":5,"result":{"status":"ready"}`,
	} {
		if !strings.Contains(output, message) {
			t.Fatalf("stdout = %q, missing %q", output, message)
		}
	}

	resumeInput := `{"id":1,"method":"initialize","params":{"clientName":"test","resumeSessionId":"missing-session"}}` + "\n"
	stdout.Reset()
	if err := server.Serve(context.Background(), strings.NewReader(resumeInput), &stdout); err != nil {
		t.Fatalf("Serve(resume) error = %v", err)
	}
	if !strings.Contains(stdout.String(), "unknown session id missing-session") {
		t.Fatalf("resume stdout = %q", stdout.String())
	}
}

func TestConnectionClosesOnUnexpectedClientMessagesLikeRust(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "response", input: `{"id":1,"result":null}` + "\n"},
		{name: "error", input: `{"id":1,"error":{"code":-32600,"message":"client error"}}` + "\n"},
		{name: "unknown notification", input: `{"method":"unexpected/notification","params":{}}` + "\n"},
		{name: "initialized before initialize", input: `{"method":"initialized","params":{}}` + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			input := test.input + `{"id":2,"method":"initialize","params":{"clientName":"must-not-run"}}` + "\n"
			if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
				t.Fatalf("Serve() error = %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q", stdout.String())
			}
		})
	}
}

func TestConnectionStreamDetachPreservesSessionRegistryLikeRust(t *testing.T) {
	server := NewServer()
	server.detachedSessionTTL = time.Minute
	defer server.shutdownSessions()

	client, peer := net.Pipe()
	done := make(chan error, 1)
	go func() {
		defer peer.Close()
		done <- server.serveConnectionStream(context.Background(), peer, peer)
	}()

	if _, err := io.WriteString(client, `{"id":1,"method":"initialize","params":{"clientName":"relay-stream-test"}}`+"\n"); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	line, err := bufio.NewReader(client).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	var initialized response
	if err := json.Unmarshal(line, &initialized); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if initialized.Error != nil {
		t.Fatalf("initialize response error = %#v", initialized.Error)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveConnectionStream() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveConnectionStream did not stop after disconnect")
	}

	server.registryMu.Lock()
	sessionCount := len(server.sessions)
	server.registryMu.Unlock()
	if sessionCount != 1 {
		t.Fatalf("detached relay stream left %d sessions, want 1", sessionCount)
	}
}

func TestSessionRegistryMatchesRustAttachResumeAndExpiry(t *testing.T) {
	server := NewServer()
	server.detachedSessionTTL = 40 * time.Millisecond
	newConnection := func() context.Context {
		return withConnectionProtocolState(withHTTPBodyStreamRegistry(context.WithValue(context.Background(), processNotifierContextKey{}, processNotifier(func(string, any) {}))))
	}
	initialize := func(ctx context.Context, resume *string) (string, error) {
		result, err := server.handleRequest(ctx, &request{
			Method: MethodInitialize,
			Params: mustMarshalRawMessage(t, InitializeParams{ClientName: "session-registry-test", ResumeSessionID: resume}),
		})
		if err != nil {
			return "", err
		}
		response, ok := result.(InitializeResponse)
		if !ok {
			t.Fatalf("initialize response = %#v", result)
		}
		return response.SessionID, nil
	}

	first := newConnection()
	firstSessionID, err := initialize(first, nil)
	if err != nil {
		t.Fatalf("first initialize error = %v", err)
	}
	second := newConnection()
	secondSessionID, err := initialize(second, nil)
	if err != nil {
		t.Fatalf("second initialize error = %v", err)
	}
	if firstSessionID == secondSessionID {
		t.Fatalf("new sessions reused id %q", firstSessionID)
	}

	activeResume := newConnection()
	_, err = initialize(activeResume, &firstSessionID)
	var failure *requestFailure
	if !errors.As(err, &failure) || failure.code != -32010 || failure.message != "session "+firstSessionID+" is already attached to another connection" {
		t.Fatalf("active resume error = %#v", err)
	}

	server.detachConnection(first)
	resumedSessionID, err := initialize(activeResume, &firstSessionID)
	if err != nil {
		t.Fatalf("detached resume error = %v", err)
	}
	if resumedSessionID != firstSessionID {
		t.Fatalf("resumed session id = %q, want %q", resumedSessionID, firstSessionID)
	}

	server.detachConnection(activeResume)
	time.Sleep(80 * time.Millisecond)
	expiredResume := newConnection()
	_, err = initialize(expiredResume, &firstSessionID)
	if !errors.As(err, &failure) || failure.code != -32600 || failure.message != "unknown session id "+firstSessionID {
		t.Fatalf("expired resume error = %#v", err)
	}

	server.detachConnection(second)
}

func TestSessionRegistryIsolatesFileHandlesLikeRust(t *testing.T) {
	server := NewServer()
	newInitializedConnection := func() context.Context {
		ctx := withConnectionProtocolState(withHTTPBodyStreamRegistry(context.WithValue(context.Background(), processNotifierContextKey{}, processNotifier(func(string, any) {}))))
		if _, err := server.handleRequest(ctx, &request{
			Method: MethodInitialize,
			Params: mustMarshalRawMessage(t, InitializeParams{ClientName: "session-isolation-test"}),
		}); err != nil {
			t.Fatalf("initialize error = %v", err)
		}
		state := protocolStateFromContext(ctx)
		state.mu.Lock()
		state.initialized = true
		state.mu.Unlock()
		return ctx
	}

	firstPath := filepath.Join(t.TempDir(), "first.txt")
	secondPath := filepath.Join(t.TempDir(), "second.txt")
	if err := os.WriteFile(firstPath, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := newInitializedConnection()
	second := newInitializedConnection()
	for _, item := range []struct {
		ctx  context.Context
		path string
	}{{first, firstPath}, {second, secondPath}} {
		if _, err := server.handleRequest(item.ctx, &request{
			Method: MethodFSOpen,
			Params: mustMarshalRawMessage(t, FSOpenParams{HandleID: "shared-handle", Path: pathToURI(item.path)}),
		}); err != nil {
			t.Fatalf("open %s error = %v", item.path, err)
		}
	}
	read := func(ctx context.Context) string {
		result, err := server.handleRequest(ctx, &request{
			Method: MethodFSReadBlock,
			Params: mustMarshalRawMessage(t, FSReadBlockParams{HandleID: "shared-handle", Len: 16}),
		})
		if err != nil {
			t.Fatalf("read block error = %v", err)
		}
		response := result.(*FSReadBlockResponse)
		data, err := base64.StdEncoding.DecodeString(response.Chunk)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := read(first); got != "first" {
		t.Fatalf("first session read = %q", got)
	}
	if got := read(second); got != "second" {
		t.Fatalf("second session read = %q", got)
	}
	for _, ctx := range []context.Context{first, second} {
		if _, err := server.handleRequest(ctx, &request{
			Method: MethodFSClose,
			Params: mustMarshalRawMessage(t, FSCloseParams{HandleID: "shared-handle"}),
		}); err != nil {
			t.Fatalf("close handle error = %v", err)
		}
	}
	server.detachConnection(first)
	server.detachConnection(second)
}

func TestLongPollReadFailsAfterSessionDetachLikeRust(t *testing.T) {
	server := NewServer()
	server.detachedSessionTTL = 50 * time.Millisecond
	connection := withConnectionProtocolState(withHTTPBodyStreamRegistry(context.WithValue(context.Background(), processNotifierContextKey{}, processNotifier(func(string, any) {}))))
	if _, err := server.handleRequest(connection, &request{
		Method: MethodInitialize,
		Params: mustMarshalRawMessage(t, InitializeParams{ClientName: "long-poll-resume-test"}),
	}); err != nil {
		t.Fatalf("initialize error = %v", err)
	}
	protocol := protocolStateFromContext(connection)
	protocol.mu.Lock()
	protocol.initialized = true
	sessionServer := protocol.session.server
	protocol.mu.Unlock()

	state, err := sessionServer.reserveProcessState(connection, &ExecParams{ProcessID: "quiet"})
	if err != nil {
		t.Fatalf("reserveProcessState() error = %v", err)
	}
	state.mu.Lock()
	state.starting = false
	state.openStreams = 1
	state.mu.Unlock()

	readDone := make(chan error, 1)
	go func() {
		waitMS := uint64(5_000)
		_, readErr := server.handleRequest(connection, &request{
			Method: MethodProcessRead,
			Params: mustMarshalRawMessage(t, ReadParams{ProcessID: "quiet", WaitMS: &waitMS}),
		})
		readDone <- readErr
	}()

	time.Sleep(25 * time.Millisecond)
	started := time.Now()
	server.detachConnection(connection)
	select {
	case readErr := <-readDone:
		var failure *requestFailure
		if !errors.As(readErr, &failure) || failure.code != -32600 || failure.message != "session has been resumed by another connection" {
			t.Fatalf("long-poll detach error = %#v", readErr)
		}
		if elapsed := time.Since(started); elapsed >= time.Second {
			t.Fatalf("long-poll detach took %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("long-poll read remained blocked after session detach")
	}
}

func TestProcessStartingStateMatchesRustReadWriteAndTerminate(t *testing.T) {
	server := NewServer()
	params := &ExecParams{ProcessID: "starting", PipeStdin: true}
	state, err := server.reserveProcessState(context.Background(), params)
	if err != nil {
		t.Fatalf("reserveProcessState() error = %v", err)
	}
	write, err := server.writeProcess(&WriteParams{ProcessID: "starting", Chunk: "eA==", WriteID: "write-1"})
	if err != nil || write.Status != "starting" {
		t.Fatalf("write while starting = %#v, err = %v", write, err)
	}
	if _, err := server.readProcess(&ReadParams{ProcessID: "starting"}); err == nil || err.Error() != "process id starting is starting" {
		t.Fatalf("read while starting error = %v", err)
	}
	terminated, err := server.terminateProcess(&TerminateParams{ProcessID: "starting"})
	if err != nil || !terminated.Running {
		t.Fatalf("terminate while starting = %#v, err = %v", terminated, err)
	}
	if server.activateProcessState("starting", state, exec.Command("unused"), nil, false, false, 2) {
		t.Fatal("cancelled process start was activated")
	}
	terminated, err = server.terminateProcess(&TerminateParams{ProcessID: "starting"})
	if err != nil || terminated.Running {
		t.Fatalf("second terminate = %#v, err = %v", terminated, err)
	}
}

func mustMarshalRawMessage(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStdioTransportPushesOrderedProcessNotificationsLikeRust(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	defer inputWriter.Close()
	defer outputReader.Close()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- NewServer().Serve(context.Background(), inputReader, outputWriter)
	}()
	command := []string{"sh", "-c", "printf stdio-pushed"}
	if runtime.GOOS == "windows" {
		command = []string{"powershell", "-NoProfile", "-Command", "[Console]::Out.Write('stdio-pushed')"}
	}
	startParams, err := json.Marshal(ExecParams{
		ProcessID: "stdio-pushed",
		Argv:      command,
		CWD:       mustCurrentPathURI(t),
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	writeDone := make(chan error, 1)
	go func() {
		for _, line := range []string{
			`{"id":1,"method":"initialize","params":{"clientName":"stdio-push-test"}}`,
			`{"method":"initialized","params":{}}`,
			fmt.Sprintf(`{"id":2,"method":"process/start","params":%s}`, startParams),
		} {
			if _, err := fmt.Fprintln(inputWriter, line); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- nil
	}()
	scanner := bufio.NewScanner(outputReader)
	lastSeq := uint64(0)
	var output strings.Builder
	sawExit := false
	deadline := time.After(3 * time.Second)
	for {
		lineCh := make(chan string, 1)
		go func() {
			if scanner.Scan() {
				lineCh <- scanner.Text()
			}
		}()
		select {
		case line := <-lineCh:
			select {
			case writeErr := <-writeDone:
				if writeErr != nil {
					t.Fatalf("write request error = %v", writeErr)
				}
				writeDone = nil
			default:
			}
			var message map[string]json.RawMessage
			if err := json.Unmarshal([]byte(line), &message); err != nil {
				t.Fatalf("notification JSON error = %v: %s", err, line)
			}
			var method string
			_ = json.Unmarshal(message["method"], &method)
			if method == "" {
				continue
			}
			var params struct {
				Seq      uint64 `json:"seq"`
				Chunk    string `json:"chunk"`
				ExitCode int    `json:"exitCode"`
			}
			if err := json.Unmarshal(message["params"], &params); err != nil {
				t.Fatalf("notification params error = %v", err)
			}
			if params.Seq != lastSeq+1 {
				t.Fatalf("notification seq = %d after %d", params.Seq, lastSeq)
			}
			lastSeq = params.Seq
			switch method {
			case MethodProcessOutput:
				data, _ := base64.StdEncoding.DecodeString(params.Chunk)
				output.Write(data)
			case MethodProcessExited:
				sawExit = params.ExitCode == 0
			case MethodProcessClosed:
				if output.String() != "stdio-pushed" || !sawExit {
					t.Fatalf("stdio pushed output=%q sawExit=%v", output.String(), sawExit)
				}
				_ = inputWriter.Close()
				select {
				case err := <-serveDone:
					if err != nil && !errors.Is(err, io.ErrClosedPipe) {
						t.Fatalf("Serve() error = %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("Serve() did not stop")
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for stdio process notifications")
		}
	}
}

func TestStdioProcessStartReadAndWriteClosed(t *testing.T) {
	cwd := mustCurrentPathURI(t)
	input := `{"id":1,"method":"initialize","params":{"clientName":"test"}}` + "\n" +
		`{"method":"initialized","params":{}}` + "\n" +
		`{"id":2,"method":"process/start","params":{"processId":"proc-1","argv":["go","version"],"cwd":` + quote(cwd) + `,"env":{},"tty":false}}` + "\n" +
		`{"id":3,"method":"process/write","params":{"processId":"proc-1","chunk":"` + base64.StdEncoding.EncodeToString([]byte("ignored\n")) + `","writeId":"w1"}}` + "\n" +
		`{"id":4,"method":"process/read","params":{"processId":"proc-1","afterSeq":0}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"processId":"proc-1"`) || !strings.Contains(output, `"status":"stdinClosed"`) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestProcessStartWireCWDRequiresRustPathURI(t *testing.T) {
	server := NewServer()
	for _, cwd := range []string{"", ".", filepath.Join("relative", "dir")} {
		_, err := server.handleRequest(context.Background(), &request{
			Method: MethodProcessStart,
			Params: mustMarshalRawMessage(t, ExecParams{
				ProcessID: "invalid-cwd",
				Argv:      []string{"go", "version"},
				CWD:       cwd,
				Env:       map[string]string{},
			}),
		})
		var failure *requestFailure
		if !errors.As(err, &failure) || failure.code != -32602 || !strings.Contains(failure.message, "cwd must be an absolute file URI") {
			t.Fatalf("cwd %q error = %#v", cwd, err)
		}
	}
}

func TestProcessStartFailureRemovesReservationAndClosedIDsRemainReservedLikeRust(t *testing.T) {
	previousRetention := execServerExitedProcessRetention
	execServerExitedProcessRetention = 75 * time.Millisecond
	defer func() { execServerExitedProcessRetention = previousRetention }()
	server := NewServer()
	processID := "reserved"
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: processID,
		Argv:      []string{"codex-go-command-that-does-not-exist"},
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	}); err == nil {
		t.Fatal("startProcess() error = nil, want spawn failure")
	}
	command := []string{"sh", "-c", "exit 0"}
	if runtime.GOOS == "windows" {
		command = []string{"powershell", "-NoProfile", "-Command", "exit 0"}
	}
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: processID,
		Argv:      command,
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	}); err != nil {
		t.Fatalf("retry startProcess() error = %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state := server.lookup(processID)
		state.mu.Lock()
		closed := state.closed
		state.mu.Unlock()
		if closed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: processID,
		Argv:      command,
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	}); err == nil || !strings.Contains(err.Error(), "process reserved already exists") {
		t.Fatalf("duplicate closed start error = %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && server.lookup(processID) != nil {
		time.Sleep(10 * time.Millisecond)
	}
	if server.lookup(processID) != nil {
		t.Fatal("closed process was not removed after retention period")
	}
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: processID,
		Argv:      command,
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	}); err != nil {
		t.Fatalf("start after retention error = %v", err)
	}
}

func TestProcessStartRejectsNonCanonicalEnvironmentInheritLikeRust(t *testing.T) {
	server := NewServer()
	for _, inherit := range []string{"", "ALL", " all "} {
		_, err := server.startProcess(context.Background(), &ExecParams{
			ProcessID: "env-" + inherit,
			Argv:      []string{"go", "version"},
			EnvPolicy: &ExecEnvPolicy{Inherit: inherit},
			Env:       map[string]string{},
		})
		if err == nil {
			t.Fatalf("startProcess(inherit=%q) error = nil", inherit)
		}
	}
}

func TestProcessWriteAcceptsWhitespaceWriteIDLikeRust(t *testing.T) {
	server := NewServer()
	command := []string{"sh", "-c", "read line"}
	if runtime.GOOS == "windows" {
		command = []string{"powershell", "-NoProfile", "-Command", "[Console]::In.ReadLine() | Out-Null"}
	}
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: "whitespace-write-id",
		Argv:      command,
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
		PipeStdin: true,
	}); err != nil {
		t.Fatalf("startProcess() error = %v", err)
	}
	response, err := server.writeProcess(&WriteParams{
		ProcessID: "whitespace-write-id",
		Chunk:     base64.StdEncoding.EncodeToString([]byte("done\n")),
		WriteID:   " ",
	})
	if err != nil || response.Status != "accepted" {
		t.Fatalf("writeProcess() = %#v, %v", response, err)
	}
	_ = server.lookup("whitespace-write-id").terminate()
}

func TestProcessWriteAndSignalValidateParamsBeforeUnknownProcessLikeRust(t *testing.T) {
	server := NewServer()
	if _, err := server.writeProcess(&WriteParams{ProcessID: "missing", Chunk: "%%%", WriteID: "write-1"}); err == nil {
		t.Fatal("writeProcess(invalid base64) error = nil")
	}
	if _, err := server.writeProcess(&WriteParams{ProcessID: "missing", Chunk: "eA==", WriteID: ""}); err == nil {
		t.Fatal("writeProcess(empty write id) error = nil")
	}
	if _, err := server.signalProcess(&SignalParams{ProcessID: "missing", Signal: "kill"}); err == nil {
		t.Fatal("signalProcess(invalid signal) error = nil")
	}
	response, err := server.writeProcess(&WriteParams{ProcessID: "missing", Chunk: "eA==", WriteID: "write-1"})
	if err != nil || response.Status != "unknownProcess" {
		t.Fatalf("writeProcess(unknown) = %#v, %v", response, err)
	}
}

type execServerTestWriteCloser struct {
	bytes.Buffer
}

func (w *execServerTestWriteCloser) Close() error { return nil }

func TestProcessWriteIDCacheIsBoundedLikeRust(t *testing.T) {
	stdin := &execServerTestWriteCloser{}
	state := &processState{
		stdin:        stdin,
		pipeStdin:    true,
		seenWriteIDs: map[string]bool{},
	}
	for index := 0; index <= retainedWriteIDsPerProcess; index++ {
		writeID := fmt.Sprintf("write-%d", index)
		response, err := state.write(&WriteParams{Chunk: "eA==", WriteID: writeID})
		if err != nil || response.Status != "accepted" {
			t.Fatalf("write %d = %#v, %v", index, response, err)
		}
	}
	if len(state.seenWriteIDs) != retainedWriteIDsPerProcess || state.seenWriteIDs["write-0"] || !state.seenWriteIDs[fmt.Sprintf("write-%d", retainedWriteIDsPerProcess)] {
		t.Fatalf("retained write ids = %d, oldest=%v newest=%v", len(state.seenWriteIDs), state.seenWriteIDs["write-0"], state.seenWriteIDs[fmt.Sprintf("write-%d", retainedWriteIDsPerProcess)])
	}
}

func TestProcessReadMaxBytesIncludesFirstOversizedChunkAndNormalExitHasNoFailureLikeRust(t *testing.T) {
	state := &processState{
		chunks:  []outputChunk{{Seq: 1, Stream: "stdout", Chunk: base64.StdEncoding.EncodeToString([]byte("oversized"))}},
		nextSeq: 2,
	}
	zero := 0
	response := state.readLocked(0, &zero)
	if len(response.Chunks) != 1 || response.NextSeq != 2 {
		t.Fatalf("readLocked(maxBytes=0) = %#v", response)
	}

	server := NewServer()
	command := []string{"sh", "-c", "exit 7"}
	if runtime.GOOS == "windows" {
		command = []string{"powershell", "-NoProfile", "-Command", "exit 7"}
	}
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: "normal-nonzero-exit",
		Argv:      command,
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	}); err != nil {
		t.Fatalf("startProcess() error = %v", err)
	}
	waitMS := uint64(3_000)
	response, err := server.readProcess(&ReadParams{ProcessID: "normal-nonzero-exit", WaitMS: &waitMS})
	if err != nil || !response.Exited || response.ExitCode == nil || *response.ExitCode != 7 || response.Failure != nil {
		t.Fatalf("readProcess() = %#v, %v", response, err)
	}
}

func TestProcessStateTransportsLocallyClassifiedSandboxDenialLikeRust(t *testing.T) {
	state := &processState{
		id:          "sandbox-denial",
		nextSeq:     1,
		sandboxType: sandbox.SandboxTypeLinuxSeccomp,
	}
	state.cond = sync.NewCond(&state.mu)
	state.mu.Lock()
	state.appendLocked("stderr", []byte("bash: /workspace/locked: Permission denied"))
	state.mu.Unlock()
	exitCode := 1
	state.finishWithCode(nil, &exitCode)

	state.mu.Lock()
	response := state.readLocked(0, nil)
	state.mu.Unlock()
	if !response.Exited || !response.SandboxDenied || response.ExitCode == nil || *response.ExitCode != 1 {
		t.Fatalf("read response = %#v", response)
	}
}

func TestProcessSandboxTypeProtocolMappingMatchesRust(t *testing.T) {
	tests := []struct {
		internal sandbox.SandboxType
		wire     ProcessSandboxType
	}{
		{sandbox.SandboxTypeNone, ProcessSandboxNone},
		{sandbox.SandboxTypeMacosSeatbelt, ProcessSandboxMacosSeatbelt},
		{sandbox.SandboxTypeLinuxSeccomp, ProcessSandboxLinuxSeccomp},
		{sandbox.SandboxTypeWindowsRestrictedToken, ProcessSandboxWindowsRestrictedToken},
	}
	for _, test := range tests {
		if got := processSandboxTypeToProtocol(test.internal); got != test.wire {
			t.Fatalf("protocol type for %q = %q", test.internal, got)
		}
		wire := test.wire
		if got := SandboxTypeFromProtocol(&wire); got == nil || *got != test.internal {
			t.Fatalf("internal type for %q = %#v", test.wire, got)
		}
	}
}

func TestStdioJSONRPCProtocolErrors(t *testing.T) {
	input := stdioInitialization + `{"id":1,"method":"unknown/method","params":{}}` + "\n" +
		`{"id":2,"method":"process/read","params":{"processId":123}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"code":-32601`) || !strings.Contains(output, "exec-server stub does not implement `unknown/method` yet") {
		t.Fatalf("missing unknown method error: %q", output)
	}
	if !strings.Contains(output, `"code":-32602`) || !strings.Contains(output, `"invalid params:`) {
		t.Fatalf("missing invalid params error: %q", output)
	}
}

func TestStdioNotificationWithNestedIDDoesNotRequireRequestID(t *testing.T) {
	input := `{"method":"initialized","params":{"id":"nested-only"}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no response for notification", stdout.String())
	}
}

func TestStdioProcessStartRejectsInvalidOrUnsupportedSandboxAndMissingManagedNetworkContext(t *testing.T) {
	cwd := mustCurrentPathURI(t)
	input := stdioInitialization + `{"id":1,"method":"process/start","params":{"processId":"sandboxed","argv":["go","version"],"cwd":` + quote(cwd) + `,"env":{},"sandbox":{"mode":"readOnly"}}}` + "\n" +
		`{"id":2,"method":"process/start","params":{"processId":"networked","argv":["go","version"],"cwd":` + quote(cwd) + `,"env":{},"sandbox":{"permissions":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"}]},"network":"restricted"},"windowsSandboxLevel":"disabled"},"enforceManagedNetwork":true}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "managed network enforcement requires managedNetwork context") {
		t.Fatalf("stdout = %q", output)
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(output, `"code":-32602`) || !strings.Contains(output, "invalid sandbox context: permissions are required") {
			t.Fatalf("stdout = %q", output)
		}
	} else if !strings.Contains(output, `"code":-32602`) || !strings.Contains(output, "invalid sandbox context") {
		t.Fatalf("stdout = %q", output)
	}
}

func TestPrepareExecProcessNativeLaunchIgnoresManagedNetworkWithoutSandboxLikeRust(t *testing.T) {
	command, _, _, err := prepareExecProcess(&ExecParams{
		Argv:                  []string{"go", "version"},
		EnforceManagedNetwork: true,
	})
	if err != nil || len(command) != 2 {
		t.Fatalf("prepareExecProcess() = %#v, %v", command, err)
	}
}

func TestNativePermissionProfileJSONConvertsPathURIs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "write root")
	uri, err := utils.FromHostNativePath(path)
	if err != nil {
		t.Fatalf("FromHostNativePath() error = %v", err)
	}
	raw := json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"path","path":` + quote(uri.String()) + `},"access":"write"}]},"network":"restricted"}`)
	converted, err := nativePermissionProfileJSON(raw)
	if err != nil {
		t.Fatalf("nativePermissionProfileJSON() error = %v", err)
	}
	if !strings.Contains(converted, quote(path)) {
		t.Fatalf("converted = %s, want native path %q", converted, path)
	}
}

func TestPrepareExecProcessWithoutSandboxAcceptsPathURI(t *testing.T) {
	dir := t.TempDir()
	uri, err := utils.FromHostNativePath(dir)
	if err != nil {
		t.Fatalf("FromHostNativePath() error = %v", err)
	}
	command, cwd, _, err := prepareExecProcess(&ExecParams{Argv: []string{"go", "version"}, CWD: uri.String()})
	if err != nil {
		t.Fatalf("prepareExecProcess() error = %v", err)
	}
	if len(command) != 2 || command[0] != "go" || cwd != dir {
		t.Fatalf("command = %#v, cwd = %q", command, cwd)
	}
}

func TestStdioFileSystemMethods(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "hello.txt")
	dirURI := pathToURI(filepath.Dir(path))
	pathURI := pathToURI(path)
	encoded := base64.StdEncoding.EncodeToString([]byte("hello"))
	input := stdioInitialization + `{"id":1,"method":"fs/createDirectory","params":{"path":` + quote(dirURI) + `,"recursive":true}}` + "\n" +
		`{"id":2,"method":"fs/writeFile","params":{"path":` + quote(pathURI) + `,"dataBase64":` + quote(encoded) + `}}` + "\n" +
		`{"id":3,"method":"fs/readFile","params":{"path":` + quote(pathURI) + `}}` + "\n" +
		`{"id":4,"method":"fs/getMetadata","params":{"path":` + quote(pathURI) + `}}` + "\n" +
		`{"id":5,"method":"fs/readDirectory","params":{"path":` + quote(dirURI) + `}}` + "\n" +
		`{"id":6,"method":"fs/canonicalize","params":{"path":` + quote(pathURI) + `}}` + "\n" +
		`{"id":7,"method":"fs/open","params":{"handleId":"h1","path":` + quote(pathURI) + `}}` + "\n" +
		`{"id":8,"method":"fs/readBlock","params":{"handleId":"h1","offset":1,"len":3}}` + "\n" +
		`{"id":9,"method":"fs/close","params":{"handleId":"h1"}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, encoded) || !strings.Contains(output, `"isFile":true`) || !strings.Contains(output, `"fileName":"hello.txt"`) || !strings.Contains(output, base64.StdEncoding.EncodeToString([]byte("ell"))) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestFilesystemWirePathsRequirePathURIsLikeRust(t *testing.T) {
	nativePath := filepath.Join(t.TempDir(), "value.txt")
	tests := []struct {
		method string
		params any
	}{
		{MethodFSReadFile, FSReadFileParams{Path: nativePath}},
		{MethodFSOpen, FSOpenParams{HandleID: "h", Path: nativePath}},
		{MethodFSWriteFile, FSWriteFileParams{Path: nativePath}},
		{MethodFSCreateDirectory, FSCreateDirectoryParams{Path: nativePath}},
		{MethodFSGetMetadata, FSGetMetadataParams{Path: nativePath}},
		{MethodFSCanonicalize, FSCanonicalizeParams{Path: nativePath}},
		{MethodFSReadDirectory, FSReadDirectoryParams{Path: nativePath}},
		{MethodFSWalk, FSWalkParams{Path: nativePath}},
		{MethodFSRemove, FSRemoveParams{Path: nativePath}},
		{MethodFSCopy, FSCopyParams{SourcePath: nativePath, DestinationPath: pathToURI(nativePath)}},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			_, err := NewServer().handleRequest(context.Background(), &request{
				Method: test.method,
				Params: mustMarshalRawMessage(t, test.params),
			})
			var failure *requestFailure
			if !errors.As(err, &failure) || failure.code != -32602 || !strings.Contains(failure.message, "absolute file URI") {
				t.Fatalf("handleRequest() error = %#v", err)
			}
		})
	}
}

func TestFilesystemWireSandboxPathsRequirePathURIsLikeRust(t *testing.T) {
	path := pathToURI(filepath.Join(t.TempDir(), "value.txt"))
	native := t.TempDir()
	for name, sandboxContext := range map[string]*FileSystemSandboxContext{
		"cwd":            {CWD: native},
		"workspace root": {WorkspaceRoots: []string{native}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewServer().handleRequest(context.Background(), &request{
				Method: MethodFSReadFile,
				Params: mustMarshalRawMessage(t, FSReadFileParams{Path: path, Sandbox: sandboxContext}),
			})
			var failure *requestFailure
			if !errors.As(err, &failure) || failure.code != -32602 || !strings.Contains(failure.message, "absolute file URI") {
				t.Fatalf("handleRequest() error = %#v", err)
			}
		})
	}
}

func TestResolvePathRejectsForeignPathURILikeRust(t *testing.T) {
	foreign := "file:///usr/local/file.txt"
	if runtime.GOOS != "windows" {
		foreign = "file:///C:/Users/Alice/file.txt"
	}
	if _, err := resolvePath(foreign); err == nil {
		t.Fatalf("resolvePath(%q) error = nil", foreign)
	}
}

func TestFilesystemHandleAndReadLimitsMatchRust(t *testing.T) {
	server := NewServer()
	path := filepath.Join(t.TempDir(), "value.txt")
	if err := os.WriteFile(path, []byte("abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	opened, err := server.openFile(&FSOpenParams{HandleID: "", Path: path})
	if err != nil || opened.HandleID != "" {
		t.Fatalf("openFile(empty handle) = %#v, %v", opened, err)
	}
	if _, err := server.openFile(&FSOpenParams{HandleID: "", Path: path}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate open error = %v", err)
	}
	for _, length := range []int{0, fileReadChunkSize + 1} {
		if _, err := server.readBlock(&FSReadBlockParams{HandleID: "", Len: length}); err == nil || !strings.Contains(err.Error(), "between 1") {
			t.Fatalf("readBlock(len=%d) error = %v", length, err)
		}
	}
	response, err := server.readBlock(&FSReadBlockParams{HandleID: "", Offset: 1, Len: 3})
	if err != nil || response.Chunk != base64.StdEncoding.EncodeToString([]byte("bcd")) || response.EOF {
		t.Fatalf("readBlock() = %#v, %v", response, err)
	}
	if _, err := server.closeFile(&FSCloseParams{HandleID: strings.Repeat("x", maxFileReadHandleIDBytes+1)}); err == nil {
		t.Fatal("closeFile(long handle) error = nil")
	}
	_, _ = server.closeFile(&FSCloseParams{HandleID: ""})
}

func TestFilesystemSandboxContextRunsThroughHelperLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	oldCommand := fsHelperCommandForExecutable
	fsHelperCommandForExecutable = func(string) []string {
		return []string{os.Args[0], "-test.run=^TestExecServerFSHelperProcess$", "--", FSHelperArg1}
	}
	defer func() { fsHelperCommandForExecutable = oldCommand }()
	params := &FSReadFileParams{
		Path: path,
		Sandbox: &FileSystemSandboxContext{
			Permissions:         json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[]},"network":"restricted"}`),
			CWD:                 filepath.Dir(path),
			WindowsSandboxLevel: "disabled",
		},
	}
	response, err := readFile(params)
	if err != nil {
		t.Fatalf("readFile(sandbox) error = %v", err)
	}
	data, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil || string(data) != "secret" {
		t.Fatalf("readFile(sandbox) data = %q, err = %v", data, err)
	}
}

func TestReadFileRejectsFilesOverRustLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := file.Truncate(maxReadFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("Truncate() error = %v", err)
	}
	_ = file.Close()
	if _, err := readFile(&FSReadFileParams{Path: path}); err == nil || !strings.Contains(err.Error(), "file is too large") {
		t.Fatalf("readFile(large) error = %v", err)
	}
}

func TestStdioFileSystemWalk(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0o600); err != nil {
		t.Fatalf("WriteFile(root) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "note.txt"), []byte("nested"), 0o600); err != nil {
		t.Fatalf("WriteFile(nested) error = %v", err)
	}

	dirURI := pathToURI(dir)
	input := stdioInitialization + `{"id":1,"method":"fs/walk","params":{"path":` + quote(dirURI) + `,"options":{"maxDepth":4,"maxDirectories":10,"maxEntries":10,"followDirectorySymlinks":false}}}` + "\n" +
		`{"id":2,"method":"fs/walk","params":{"path":` + quote(dirURI) + `,"options":{"maxDepth":4,"maxDirectories":1,"maxEntries":10,"followDirectorySymlinks":false}}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"kind":"directory"`) || !strings.Contains(output, `"kind":"file"`) || !strings.Contains(output, `"truncated":true`) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestWalkPathEmptyDirectoryUsesEmptyArrays(t *testing.T) {
	response, err := walkPath(&FSWalkParams{Path: t.TempDir(), Options: FSWalkOptions{MaxDepth: 4, MaxDirectories: 10, MaxEntries: 10}})
	if err != nil {
		t.Fatalf("walkPath() error = %v", err)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"entries":[]`) || !strings.Contains(string(data), `"errors":[]`) {
		t.Fatalf("walk response = %s", data)
	}
}

func TestWalkPathUsesRustBreadthFirstOrderAndRequiresBounds(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	for path, contents := range map[string]string{
		filepath.Join(dir, "root.txt"):      "root",
		filepath.Join(nested, "nested.txt"): "nested",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	response, err := walkPath(&FSWalkParams{Path: dir, Options: FSWalkOptions{MaxDepth: 4, MaxDirectories: 10, MaxEntries: 10}})
	if err != nil {
		t.Fatalf("walkPath() error = %v", err)
	}
	wantPaths := []string{
		pathToURI(nested),
		pathToURI(filepath.Join(dir, "root.txt")),
		pathToURI(filepath.Join(nested, "nested.txt")),
	}
	if len(response.Entries) != len(wantPaths) {
		t.Fatalf("walk entries = %#v", response.Entries)
	}
	for i, want := range wantPaths {
		if response.Entries[i].Path != want {
			t.Fatalf("walk entry %d = %#v, want path %q", i, response.Entries[i], want)
		}
	}
	if _, err := walkPath(&FSWalkParams{Path: dir}); err == nil || err.Error() != "filesystem walk limits must be greater than zero" {
		t.Fatalf("unbounded walk error = %v", err)
	}
}

func TestCanonicalizeMissingPathFailsLikeRust(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := canonicalize(&FSCanonicalizeParams{Path: missing}); err == nil {
		t.Fatal("canonicalize(missing) error = nil")
	}
}

func TestStdioProcessReadWaitsForOutput(t *testing.T) {
	shell, args := waitOutputCommand()
	cwd := mustCurrentPathURI(t)
	input := stdioInitialization + `{"id":1,"method":"process/start","params":{"processId":"proc-wait","argv":[` + quote(shell)
	for _, arg := range args {
		input += `,` + quote(arg)
	}
	input += `],"cwd":` + quote(cwd) + `,"env":{},"tty":false,"pipeStdin":false}}` + "\n" +
		`{"id":2,"method":"process/read","params":{"processId":"proc-wait","afterSeq":0,"waitMs":1500}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	var read *ReadResponse
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var envelope struct {
			ID     int           `json:"id"`
			Result *ReadResponse `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if envelope.ID == 2 {
			read = envelope.Result
			break
		}
	}
	if read == nil {
		t.Fatalf("read response missing in %q", stdout.String())
	}
	if output := decodedChunks(read.Chunks, "stdout"); !strings.Contains(output, "delayed") {
		t.Fatalf("stdout chunks = %q response = %+v raw = %q", output, read, stdout.String())
	}
}

func TestStartProcessHonorsArg0(t *testing.T) {
	server := NewServer()
	arg0 := "codex-custom-arg0"
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: "proc-arg0",
		Argv:      []string{os.Args[0], "-test.run=TestExecServerArg0Helper"},
		CWD:       "",
		EnvPolicy: &ExecEnvPolicy{
			Inherit:               "core",
			IgnoreDefaultExcludes: true,
		},
		Env:  map[string]string{"CODEX_EXEC_SERVER_ARG0_HELPER": "1"},
		Arg0: &arg0,
	}); err != nil {
		t.Fatalf("startProcess() error = %v", err)
	}
	after := uint64(0)
	wait := uint64(5000)
	read, err := server.readProcess(&ReadParams{ProcessID: "proc-arg0", AfterSeq: &after, WaitMS: &wait})
	if err != nil {
		t.Fatalf("readProcess() error = %v", err)
	}
	output := decodedChunks(read.Chunks, "stdout")
	if output != arg0 {
		t.Fatalf("stdout = %q, want arg0 %q", output, arg0)
	}
}

func TestExecServerArg0Helper(t *testing.T) {
	if os.Getenv("CODEX_EXEC_SERVER_ARG0_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString(os.Args[0])
	os.Exit(0)
}

func TestChildEnvPolicy(t *testing.T) {
	params := &ExecParams{
		EnvPolicy: &ExecEnvPolicy{
			Inherit:               "none",
			IgnoreDefaultExcludes: true,
			Set:                   map[string]string{"FROM_POLICY": "yes"},
		},
		Env: map[string]string{"FROM_ENV": "yes"},
	}
	env := childEnv(params)
	wantLen := 2
	if runtime.GOOS == "windows" {
		wantLen = 3
		if env["PATHEXT"] != ".COM;.EXE;.BAT;.CMD" {
			t.Fatalf("childEnv() = %#v", env)
		}
	}
	if env["FROM_POLICY"] != "yes" || env["FROM_ENV"] != "yes" || len(env) != wantLen {
		t.Fatalf("childEnv() = %#v", env)
	}
}

func TestChildEnvRemovesParentLifetimeControl(t *testing.T) {
	for _, params := range []*ExecParams{
		{Env: map[string]string{CodexExecServerExitOnStdinCloseEnvVar: "true", "KEEP": "yes"}},
		{
			EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
			Env:       map[string]string{CodexExecServerExitOnStdinCloseEnvVar: "true", "KEEP": "yes"},
		},
	} {
		env := childEnv(params)
		if hasEnvKey(env, CodexExecServerExitOnStdinCloseEnvVar) || env["KEEP"] != "yes" {
			t.Fatalf("childEnv() = %#v", env)
		}
	}
}

func TestCopyPathPreservesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on Windows")
	}
	root := t.TempDir()
	source := filepath.Join(root, "src")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "target.txt"), []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(source, "target-link")); err != nil {
		t.Fatalf("Symlink(target-link) error = %v", err)
	}

	destination := filepath.Join(root, "dst")
	if _, err := copyPath(&FSCopyParams{SourcePath: source, DestinationPath: destination, Recursive: true}); err != nil {
		t.Fatalf("copyPath(directory) error = %v", err)
	}
	copiedLink := filepath.Join(destination, "target-link")
	info, err := os.Lstat(copiedLink)
	if err != nil {
		t.Fatalf("Lstat(copiedLink) error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("copied link mode = %v, want symlink", info.Mode())
	}
	target, err := os.Readlink(copiedLink)
	if err != nil {
		t.Fatalf("Readlink(copiedLink) error = %v", err)
	}
	if target != "target.txt" {
		t.Fatalf("copied link target = %q, want target.txt", target)
	}

	singleLink := filepath.Join(root, "single-link")
	if _, err := copyPath(&FSCopyParams{SourcePath: filepath.Join(source, "target-link"), DestinationPath: singleLink}); err != nil {
		t.Fatalf("copyPath(symlink) error = %v", err)
	}
	info, err = os.Lstat(singleLink)
	if err != nil {
		t.Fatalf("Lstat(singleLink) error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("single copied link mode = %v, want symlink", info.Mode())
	}
}

func TestGetMetadataIncludesCreatedAtOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("createdAtMs is only available through this backend on Windows")
	}
	path := filepath.Join(t.TempDir(), "created.txt")
	if err := os.WriteFile(path, []byte("created"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	metadata, err := getMetadata(&FSGetMetadataParams{Path: path})
	if err != nil {
		t.Fatalf("getMetadata() error = %v", err)
	}
	if metadata.CreatedAtMS == 0 {
		t.Fatalf("createdAtMs = 0, want Windows creation timestamp")
	}
}

func TestRemovePathDefaultsRecursiveAndForce(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "dir")
	if err := os.MkdirAll(filepath.Join(directory, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "nested", "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := removePath(&FSRemoveParams{Path: directory}); err != nil {
		t.Fatalf("removePath(default recursive) error = %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("Stat(directory) error = %v, want not exist", err)
	}
	if _, err := removePath(&FSRemoveParams{Path: filepath.Join(root, "missing")}); err != nil {
		t.Fatalf("removePath(default force missing) error = %v", err)
	}
}

func TestWriteFileInvalidBase64Message(t *testing.T) {
	_, err := writeFile(&FSWriteFileParams{Path: filepath.Join(t.TempDir(), "bad.txt"), DataBase64: "not base64"})
	if err == nil || !strings.Contains(err.Error(), "fs/writeFile requires valid base64 dataBase64") {
		t.Fatalf("writeFile() error = %v", err)
	}
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func mustCurrentPathURI(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	return pathToURI(cwd)
}

func decodedChunks(chunks []outputChunk, stream string) string {
	var out strings.Builder
	for _, chunk := range chunks {
		if chunk.Stream != stream {
			continue
		}
		data, _ := base64.StdEncoding.DecodeString(chunk.Chunk)
		out.Write(data)
	}
	return out.String()
}

func waitOutputCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-Command", "Start-Sleep -Milliseconds 100; Write-Output delayed"}
	}
	return "sh", []string{"-c", "sleep 0.1; printf 'delayed\n'"}
}

func TestStdioHTTPRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("x-codex-test") != "yes" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.Header.Get("x-codex-test"))
		}
		w.Header().Set("x-codex-response", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("response-body"))
	}))
	defer server.Close()

	body := base64.StdEncoding.EncodeToString([]byte("request-body"))
	input := stdioInitialization + `{"id":1,"method":"http/request","params":{"method":"POST","url":` + quote(server.URL) + `,"headers":[{"name":"x-codex-test","value":"yes"}],"bodyBase64":` + quote(body) + `,"requestId":"http-1"}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	var got struct {
		ID     int                 `json:"id"`
		Result HTTPRequestResponse `json:"result"`
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var candidate struct {
			ID     int                 `json:"id"`
			Result HTTPRequestResponse `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &candidate); err == nil && candidate.ID == 1 {
			got = candidate
		}
	}
	if got.Result.Status == 0 {
		t.Fatalf("stdout = %q, http response missing", output)
	}
	hasHeader := false
	for _, header := range got.Result.Headers {
		if strings.EqualFold(header.Name, "x-codex-response") && header.Value == "ok" {
			hasHeader = true
			break
		}
	}
	if got.Result.Status != http.StatusCreated || got.Result.BodyBase64 != base64.StdEncoding.EncodeToString([]byte("response-body")) || !hasHeader {
		t.Fatalf("stdout = %q", output)
	}
}

func TestHTTPRequestRejectsInvalidHeadersLikeRust(t *testing.T) {
	for name, header := range map[string]HTTPHeader{
		"name":  {Name: "Bad Header", Value: "bad-name"},
		"value": {Name: "X-Bad-Value", Value: "bad\nvalue"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := doHTTPRequest(context.Background(), &HTTPRequestParams{
				Method:  "GET",
				URL:     "http://127.0.0.1/",
				Headers: []HTTPHeader{header},
			})
			if err == nil || !strings.Contains(err.Error(), "header") {
				t.Fatalf("doHTTPRequest() error = %v", err)
			}
		})
	}
}

func TestHTTPRequestValidatesMethodURLRedirectAndZeroTimeoutLikeRust(t *testing.T) {
	for name, params := range map[string]HTTPRequestParams{
		"empty-method":     {Method: "", URL: "http://127.0.0.1/"},
		"unsupported-url":  {Method: "GET", URL: "file:///tmp/value"},
		"missing-url-host": {Method: "GET", URL: "http:/value"},
		"redirect-case":    {Method: "GET", URL: "http://127.0.0.1/", RedirectPolicy: "STOP"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := doHTTPRequest(context.Background(), &params); err == nil {
				t.Fatal("doHTTPRequest() error = nil")
			}
		})
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	zero := uint64(0)
	if _, err := doHTTPRequest(context.Background(), &HTTPRequestParams{Method: "GET", URL: upstream.URL, TimeoutMS: &zero}); err == nil {
		t.Fatal("doHTTPRequest(timeoutMs=0) error = nil")
	}
}

func TestWebSocketHTTPRequestStreamsBodyAfterResponseLikeRust(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("alpha"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("beta"))
	}))
	defer upstream.Close()

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}
	conn, _, err := websocket.Dial(context.Background(), serverURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	write := func(value any) {
		data, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatalf("Marshal() error = %v", marshalErr)
		}
		if writeErr := conn.Write(context.Background(), websocket.MessageText, data); writeErr != nil {
			t.Fatalf("Write() error = %v", writeErr)
		}
	}
	read := func() map[string]json.RawMessage {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, data, readErr := conn.Read(ctx)
		if readErr != nil {
			t.Fatalf("Read() error = %v", readErr)
		}
		var message map[string]json.RawMessage
		if unmarshalErr := json.Unmarshal(data, &message); unmarshalErr != nil {
			t.Fatalf("Unmarshal() error = %v", unmarshalErr)
		}
		return message
	}
	write(map[string]any{"id": 1, "method": MethodInitialize, "params": InitializeParams{ClientName: "http-stream-test"}})
	_ = read()
	write(map[string]any{"method": MethodInitialized, "params": map[string]any{}})
	write(map[string]any{"id": 2, "method": MethodHTTPRequest, "params": HTTPRequestParams{
		Method: "GET", URL: upstream.URL, RequestID: "http-stream", StreamResponse: true,
	}})
	responseMessage := read()
	if len(responseMessage["id"]) == 0 || len(responseMessage["result"]) == 0 || len(responseMessage["method"]) != 0 {
		t.Fatalf("first streamed message = %#v", responseMessage)
	}
	var response HTTPRequestResponse
	if err := json.Unmarshal(responseMessage["result"], &response); err != nil || response.Status != http.StatusOK || response.BodyBase64 != "" {
		t.Fatalf("stream response = %#v, %v", response, err)
	}
	var body strings.Builder
	lastSeq := uint64(0)
	for {
		message := read()
		var method string
		_ = json.Unmarshal(message["method"], &method)
		if method != MethodHTTPRequestBodyDelta {
			t.Fatalf("stream notification method = %q", method)
		}
		var delta HTTPRequestBodyDeltaNotification
		if err := json.Unmarshal(message["params"], &delta); err != nil {
			t.Fatalf("delta unmarshal error = %v", err)
		}
		if delta.RequestID != "http-stream" || delta.Seq != lastSeq+1 {
			t.Fatalf("delta = %#v after seq %d", delta, lastSeq)
		}
		lastSeq = delta.Seq
		data, _ := base64.StdEncoding.DecodeString(delta.DeltaBase64)
		body.Write(data)
		if delta.Done {
			if delta.Error != nil || body.String() != "alphabeta" {
				t.Fatalf("stream terminal delta = %#v body=%q", delta, body.String())
			}
			break
		}
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

func TestHTTPBodyStreamRequestIDsAreConnectionScopedAndReusableAfterTerminalDeltaLikeRust(t *testing.T) {
	ctx := withHTTPBodyStreamRegistry(context.Background())
	release, err := reserveHTTPBodyStream(ctx, "same-id")
	if err != nil {
		t.Fatalf("reserveHTTPBodyStream() error = %v", err)
	}
	if _, err := reserveHTTPBodyStream(ctx, "same-id"); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate reserve error = %v", err)
	}
	release()
	releaseAgain, err := reserveHTTPBodyStream(ctx, "same-id")
	if err != nil {
		t.Fatalf("reserve after release error = %v", err)
	}
	releaseAgain()
	otherConnection := withHTTPBodyStreamRegistry(context.Background())
	otherRelease, err := reserveHTTPBodyStream(otherConnection, "same-id")
	if err != nil {
		t.Fatalf("other connection reserve error = %v", err)
	}
	otherRelease()
}

func TestParseListenURLLikeRust(t *testing.T) {
	tests := []struct {
		name      string
		listenURL string
		want      ListenTransport
		wantErr   string
	}{
		{
			name:      "default",
			listenURL: DefaultListenURL,
			want:      ListenTransport{Kind: ListenKindWebSocket, Address: "127.0.0.1:0"},
		},
		{
			name:      "stdio",
			listenURL: "stdio",
			want:      ListenTransport{Kind: ListenKindStdio},
		},
		{
			name:      "stdio url",
			listenURL: "stdio://",
			want:      ListenTransport{Kind: ListenKindStdio},
		},
		{
			name:      "websocket",
			listenURL: "ws://127.0.0.1:1234",
			want:      ListenTransport{Kind: ListenKindWebSocket, Address: "127.0.0.1:1234"},
		},
		{
			name:      "hostname rejected",
			listenURL: "ws://localhost:1234",
			wantErr:   "invalid websocket --listen URL `ws://localhost:1234`; expected `ws://IP:PORT`",
		},
		{
			name:      "unsupported scheme",
			listenURL: "http://127.0.0.1:1234",
			wantErr:   "unsupported --listen URL `http://127.0.0.1:1234`; expected `ws://IP:PORT` or `stdio`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseListenURL(tt.listenURL)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ParseListenURL error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseListenURL returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseListenURL = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestWebSocketTransportServesInitializeAndRejectsOrigin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewServer().ServeTransport(ctx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	listenURL := waitForExecServerListenURL(t, urlCh)

	client := &http.Client{Timeout: time.Second}
	readyURL := "http://" + strings.TrimPrefix(listenURL, "ws://") + "/readyz"
	response, err := client.Get(readyURL)
	if err != nil {
		t.Fatalf("GET /readyz error = %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/readyz status = %d", response.StatusCode)
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), time.Second)
	_, originResponse, err := websocket.Dial(dialCtx, listenURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}},
	})
	dialCancel()
	if err == nil {
		t.Fatal("websocket dial with Origin should fail")
	}
	if originResponse == nil || originResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("origin response = %#v, err = %v", originResponse, err)
	}

	dialCtx, dialCancel = context.WithTimeout(context.Background(), time.Second)
	conn, _, err := websocket.Dial(dialCtx, listenURL, nil)
	dialCancel()
	if err != nil {
		t.Fatalf("websocket dial error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), time.Second)
	defer rpcCancel()
	if err := conn.Write(rpcCtx, websocket.MessageText, []byte("not-json")); err != nil {
		t.Fatalf("write malformed JSON error = %v", err)
	}
	messageType, data, err := conn.Read(rpcCtx)
	if err != nil {
		t.Fatalf("read malformed JSON response error = %v", err)
	}
	if messageType != websocket.MessageText || !bytes.Contains(data, []byte(`"id":-1`)) || !bytes.Contains(data, []byte(`"code":-32600`)) || !bytes.Contains(data, []byte("failed to parse websocket JSON-RPC message from exec-server websocket")) {
		t.Fatalf("malformed JSON response = %s", data)
	}
	if err := conn.Write(rpcCtx, websocket.MessageText, []byte(`{"id":1,"method":"initialize","params":{"clientName":"ws-test"}}`)); err != nil {
		t.Fatalf("write initialize error = %v", err)
	}
	messageType, data, err = conn.Read(rpcCtx)
	if err != nil {
		t.Fatalf("read initialize response error = %v", err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("message type = %v", messageType)
	}
	if !bytes.Contains(data, []byte(`"id":1`)) || !bytes.Contains(data, []byte(`"sessionId"`)) {
		t.Fatalf("initialize response = %s", data)
	}
	if err := conn.Write(rpcCtx, websocket.MessageText, []byte(`{"id":2,"result":null}`)); err != nil {
		t.Fatalf("write unexpected client response error = %v", err)
	}
	if _, _, err := conn.Read(rpcCtx); err == nil {
		t.Fatal("websocket stayed open after unexpected client response")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeTransport returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeTransport did not stop after context cancellation")
	}
}

func waitForExecServerListenURL(t *testing.T, urlCh <-chan string) string {
	t.Helper()
	select {
	case line := <-urlCh:
		if strings.HasPrefix(line, "ws://") {
			return line
		}
		t.Fatalf("exec-server listen URL has unexpected value: %q", line)
	case <-time.After(time.Second):
		t.Fatal("exec-server listen URL was not written")
	}
	return ""
}
