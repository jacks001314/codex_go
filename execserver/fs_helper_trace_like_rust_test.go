package execserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"codex_go/tracehook"
)

func TestFSHelperRecordsPreparedPermissionEntriesLikeRust(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux filesystem helper launch path")
	}
	for _, legacy := range []bool{false, true} {
		t.Run(strconv.FormatBool(legacy), func(t *testing.T) {
			cwd := t.TempDir()
			roots := []string{t.TempDir(), t.TempDir()}
			// A missing launcher exercises real preparation and fails before
			// any sandbox or filesystem mutation, independent of host support.
			launcher := filepath.Join(t.TempDir(), "missing-linux-sandbox")
			policy := &FileSystemSandboxContext{
				Permissions: json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"project_roots"}},"access":"write"}]},"network":"restricted"}`),
				CWD:         cwd, WorkspaceRoots: roots, UseLegacyLandlock: legacy,
				codexLinuxSandboxExe: launcher,
			}
			required, profile, profileJSON, policyCWD, workspaceRoots, err := prepareFSSandboxForOperation(policy, MethodFSWriteFile)
			if err != nil || !required {
				t.Fatalf("prepare: required=%v err=%v", required, err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			plan, err := fsSandboxRunPlan(policy, profile, profileJSON, policyCWD, workspaceRoots, "", executable, fsHelperCommandForExecutable(executable))
			if err != nil {
				t.Fatal(err)
			}
			var prepared struct {
				FileSystem struct {
					Entries []json.RawMessage `json:"entries"`
				} `json:"file_system"`
			}
			if err := json.Unmarshal([]byte(plan.PermissionProfileJSON), &prepared); err != nil {
				t.Fatal(err)
			}
			if len(prepared.FileSystem.Entries) <= 1 {
				t.Fatalf("prepared policy did not materialize workspace roots: %s", plan.PermissionProfileJSON)
			}

			target := filepath.Join(roots[0], "untouched.txt")
			wirePath := func(path string) string {
				uri, err := normalizeFSPathForWire(path)
				if err != nil {
					t.Fatal(err)
				}
				return uri
			}
			wirePolicy := *policy
			wirePolicy.CWD = wirePath(cwd)
			wirePolicy.WorkspaceRoots = []string{wirePath(roots[0]), wirePath(roots[1])}
			params, err := json.Marshal(FSWriteFileParams{
				Path: wirePath(target), DataBase64: base64.StdEncoding.EncodeToString([]byte("data")), Sandbox: &wirePolicy,
			})
			if err != nil {
				t.Fatal(err)
			}
			server := NewServer()
			server.SetCodexLinuxSandboxExe(launcher)
			root := newRecordingTraceSpan()
			ctx := tracehook.WithSpan(context.Background(), root)
			req := &request{Method: MethodFSWriteFile, Params: params}
			_, tracedErr := server.handleRequest(ctx, req)
			if tracedErr == nil || !strings.Contains(tracedErr.Error(), "fs sandbox helper start failed") {
				t.Fatalf("write error = %v, want missing launcher", tracedErr)
			}
			children := root.childrenSnapshot()
			if len(children) != 1 {
				t.Fatalf("child spans = %d, want 1", len(children))
			}
			child := children[0]
			attributes := child.attributesSnapshot()
			if child.name != "fs.sandbox_execute" || attributes["operation"] != MethodFSWriteFile {
				t.Fatalf("span = %q, attributes = %#v", child.name, attributes)
			}
			// Rust records the count right after materializing project roots and
			// before the minimal and runtime read grants, so the recorded value is
			// the pre-grant count and must be smaller than the launched policy.
			want := strconv.Itoa(plan.PermissionEntryCount)
			t.Logf("recorded permission_entries = %s, final policy entries = %d (legacy Landlock=%v)", want, len(prepared.FileSystem.Entries), legacy)
			if plan.PermissionEntryCount <= 1 {
				t.Fatalf("pre-grant entry count = %d, want the materialized roots", plan.PermissionEntryCount)
			}
			if plan.PermissionEntryCount > len(prepared.FileSystem.Entries) {
				t.Fatalf("pre-grant entry count = %d exceeds the final %d", plan.PermissionEntryCount, len(prepared.FileSystem.Entries))
			}
			if !legacy && plan.PermissionEntryCount == len(prepared.FileSystem.Entries) {
				// The modern policy must gain runtime read grants after the
				// recording point; equality would mean the count was taken late.
				t.Fatalf("pre-grant entry count = %d, want fewer than the final %d", plan.PermissionEntryCount, len(prepared.FileSystem.Entries))
			}
			if got := attributes["permission_entries"]; got != want {
				t.Fatalf("permission_entries = %q, want %q", got, want)
			}
			if !child.ended {
				t.Fatal("filesystem sandbox span did not end on launch failure")
			}
			if _, present := root.attributesSnapshot()["permission_entries"]; present {
				t.Fatal("permission_entries recorded on parent instead of child")
			}
			// The same request without a span must retain its error and leave
			// the target untouched, including when preparation adds grants.
			_, untracedErr := server.handleRequest(context.Background(), req)
			if untracedErr == nil || untracedErr.Error() != tracedErr.Error() {
				t.Fatalf("untraced error = %v, traced error = %v", untracedErr, tracedErr)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed sandbox launch mutated target: %v", err)
			}
		})
	}
}
