package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseCLIPluginArgumentsAreValidatedBeforeStartup(t *testing.T) {
	for _, arguments := range [][]string{
		{"--help", "--codex-ui-plugin-process"},
		{"--codex-ui-plugin-process", "--help"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(arguments, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("%v: exit = %d, stderr = %q", arguments, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage:") {
			t.Fatalf("%v: stdout = %q", arguments, stdout.String())
		}
	}
	for _, arguments := range [][]string{
		{"--unknown", "--codex-ui-plugin-process"},
		{"--codex-ui-plugin-process", "--unknown"},
		{"--listen=stdio", "--codex-ui-plugin-process"},
		{"--codex-ui-plugin-process", "--listen=stdio"},
		{"--codex-ui-plugin-process", "--otel-trace-listen=ws://localhost:1234"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(arguments, strings.NewReader(""), &stdout, &stderr); code == 0 {
			t.Fatalf("%v: unexpectedly succeeded", arguments)
		}
		if stdout.Len() != 0 {
			t.Fatalf("%v: stdout = %q", arguments, stdout.String())
		}
		if !strings.Contains(stderr.String(), "error:") {
			t.Fatalf("%v: stderr = %q", arguments, stderr.String())
		}
	}
}

func TestParseCLIUIPluginMode(t *testing.T) {
	options, err := parseCLI([]string{"--codex-ui-plugin-process"})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if !options.uiPluginProcess || options.listen != "stdio" {
		t.Fatalf("options = %+v", options)
	}
}

// Rust's parse_listen_url (code-mode-host/src/transport.rs) accepts exactly
// `stdio`, `stdio://`, `grpc+stdio://`, and `grpc://IP:PORT`, and reports the
// unsupported or malformed URL verbatim.
func TestSelectListenTransportLikeRust(t *testing.T) {
	cases := []struct {
		listen    string
		transport listenTransport
		address   string
	}{
		{listen: "stdio", transport: listenStdio},
		{listen: "stdio://", transport: listenStdio},
		{listen: "grpc+stdio://", transport: listenGrpcStdio},
		{listen: "grpc://127.0.0.1:8080", transport: listenGrpc, address: "127.0.0.1:8080"},
		{listen: "grpc://[::1]:8080", transport: listenGrpc, address: "[::1]:8080"},
	}
	for _, testCase := range cases {
		transport, address, err := selectListenTransport(testCase.listen)
		if err != nil {
			t.Fatalf("%q: error = %v", testCase.listen, err)
		}
		if transport != testCase.transport || address != testCase.address {
			t.Fatalf("%q: got (%v, %q), want (%v, %q)", testCase.listen, transport, address, testCase.transport, testCase.address)
		}
	}
	for _, listen := range []string{"grpc://localhost:8080", "grpc://127.0.0.1", "grpc://", "tcp://127.0.0.1:1"} {
		if _, _, err := selectListenTransport(listen); err == nil {
			t.Fatalf("%q: unexpectedly accepted", listen)
		}
	}
	if _, _, err := selectListenTransport("grpc://localhost:8080"); err == nil || err.Error() != "invalid gRPC --listen URL `grpc://localhost:8080`; expected `grpc://IP:PORT`" {
		t.Fatalf("invalid URL error = %v", err)
	}
	if _, _, err := selectListenTransport("tcp://127.0.0.1:1"); err == nil || err.Error() != "unsupported --listen URL `tcp://127.0.0.1:1`; expected `grpc://IP:PORT`, `grpc+stdio://`, `stdio`, or `stdio://`" {
		t.Fatalf("unsupported URL error = %v", err)
	}
}

func TestRunCLIRejectsUnsupportedListenURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"--listen", "tcp://127.0.0.1:1"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "unsupported --listen URL `tcp://127.0.0.1:1`") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
