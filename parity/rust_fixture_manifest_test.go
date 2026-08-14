package parity

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

type rustFixtureRoot struct {
	Path     string
	Files    int
	Owner    string
	Focus    string
	Required []string
}

func TestRustGoldenFixtureRootsSnapshot(t *testing.T) {
	root := rustSnapshotRoot(t)
	for _, entry := range rustGoldenFixtureRootsSnapshot() {
		if entry.Path == "" || entry.Owner == "" || entry.Focus == "" || entry.Files <= 0 {
			t.Fatalf("incomplete Rust fixture root manifest entry: %#v", entry)
		}
		abs := filepath.Join(root, filepath.FromSlash(entry.Path))
		info, err := os.Stat(abs)
		if err != nil {
			t.Fatalf("Rust fixture root %s missing: %v", entry.Path, err)
		}
		if !info.IsDir() {
			t.Fatalf("Rust fixture root %s is not a directory", entry.Path)
		}
		files := countFilesRecursive(t, abs)
		if files != entry.Files {
			t.Fatalf("Rust fixture root %s file-count drift: got %d want %d", entry.Path, files, entry.Files)
		}
		for _, required := range entry.Required {
			path := filepath.Join(root, filepath.FromSlash(required))
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("required Rust fixture %s missing for root %s: %v", required, entry.Path, err)
			}
		}
	}
}

func rustGoldenFixtureRootsSnapshot() []rustFixtureRoot {
	return []rustFixtureRoot{
		{
			Path:  "cli/tests",
			Files: 18,
			Owner: "cli, app",
			Focus: "CLI help, hidden commands, feature flags, MCP/plugin/login flows",
			Required: []string{
				"cli/tests/features.rs",
				"cli/tests/mcp_list.rs",
				"cli/tests/plugin_cli.rs",
			},
		},
		{
			Path:  "exec/tests",
			Files: 19,
			Owner: "exec, app",
			Focus: "exec JSON output, prompt/stdin, output schema, resume, sandbox, hooks",
			Required: []string{
				"exec/tests/event_processor_with_json_output.rs",
				"exec/tests/suite/output_schema.rs",
				"exec/tests/suite/prompt_stdin.rs",
			},
		},
		{
			Path:  "exec/tests/suite",
			Files: 16,
			Owner: "exec",
			Focus: "codex exec end-to-end suite cases",
			Required: []string{
				"exec/tests/suite/approval_policy.rs",
				"exec/tests/suite/resume.rs",
				"exec/tests/suite/server_error_exit.rs",
			},
		},
		{
			Path:  "app-server/tests/suite/v2",
			Files: 105,
			Owner: "appserver",
			Focus: "JSON-RPC v2 protocol and runtime fixtures",
			Required: []string{
				"app-server/tests/suite/v2/initialize.rs",
				"app-server/tests/suite/v2/output_schema.rs",
				"app-server/tests/suite/v2/turn_start.rs",
			},
		},
		{
			Path:  "core/tests/suite",
			Files: 159,
			Owner: "turn, model, tool, session",
			Focus: "core agent loop, model client, session, tools, sandbox, resume",
			Required: []string{
				"core/tests/suite/request_compression.rs",
				"core/tests/suite/review.rs",
				"core/tests/suite/turn_state.rs",
				"core/tests/suite/unified_exec.rs",
			},
		},
		{
			Path:  "tui/src/chatwidget/tests",
			Files: 35,
			Owner: "tui",
			Focus: "chat widget behavior and snapshot coverage",
			Required: []string{
				"tui/src/chatwidget/tests/approval_requests.rs",
				"tui/src/chatwidget/tests/composer_submission.rs",
				"tui/src/chatwidget/tests/status_and_layout.rs",
			},
		},
		{
			Path:  "tui/src/chatwidget/tests/snapshots",
			Files: 12,
			Owner: "tui",
			Focus: "Rust terminal snapshot goldens",
			Required: []string{
				"tui/src/chatwidget/tests/snapshots/codex_tui__chatwidget__tests__approval_requests__exec_approval_modal_exec.snap",
			},
		},
		{
			Path:  "tools/src",
			Files: 31,
			Owner: "tool, turn",
			Focus: "tool schemas, dynamic tools, MCP tools, tool search",
			Required: []string{
				"tools/src/json_schema_tests.rs",
				"tools/src/tool_search_tests.rs",
				"tools/src/tool_spec_tests.rs",
			},
		},
	}
}

func countFilesRecursive(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s) error = %v", root, err)
	}
	return count
}
