package codemode

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

type uiPluginTestHost struct {
	input  *io.PipeWriter
	output *io.PipeReader
	done   chan error
}

func startUIPluginTestHost(t *testing.T) *uiPluginTestHost {
	t.Helper()
	hostInput, testInput := io.Pipe()
	testOutput, hostOutput := io.Pipe()
	host := &uiPluginTestHost{input: testInput, output: testOutput, done: make(chan error, 1)}
	go func() {
		host.done <- RunUIPluginSandbox(hostInput, hostOutput)
		_ = hostOutput.Close()
	}()
	ready, ok, err := readUIPluginFrame(testOutput)
	if err != nil || !ok {
		t.Fatalf("read READY frame: ok=%v err=%v", ok, err)
	}
	if string(ready) != uiPluginReady {
		t.Fatalf("READY = %q, want %q", ready, uiPluginReady)
	}
	return host
}

func (h *uiPluginTestHost) close(t *testing.T) {
	t.Helper()
	_ = h.input.Close()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("RunUIPluginSandbox() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunUIPluginSandbox() did not exit")
	}
	_ = h.output.Close()
}

func (h *uiPluginTestHost) send(t *testing.T, instance uint64, message any) {
	t.Helper()
	if err := writeUIPluginJSON(h.input, uiPluginOutboundPacket{Instance: instance, Message: message}); err != nil {
		t.Fatalf("send plugin message: %v", err)
	}
}

func (h *uiPluginTestHost) receive(t *testing.T) (uint64, map[string]any) {
	t.Helper()
	type result struct {
		instance uint64
		message  map[string]any
		err      error
	}
	results := make(chan result, 1)
	go func() {
		payload, ok, err := readUIPluginFrame(h.output)
		if err != nil || !ok {
			results <- result{err: fmt.Errorf("plugin host closed: ok=%v err=%v", ok, err)}
			return
		}
		var packet uiPluginOutboundPacket
		if err := json.Unmarshal(payload, &packet); err != nil {
			results <- result{err: err}
			return
		}
		message, _ := packet.Message.(map[string]any)
		results <- result{instance: packet.Instance, message: message}
	}()
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("receive plugin message: %v", result.err)
		}
		return result.instance, result.message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for plugin message")
		return 0, nil
	}
}

func initializeUIPlugin(t *testing.T, host *uiPluginTestHost, instance uint64, source string) {
	t.Helper()
	host.send(t, instance, map[string]any{"type": "initialize", "source": source})
	gotInstance, message := host.receive(t)
	if gotInstance != instance || message["type"] != "initialized" {
		t.Fatalf("initialize = (%d, %v), want initialized for %d", gotInstance, message, instance)
	}
}

func dispatchUIPlugin(t *testing.T, host *uiPluginTestHost, instance uint64, id uint64, export, mode string) (uint64, map[string]any) {
	t.Helper()
	host.send(t, instance, map[string]any{
		"type": "dispatch", "id": id, "export": export, "returnMode": mode,
	})
	return host.receive(t)
}

func TestUIPluginInitializesInvokesAndClosesLikeRust(t *testing.T) {
	host := startUIPluginTestHost(t)
	source := fmt.Sprintf("/*%s*/ export default () => 'x'.repeat(5000);", strings.Repeat("p", 40*1024))
	initializeUIPlugin(t, host, 7, source)
	instance, reply := dispatchUIPlugin(t, host, 7, 0, "default", uiPluginReturnMaybeAsync)
	if instance != 7 || reply["type"] != "reply" || reply["value"] != strings.Repeat("x", 5000) {
		t.Fatalf("reply = (%d, %v)", instance, reply)
	}
	host.send(t, 7, map[string]any{"type": "close"})
	instance, closed := host.receive(t)
	if instance != 7 || closed["type"] != "closed" {
		t.Fatalf("close = (%d, %v)", instance, closed)
	}
	host.close(t)
}

func TestUIPluginPromiseErrorsCloseIsolateInBothReturnModesLikeRust(t *testing.T) {
	host := startUIPluginTestHost(t)
	source := `
		export function run() {
			const promise = Promise.reject(Error('tracked failure'));
			promise.catch(() => {});
			return promise;
		}
	`
	for _, testCase := range []struct {
		instance uint64
		mode     string
		code     string
	}{
		{1, uiPluginReturnSync, uiPluginErrInvalidResult},
		{2, uiPluginReturnMaybeAsync, uiPluginErrExecutionFailed},
	} {
		initializeUIPlugin(t, host, testCase.instance, source)
		instance, failure := dispatchUIPlugin(t, host, testCase.instance, 0, "run", testCase.mode)
		if instance != testCase.instance || failure["type"] != "error" || failure["code"] != testCase.code {
			t.Fatalf("%s dispatch = (%d, %v), want %s", testCase.mode, instance, failure, testCase.code)
		}
		if testCase.mode == uiPluginReturnMaybeAsync {
			scriptError, _ := failure["scriptError"].(map[string]any)
			if scriptError == nil || !strings.Contains(fmt.Sprint(scriptError["message"]), "tracked failure") {
				t.Fatalf("scriptError = %v", failure["scriptError"])
			}
		}
		instance, closed := host.receive(t)
		if instance != testCase.instance || closed["type"] != "closed" {
			t.Fatalf("closed = (%d, %v)", instance, closed)
		}
	}
	host.close(t)
}

