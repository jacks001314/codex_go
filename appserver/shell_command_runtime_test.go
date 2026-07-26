package appserver

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/tool"
)

func TestCommandExecutionStartedThreadItemSupportsRustLegacyShellCommand(t *testing.T) {
	workdir := t.TempDir()
	encodedWorkdir, err := json.Marshal(workdir)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Unix(123, 0).UTC()
	item, ok := commandExecutionStartedThreadItem(&tool.Invocation{
		CallID:   "legacy-weather",
		ToolName: tool.PlainName(tool.DefaultShellCommandToolName),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"command":"curl.exe -sS https://sdk-weather.invalid/Yunnan?format=j1","workdir":` + string(encodedWorkdir) + `}`,
		},
	}, "turn-weather", "", startedAt)
	if !ok {
		t.Fatal("legacy shell_command did not create commandExecution item")
	}
	if item.ID != "legacy-weather" || item.Type != "commandExecution" || item.TurnID != "turn-weather" || item.CreatedAt != startedAt.UnixMilli() {
		t.Fatalf("item identity = %#v", item)
	}
	if item.Data["command"] != "curl.exe -sS https://sdk-weather.invalid/Yunnan?format=j1" ||
		item.Data["status"] != string(CommandExecutionInProgress) || item.Data["source"] != string(CommandExecutionSourceAgent) {
		t.Fatalf("item data = %#v", item.Data)
	}
	wantWorkdir, err := filepath.Abs(workdir)
	if err != nil {
		t.Fatal(err)
	}
	if item.Data["cwd"] != wantWorkdir {
		t.Fatalf("cwd = %#v, want %q", item.Data["cwd"], wantWorkdir)
	}
}
