package shell

import (
	"strings"
	"testing"
	"time"
)

func TestCommandRecordRenderAndItem(t *testing.T) {
	record := NewCommandRecord("go test", ExecOutput{ExitCode: 1, Duration: time.Second, Stdout: "out", Stderr: "err"}, 0)
	rendered := record.Render()
	if !strings.Contains(rendered, "command: go test") || !strings.Contains(rendered, "exit_code: 1") || !strings.Contains(rendered, "out\nerr") {
		t.Fatalf("rendered = %q", rendered)
	}
	item := record.ResponseItem()
	if item.Role != "user" || len(item.Content) != 1 {
		t.Fatalf("item = %#v", item)
	}
}

func TestFormatExecOutputTruncates(t *testing.T) {
	got := FormatExecOutput(ExecOutput{Stdout: "abcdef"}, 3)
	if got != "abc\n[truncated]" {
		t.Fatalf("got = %q", got)
	}
}
