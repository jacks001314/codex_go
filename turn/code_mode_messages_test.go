package turn

import (
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
)

func TestCodeModeOnlyExecPromptUsesCatalogMessagesLikeRust(t *testing.T) {
	execText := "Catalog exec default {{ default_exec_yield_time_ms }}"
	guidance := ""
	preamble := "type Catalog = string;"
	messages := &model.CodeModeToolMessages{
		Exec:                        &model.CodeModeToolMessage{Description: &execText},
		DeferredNestedToolsGuidance: &guidance,
		McpTypescriptPreamble:       &preamble,
	}
	execSpec := tool.Spec{
		Name:        tool.PlainName(tool.CodeModeExecToolName),
		Description: "bundled",
		Freeform:    &tool.FreeformSpec{Syntax: "lark", Definition: "grammar"},
	}
	got := codeModeOnlyExecPromptSpecsWithOptions([]tool.Spec{execSpec}, nil, nil, messages, 5*time.Second)
	if len(got) != 1 {
		t.Fatalf("specs = %#v", got)
	}
	description := got[0].Description
	if !strings.Contains(description, "Catalog exec default 5000") {
		t.Fatalf("exec description = %q", description)
	}
	if strings.Contains(description, "Some deferred nested tools may be omitted") {
		t.Fatal("empty guidance override did not suppress bundled guidance")
	}
	if !strings.Contains(description, "type Catalog = string;") {
		t.Fatalf("custom MCP preamble missing: %s", description)
	}
}
