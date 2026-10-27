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
