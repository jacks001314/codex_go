package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestStandaloneVersionReportsExecutorName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"--version"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "codex-exec-server ") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestStandaloneHelpDocumentsExecutorOptions(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"--help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"Usage: codex-exec-server", "--listen", "--remote", "--environment-id"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %q", want, stdout.String())
		}
	}
}

func TestStandaloneRejectsUnknownOptionBeforeStartup(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"--unknown"}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("unexpected success")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown exec-server option") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestStandaloneParsesExecutorArgumentsWithoutSubcommand(t *testing.T) {
	parsed, err := parseStandaloneArguments([]string{"--listen", "stdio"})
	if err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if parsed.ExecServer.Listen != "stdio" || !parsed.ExecServer.ListenSet {
		t.Fatalf("listen = %#v", parsed)
	}
}
