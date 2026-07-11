package app

import (
	"testing"

	"codex_go/internal/appserver"
)

func TestRemoteProtocolCommandExecutionPreservesLifecycle(t *testing.T) {
	started := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":      "call-1",
		"type":    "commandExecution",
		"command": "Get-ChildItem test.pdf",
		"status":  "inProgress",
	}, false)
	if started.Type != "command_execution" || started.Command != "Get-ChildItem test.pdf" || started.Status != "in_progress" {
		t.Fatalf("started item = %#v", started)
	}

	completed := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":               "call-1",
		"type":             "commandExecution",
		"command":          "Get-ChildItem test.pdf",
		"status":           "completed",
		"aggregatedOutput": "test.pdf\n",
		"exitCode":         float64(0),
	}, true)
	if completed.Type != "command_execution" || completed.Command != started.Command || completed.Status != "completed" {
		t.Fatalf("completed item = %#v", completed)
	}
	if completed.ExitCode == nil || *completed.ExitCode != 0 || completed.AggregatedOutput == nil || *completed.AggregatedOutput != "test.pdf\n" {
		t.Fatalf("completed output = %#v", completed)
	}
}
