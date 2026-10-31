package exec

import (
	"errors"
	"io"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/session"
)

// Rust's resolve_resume_thread_id returns None for an unknown session name, so
// exec falls back to thread/start and still runs the prompt on a new thread.
func TestResolveExecResumeUnknownNameStartsNewThread(t *testing.T) {
	runner := NewRunner(t.TempDir())
	req := &Request{Exec: cli.ExecOptions{
		Subcommand: "resume",
		Resume: cli.ExecResumeOptions{
			SessionID: "ghost-session",
			Prompt:    "Reply with exactly: RS-OK",
		},
	}}

	prompt, resume, err := runner.promptForRequest(req, nil, strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatalf("promptForRequest returned error: %v", err)
	}
	if resume != nil {
		t.Fatalf("resume context = %#v, want nil so the turn starts a new thread", resume)
	}
	if prompt != "Reply with exactly: RS-OK" {
		t.Fatalf("prompt = %q", prompt)
	}
}

// An empty --last lookup is the same None result, so Rust starts a new thread.
func TestResolveExecResumeLastWithoutHistoryStartsNewThread(t *testing.T) {
	runner := NewRunner(t.TempDir())
	req := &Request{Exec: cli.ExecOptions{
		Subcommand: "resume",
		Resume: cli.ExecResumeOptions{
			Last:   true,
			All:    true,
			Prompt: "Reply with exactly: LAST-OK",
		},
	}}

	prompt, resume, err := runner.promptForRequest(req, nil, strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatalf("promptForRequest returned error: %v", err)
	}
	if resume != nil {
		t.Fatalf("resume context = %#v, want nil", resume)
	}
	if prompt != "Reply with exactly: LAST-OK" {
		t.Fatalf("prompt = %q", prompt)
	}
}

// A thread id is taken at face value, so Rust reaches thread/resume and reports
// the app-server failure instead of silently starting a new thread.
func TestResolveExecResumeMissingUUIDReportsAppServerError(t *testing.T) {
	runner := NewRunner(t.TempDir())
	req := &Request{Exec: cli.ExecOptions{
		Subcommand: "resume",
		Resume:     cli.ExecResumeOptions{SessionID: "019a0000-0000-7000-8000-000000000000"},
		Prompt:     "hi",
	}}

	_, _, err := runner.promptForRequest(req, nil, strings.NewReader(""), io.Discard)
	want := "thread/resume: thread/resume failed: no rollout found for thread id 019a0000-0000-7000-8000-000000000000 (code -32600)"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestResolveExecForkMissingUUIDReportsForkError(t *testing.T) {
	runner := NewRunner(t.TempDir())
	req := &Request{Exec: cli.ExecOptions{
		Subcommand: "fork",
		Fork:       cli.ExecForkOptions{SessionID: "019a0000-0000-7000-8000-000000000000", Prompt: "hi"},
	}}

	_, _, err := runner.promptForRequest(req, nil, strings.NewReader(""), io.Discard)
	want := "thread/fork: thread/fork failed: no rollout found for thread id 019a0000-0000-7000-8000-000000000000 (code -32600)"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestResolveExecForkUnknownNameReportsSessionNotFound(t *testing.T) {
	runner := NewRunner(t.TempDir())
	req := &Request{Exec: cli.ExecOptions{
		Subcommand: "fork",
		Fork:       cli.ExecForkOptions{SessionID: "ghost-session", Prompt: "hi"},
	}}

	_, _, err := runner.promptForRequest(req, nil, strings.NewReader(""), io.Discard)
	if err == nil || err.Error() != "Session not found: ghost-session" {
		t.Fatalf("error = %v, want Session not found", err)
	}
}

func TestResolveExecResumeRecordSignalsNoTargetForUnknownName(t *testing.T) {
	runner := NewRunner(t.TempDir())
	_, err := runner.resolveExecResumeRecordForMethod(&Request{Exec: cli.ExecOptions{
		Resume: cli.ExecResumeOptions{SessionID: "ghost-session"},
	}}, execThreadResumeMethod)
	if !errors.Is(err, errExecResumeNoTarget) {
		t.Fatalf("error = %v, want errExecResumeNoTarget", err)
	}
}

func TestExecAppServerThreadErrorMirrorsTypedRequestLayering(t *testing.T) {
	err := execAppServerThreadError(execThreadResumeMethod, session.ThreadID("abc"))
	want := "thread/resume: thread/resume failed: no rollout found for thread id abc (code -32600)"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
