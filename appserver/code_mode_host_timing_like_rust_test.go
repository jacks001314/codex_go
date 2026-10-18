package appserver

import (
	"reflect"
	"testing"

	"codex_go/tool"
)

func TestCodeModeHostTimingEventFieldsLikeRust(t *testing.T) {
	fields := codeModeHostTimingEventFields("thread-1", "turn-1", tool.CodeModeCallObservation{
		Kind:                   tool.CodeModeCallCompleted,
		CallID:                 "call-1",
		CellID:                 "cell-1",
		ToolName:               tool.CodeModeWaitToolName,
		CodeModeHostDurationNS: 123456789,
	})
	want := map[string]string{
		"conversation_id":            "thread-1",
		"turn_id":                    "turn-1",
		"call_id":                    "call-1",
		"cell_id":                    "cell-1",
		"tool_name":                  tool.CodeModeWaitToolName,
		"code_mode_host_duration_ns": "123456789",
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
	if fields := codeModeHostTimingEventFields("thread-1", "turn-1", tool.CodeModeCallObservation{CallID: "call-1"}); fields != nil {
		t.Fatalf("untimed observation fields = %#v, want nil", fields)
	}
}
