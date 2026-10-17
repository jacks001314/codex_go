package appserver

import (
	"testing"

	"codex_go/tool"
)

func TestUnifiedExecLaunchFailureItemOmitsProcessIDLikeRust(t *testing.T) {
	item := unifiedExecThreadItem(tool.UnifiedExecEvent{
		Kind:      tool.UnifiedExecEventEnd,
		CallID:    "failed-launch",
		Command:   []string{"missing-binary", "arg"},
		CWD:       "/repo",
		Output:    "start missing-binary: executable file not found",
		ExitCode:  -1,
		StartedAt: fixedTime(),
	}, CommandExecutionFailed)

	if item.ID != "failed-launch" || item.Type != "commandExecution" {
		t.Fatalf("item = %#v", item)
	}
	if _, ok := item.Data["processId"]; ok {
		t.Fatalf("failed launch reported processId: %#v", item.Data)
	}
	if _, ok := item.Data["process_id"]; ok {
		t.Fatalf("failed launch reported process_id: %#v", item.Data)
	}
	if item.Data["status"] != string(CommandExecutionFailed) ||
		item.Data["exitCode"] != -1 ||
		item.Data["durationMs"] != int64(0) ||
		item.Data["aggregatedOutput"] != "start missing-binary: executable file not found" {
		t.Fatalf("failed launch data = %#v", item.Data)
	}
}