func TestUIPluginCancellationClosesPendingWorkLikeRust(t *testing.T) {
	host := startUIPluginTestHost(t)
	initializeUIPlugin(t, host, 1, "export function run() { return new Promise(() => {}); }")
	instance, pending := dispatchUIPlugin(t, host, 1, 0, "run", uiPluginReturnMaybeAsync)
	if instance != 1 || pending["type"] != "pending" || pending["id"] != float64(0) {
		t.Fatalf("pending = (%d, %v)", instance, pending)
	}
	host.send(t, 1, map[string]any{"type": "close"})
	instance, closed := host.receive(t)
	if instance != 1 || closed["type"] != "closed" {
		t.Fatalf("closed = (%d, %v)", instance, closed)
	}
	host.close(t)
}

func TestUIPluginSyncDispatchSettlesPendingInvocationsLikeRust(t *testing.T) {
	host := startUIPluginTestHost(t)
	initializeUIPlugin(t, host, 1, `
		const resolvers = [];
		export function wait() { return new Promise(resolve => resolvers.push(resolve)); }
		export function finish() {
			for (const resolve of resolvers) resolve('done');
			return 'finished';
		}
	`)
	for id := uint64(0); id < uiPluginMaxPromises; id++ {
		instance, pending := dispatchUIPlugin(t, host, 1, id, "wait", uiPluginReturnMaybeAsync)
		if instance != 1 || pending["type"] != "pending" || pending["id"] != float64(id) {
			t.Fatalf("pending %d = (%d, %v)", id, instance, pending)
		}
	}
	instance, reply := dispatchUIPlugin(t, host, 1, uiPluginMaxPromises, "finish", uiPluginReturnSync)
	if instance != 1 || reply["type"] != "reply" || reply["value"] != "finished" {
		t.Fatalf("finish = (%d, %v)", instance, reply)
	}
	resolved := map[uint64]bool{}
	for range uiPluginMaxPromises {
		instance, reply := host.receive(t)
		if instance != 1 || reply["type"] != "reply" || reply["value"] != "done" {
			t.Fatalf("settled reply = (%d, %v)", instance, reply)
		}
		resolved[uint64(reply["id"].(float64))] = true
	}
	if len(resolved) != uiPluginMaxPromises {
		t.Fatalf("settled IDs = %d, want %d", len(resolved), uiPluginMaxPromises)
	}
	host.close(t)
}

func TestUIPluginSyntaxAndExecutionErrorsPreserveBoundedScriptErrorsLikeRust(t *testing.T) {
	host := startUIPluginTestHost(t)
	host.send(t, 1, map[string]any{"type": "initialize", "source": "export default => ;"})
	instance, failure := host.receive(t)
	if instance != 1 || failure["code"] != uiPluginErrInvalidProgram {
		t.Fatalf("syntax error = (%d, %v)", instance, failure)
	}
	scriptError, _ := failure["scriptError"].(map[string]any)
	if scriptError == nil || scriptError["line"] != float64(1) || scriptError["truncated"] != false {
		t.Fatalf("syntax scriptError = %v", failure["scriptError"])
	}
	if _, ok := scriptError["column"].(float64); !ok {
		t.Fatalf("syntax column = %v", scriptError["column"])
	}
	if instance, closed := host.receive(t); instance != 1 || closed["type"] != "closed" {
		t.Fatalf("syntax close = (%d, %v)", instance, closed)
	}

	source := fmt.Sprintf("export function fail() { throw Error('xxx\\u001b[31m' + 'x'.repeat(%d)); }", uiPluginMaxScriptError)
	initializeUIPlugin(t, host, 2, source)
	instance, failure = dispatchUIPlugin(t, host, 2, 0, "fail", uiPluginReturnSync)
	if instance != 2 || failure["type"] != "error" || failure["code"] != uiPluginErrExecutionFailed {
		t.Fatalf("execution error = (%d, %v)", instance, failure)
	}
	scriptError, _ = failure["scriptError"].(map[string]any)
	message, _ := scriptError["message"].(string)
	if !strings.Contains(message, "Error: xxx\x1b[31m") || len(message) > uiPluginMaxScriptError || scriptError["truncated"] != true {
		t.Fatalf("execution scriptError = %v (len %d)", scriptError, len(message))
	}
	if instance, closed := host.receive(t); instance != 2 || closed["type"] != "closed" {
		t.Fatalf("execution close = (%d, %v)", instance, closed)
	}
	host.close(t)
}

