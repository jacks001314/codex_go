package appserver

import (
	"encoding/json"
	"path/filepath"
	"reflect"
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

func TestCommandExecutionActionsPreserveExecutorPathsLikeRust(t *testing.T) {
	for _, test := range []struct {
		cwd  string
		path string
		want string
	}{
		{cwd: "file:///home/alice/repo", path: "src/main.rs", want: "/home/alice/repo/src/main.rs"},
		{cwd: "file:///C:/Users/Alice%20Smith/repo", path: `src\main.rs`, want: `C:\Users\Alice Smith\repo\src\main.rs`},
		{cwd: "file:///C:/Users/Alice%20Smith/repo", path: `C:src\main.rs`, want: `C:\Users\Alice Smith\repo\src\main.rs`},
		{cwd: "file://server/share/repo", path: `src\main.rs`, want: `\\server\share\repo\src\main.rs`},
	} {
		command := "cat " + test.path
		got := commandExecutionActions([]string{"cat", test.path}, command, test.cwd)
		want := []map[string]any{{"type": "read", "command": command, "name": "main.rs", "path": test.want}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("commandExecutionActions(%q, %q) = %#v, want %#v", test.cwd, test.path, got, want)
		}
	}
}