func TestUIPluginLoadingRestrictionsCloseIsolateLikeRust(t *testing.T) {
	host := startUIPluginTestHost(t)
	cases := []struct {
		source     string
		code       string
		diagnostic string
	}{
		{"import 'forbidden';", uiPluginErrInvalidProgram, "module imports are unavailable"},
		{"await import('forbidden');", uiPluginErrExecutionFailed, "Not supported"},
		{"eval('1 + 1');", uiPluginErrExecutionFailed, "EvalError"},
		{"new Function('return 1')();", uiPluginErrExecutionFailed, "EvalError"},
		{"WebAssembly;", uiPluginErrExecutionFailed, "WebAssembly is not defined"},
		{"new ArrayBuffer(1);", uiPluginErrExecutionFailed, "ArrayBuffer is not defined"},
		{"new Uint8Array(1);", uiPluginErrExecutionFailed, "Uint8Array is not defined"},
	}
	for index, testCase := range cases {
		instance := uint64(index + 1)
		host.send(t, instance, map[string]any{"type": "initialize", "source": testCase.source})
		gotInstance, failure := host.receive(t)
		if gotInstance != instance || failure["type"] != "error" || failure["code"] != testCase.code {
			t.Fatalf("%s: error = (%d, %v), want %s", testCase.source, gotInstance, failure, testCase.code)
		}
		scriptError, _ := failure["scriptError"].(map[string]any)
		if scriptError == nil || !strings.Contains(fmt.Sprint(scriptError["message"]), testCase.diagnostic) {
			t.Fatalf("%s: scriptError = %v", testCase.source, failure["scriptError"])
		}
		if gotInstance, closed := host.receive(t); gotInstance != instance || closed["type"] != "closed" {
			t.Fatalf("%s: close = (%d, %v)", testCase.source, gotInstance, closed)
		}
	}
	host.close(t)
}

func TestUIPluginFailuresDoNotAffectHealthyPeersLikeRust(t *testing.T) {
	const (
		healthy   = uint64(1)
		rejecting = uint64(2)
		looping   = uint64(3)
	)
	host := startUIPluginTestHost(t)
	source := `
		let n = 0;
		export function count() { return String(++n); }
		export function caught() {
			const p = Promise.reject('handled');
			p.catch(() => {});
			return 'ok';
		}
		export function detached() {
			Promise.resolve().then(() => { throw Error('detached failure'); });
			return 'ok';
		}
	`
	for _, instance := range []uint64{healthy, rejecting} {
		initializeUIPlugin(t, host, instance, source)
	}
	for _, call := range []struct {
		instance uint64
		export   string
		want     string
	}{
		{healthy, "count", "1"},
		{healthy, "count", "2"},
		{rejecting, "caught", "ok"},
		{rejecting, "count", "1"},
	} {
		instance, reply := dispatchUIPlugin(t, host, call.instance, 0, call.export, uiPluginReturnMaybeAsync)
		if instance != call.instance || reply["type"] != "reply" || reply["value"] != call.want {
			t.Fatalf("%s reply = (%d, %v)", call.export, instance, reply)
		}
	}
	instance, failure := dispatchUIPlugin(t, host, rejecting, 0, "detached", uiPluginReturnMaybeAsync)
	if instance != rejecting || failure["code"] != uiPluginErrExecutionFailed {
		t.Fatalf("detached = (%d, %v)", instance, failure)
	}
	scriptError, _ := failure["scriptError"].(map[string]any)
	if !strings.Contains(fmt.Sprint(scriptError["message"]), "detached failure") {
		t.Fatalf("detached scriptError = %v", failure["scriptError"])
	}
	if instance, closed := host.receive(t); instance != rejecting || closed["type"] != "closed" {
		t.Fatalf("detached close = (%d, %v)", instance, closed)
	}

	initializeUIPlugin(t, host, looping, "export function loop() { while (true) {} }")
	host.send(t, looping, map[string]any{"type": "dispatch", "id": 0, "export": "loop", "returnMode": uiPluginReturnMaybeAsync})
	host.send(t, healthy, map[string]any{"type": "dispatch", "id": 0, "export": "count", "returnMode": uiPluginReturnMaybeAsync})
	instance, failure = host.receive(t)
	if instance != looping || failure["code"] != uiPluginErrExecutionTimeout || failure["scriptError"] != nil {
		t.Fatalf("timeout = (%d, %v)", instance, failure)
	}
	if instance, closed := host.receive(t); instance != looping || closed["type"] != "closed" {
		t.Fatalf("timeout close = (%d, %v)", instance, closed)
	}
	instance, reply := host.receive(t)
	if instance != healthy || reply["type"] != "reply" || reply["value"] != "3" {
		t.Fatalf("healthy reply = (%d, %v)", instance, reply)
	}
	host.close(t)
}

func TestUIPluginClosedInstanceCanBeReplacedAtCapacityLikeRust(t *testing.T) {
	host := startUIPluginTestHost(t)
	initialize := map[string]any{"type": "initialize", "source": "export default () => 'ok'"}
	for instance := uint64(0); instance < uiPluginMaxInstances; instance++ {
		host.send(t, instance, initialize)
		gotInstance, message := host.receive(t)
		if gotInstance != instance || message["type"] != "initialized" {
			t.Fatalf("initialized = (%d, %v)", gotInstance, message)
		}
	}
	host.send(t, 0, map[string]any{"type": "close"})
	if instance, closed := host.receive(t); instance != 0 || closed["type"] != "closed" {
		t.Fatalf("close = (%d, %v)", instance, closed)
	}
	host.send(t, uiPluginMaxInstances, initialize)
	gotInstance, message := host.receive(t)
	if gotInstance != uint64(uiPluginMaxInstances) || message["type"] != "initialized" {
		t.Fatalf("replacement = (%d, %v)", gotInstance, message)
	}
	host.close(t)
}

func TestUIPluginSourceResultAndMethodLimitsLikeRust(t *testing.T) {
	host := startUIPluginTestHost(t)
	host.send(t, 1, map[string]any{"type": "initialize", "source": strings.Repeat("a", uiPluginMaxSourceBytes+1)})
	if instance, failure := host.receive(t); instance != 1 || failure["code"] != uiPluginErrInvalidProgram {
		t.Fatalf("oversized source = (%d, %v)", instance, failure)
	}
	if _, closed := host.receive(t); closed["type"] != "closed" {
		t.Fatalf("oversized source close = %v", closed)
	}

	initializeUIPlugin(t, host, 2, fmt.Sprintf("export default () => 'x'.repeat(%d);", uiPluginMaxResultBytes+1))
	if instance, failure := dispatchUIPlugin(t, host, 2, 0, "default", uiPluginReturnMaybeAsync); instance != 2 || failure["code"] != uiPluginErrQuotaExceeded {
		t.Fatalf("oversized result = (%d, %v)", instance, failure)
	}
	if _, closed := host.receive(t); closed["type"] != "closed" {
		t.Fatalf("oversized result close = %v", closed)
	}

	initializeUIPlugin(t, host, 3, "export default () => 'ok'")
	if instance, failure := dispatchUIPlugin(t, host, 3, 0, strings.Repeat("a", uiPluginMaxMethodBytes+1), uiPluginReturnSync); instance != 3 || failure["code"] != uiPluginErrQuotaExceeded {
		t.Fatalf("oversized method = (%d, %v)", instance, failure)
	}
	if _, closed := host.receive(t); closed["type"] != "closed" {
		t.Fatalf("oversized method close = %v", closed)
	}
	host.close(t)
}

func TestUIPluginRejectsOversizedFramesLikeRust(t *testing.T) {
	hostInput, testInput := io.Pipe()
	testOutput, hostOutput := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- RunUIPluginSandbox(hostInput, hostOutput)
		_ = hostOutput.Close()
	}()
	if _, ok, err := readUIPluginFrame(testOutput); err != nil || !ok {
		t.Fatalf("read READY frame: ok=%v err=%v", ok, err)
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(uiPluginMaxFrameBytes+1))
	if _, err := testInput.Write(header[:]); err != nil {
		t.Fatalf("write oversized header: %v", err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("RunUIPluginSandbox() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("oversized frame did not stop the host")
	}
	_ = testInput.Close()
	_ = testOutput.Close()
}

func TestUIPluginHostMessagesRejectUnknownOrNullFields(t *testing.T) {
	for _, payload := range []string{
		`{"type":"close","id":1}`,
		`{"type":"initialize"}`,
		`{"type":"initialize","source":null}`,
		`{"type":"dispatch","id":null,"export":"default","returnMode":"sync"}`,
		`{"type":"dispatch","id":0,"export":"default","returnMode":"other"}`,
		`{"type":"unknown"}`,
	} {
		if _, err := decodeUIPluginHostMessage([]byte(payload)); err == nil {
			t.Fatalf("decodeUIPluginHostMessage(%s) unexpectedly succeeded", payload)
		}
	}
	for _, payload := range []string{
		`{"type":"initialize","source":"export default () => 1"}`,
		`{"type":"dispatch","id":0,"export":"default","returnMode":"maybeAsync"}`,
		`{"type":"close"}`,
	} {
		if _, err := decodeUIPluginHostMessage([]byte(payload)); err != nil {
			t.Fatalf("decodeUIPluginHostMessage(%s) error = %v", payload, err)
		}
	}
}
