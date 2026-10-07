package tool

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/execserver"
	"codex_go/metrics"
)

// agentsMdMetricsRecorder captures the counter calls the apply_patch executor
// makes on the process-global metrics recorder.
type agentsMdMetricsRecorder struct {
	mu    sync.Mutex
	calls []agentsMdMetricsCall
}

type agentsMdMetricsCall struct {
	name string
	inc  int
	tags map[string]string
}

func (r *agentsMdMetricsRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *agentsMdMetricsRecorder) Counter(name string, inc int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, agentsMdMetricsCall{name: name, inc: inc, tags: tags})
}

func (r *agentsMdMetricsRecorder) Histogram(string, int, map[string]string) {}

// install makes the recorder the process-global recorder for this test and
// restores the empty recorder afterwards.
func (r *agentsMdMetricsRecorder) install(t *testing.T) {
	t.Helper()
	metrics.InstallGlobal(r)
	t.Cleanup(func() { metrics.InstallGlobal(nil) })
}

// agentsMdFilenames returns the sorted `filename` tags recorded on the
// codex.agents_md.edit counter (other counters are ignored).
func (r *agentsMdMetricsRecorder) agentsMdFilenames(t *testing.T) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var filenames []string
	for _, call := range r.calls {
		if call.name != agentsMdEditMetricName {
			continue
		}
		if call.inc != 1 {
			t.Fatalf("codex.agents_md.edit inc = %d, want 1", call.inc)
		}
		if len(call.tags) != 1 || call.tags["filename"] == "" {
			t.Fatalf("codex.agents_md.edit tags = %#v, want a single filename tag", call.tags)
		}
		filenames = append(filenames, call.tags["filename"])
	}
	sort.Strings(filenames)
	return filenames
}

func agentsMdInvocation(callID, patch string) *Invocation {
	return &Invocation{
		CallID:   callID,
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: patch},
	}
}

func writeAgentsMdFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// Rust #51652: a committed AGENTS.md (or AGENTS.override.md) edit increments
// codex.agents_md.edit and is tagged with the lowercase basename, matching the
// filename case-insensitively; a non-AGENTS.md file is not counted.
func TestApplyPatchAgentsMdEditMetricLikeRust(t *testing.T) {
	dir := t.TempDir()
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: dir})

	recorder := &agentsMdMetricsRecorder{}
	recorder.install(t)

	patch := "*** Begin Patch\n" +
		"*** Add File: AGENTS.md\n" +
		"+root agents\n" +
		"*** Add File: nested/AGENTS.Override.MD\n" +
		"+nested agents\n" +
		"*** Add File: notes.txt\n" +
		"+not agents\n" +
		"*** End Patch"
	output, err := executor.Execute(context.Background(), agentsMdInvocation("agents-md-basic", patch))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("Execute output = %+v, want success", output)
	}
	if got := strings.Join(recorder.agentsMdFilenames(t), ","); got != "agents.md,agents.override.md" {
		t.Fatalf("codex.agents_md.edit filenames = %q, want %q", got, "agents.md,agents.override.md")
	}
}

// Rust #51652 counts a distinct move destination in addition to the change path,
// while a move whose destination equals its source is only counted once.
func TestApplyPatchAgentsMdMoveDestinationMetricLikeRust(t *testing.T) {
	t.Run("distinct destination", func(t *testing.T) {
		dir := t.TempDir()
		writeAgentsMdFile(t, filepath.Join(dir, "notes.md"), "old\n")
		executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: dir})

		recorder := &agentsMdMetricsRecorder{}
		recorder.install(t)

		patch := "*** Begin Patch\n" +
			"*** Update File: notes.md\n" +
			"*** Move to: AGENTS.md\n" +
			"@@\n" +
			"-old\n" +
			"+new\n" +
			"*** End Patch"
		output, err := executor.Execute(context.Background(), agentsMdInvocation("agents-md-move", patch))
		if err != nil {
			t.Fatalf("Execute error = %v", err)
		}
		if output == nil || !output.Success {
			t.Fatalf("Execute output = %+v, want success", output)
		}
		if got := strings.Join(recorder.agentsMdFilenames(t), ","); got != "agents.md" {
			t.Fatalf("codex.agents_md.edit filenames = %q, want %q", got, "agents.md")
		}
	})

	t.Run("destination equals source", func(t *testing.T) {
		dir := t.TempDir()
		writeAgentsMdFile(t, filepath.Join(dir, "AGENTS.md"), "old\n")
		executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: dir})

		recorder := &agentsMdMetricsRecorder{}
		recorder.install(t)

		patch := "*** Begin Patch\n" +
			"*** Update File: AGENTS.md\n" +
			"*** Move to: AGENTS.md\n" +
			"@@\n" +
			"-old\n" +
			"+new\n" +
			"*** End Patch"
		output, err := executor.Execute(context.Background(), agentsMdInvocation("agents-md-move-same", patch))
		if err != nil {
			t.Fatalf("Execute error = %v", err)
		}
		if output == nil || !output.Success {
			t.Fatalf("Execute output = %+v, want success", output)
		}
		if got := strings.Join(recorder.agentsMdFilenames(t), ","); got != "agents.md" {
			t.Fatalf("codex.agents_md.edit filenames = %q, want a single %q", got, "agents.md")
		}
	})
}

// Rust #51652 counts the changes committed before a later patch failure (the
// ApplyPatchFailure delta) and still reports the failure. The environment
// filesystem is injected so the committed write fails on the real apply, while
// the preflight dry-run (a local shadow workspace) succeeds.
func TestApplyPatchAgentsMdEditCountsCommittedPrefixOnFailureLikeRust(t *testing.T) {
	remote := newAgentsMdEnvironmentFileSystem("/remote-x-workspace")
	remote.files["/remote-x-workspace/poison.txt"] = []byte("before\n")
	remote.failWrites["/remote-x-workspace/poison.txt"] = "boom"

	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD:                  t.TempDir(),
		IncludeEnvironmentID: true,
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote-x"},
			ReadyEnvironmentCount:  1,
			StableEnvironmentTools: true,
		},
		EnvironmentFileSystems: agentsMdEnvironmentProvider{fileSystem: remote},
	})

	recorder := &agentsMdMetricsRecorder{}
	recorder.install(t)

	patch := "*** Begin Patch\n" +
		"*** Environment ID: remote-x\n" +
		"*** Add File: AGENTS.md\n" +
		"+root agents\n" +
		"*** Update File: poison.txt\n" +
		"@@\n" +
		"-before\n" +
		"+after\n" +
		"*** End Patch"
	output, err := executor.Execute(context.Background(), agentsMdInvocation("agents-md-partial", patch))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if output == nil || output.Success {
		t.Fatalf("Execute output = %+v, want a failure output", output)
	}
	if !strings.Contains(output.Body, "apply_patch failed") {
		t.Fatalf("output.Body = %q, want an apply_patch failure", output.Body)
	}
	if got := string(remote.files["/remote-x-workspace/AGENTS.md"]); got != "root agents\n" {
		t.Fatalf("committed AGENTS.md = %q, want it written before the failure", got)
	}
	if got := strings.Join(recorder.agentsMdFilenames(t), ","); got != "agents.md" {
		t.Fatalf("codex.agents_md.edit filenames = %q, want %q from the committed prefix", got, "agents.md")
	}
}

// agentsMdEnvironmentFileSystem is a minimal in-memory EnvironmentFileSystem whose
// CWD is a virtual POSIX-style root, so a patch resolves its targets inside the
// environment instead of the host filesystem. Keys are normalized to a
// volume-less slash form because apply_patch resolves relative paths against the
// environment cwd with the host path rules, which adds a host drive on Windows.
type agentsMdEnvironmentFileSystem struct {
	cwd        string
	files      map[string][]byte
	dirs       map[string]bool
	failWrites map[string]string
}

func newAgentsMdEnvironmentFileSystem(cwd string) *agentsMdEnvironmentFileSystem {
	return &agentsMdEnvironmentFileSystem{
		cwd:        cwd,
		files:      map[string][]byte{},
		dirs:       map[string]bool{normalizeAgentsMdEnvironmentPath(cwd): true},
		failWrites: map[string]string{},
	}
}

// normalizeAgentsMdEnvironmentPath folds host path spellings onto the virtual
// slash root: separators are unified and a Windows drive prefix is dropped, so
// the same logical file matches whether the host produced `/a/b` or `C:\a\b`.
func normalizeAgentsMdEnvironmentPath(name string) string {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if volume := filepath.VolumeName(normalized); volume != "" {
		normalized = strings.TrimPrefix(normalized, volume)
	}
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	return path.Clean(normalized)
}

func (f *agentsMdEnvironmentFileSystem) CWD() string { return f.cwd }

func (f *agentsMdEnvironmentFileSystem) GetMetadata(_ context.Context, name string, _ *execserver.FileSystemSandboxContext) (*execserver.FSGetMetadataResponse, error) {
	key := normalizeAgentsMdEnvironmentPath(name)
	if data, ok := f.files[key]; ok {
		return &execserver.FSGetMetadataResponse{IsFile: true, Size: int64(len(data))}, nil
	}
	if f.dirs[key] {
		return &execserver.FSGetMetadataResponse{IsDirectory: true}, nil
	}
	return nil, &os.PathError{Op: "lstat", Path: name, Err: os.ErrNotExist}
}

func (f *agentsMdEnvironmentFileSystem) ReadFile(_ context.Context, name string, _ *execserver.FileSystemSandboxContext) ([]byte, error) {
	if data, ok := f.files[normalizeAgentsMdEnvironmentPath(name)]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
}

func (f *agentsMdEnvironmentFileSystem) WriteFile(_ context.Context, name string, data []byte, _ *execserver.FileSystemSandboxContext) error {
	key := normalizeAgentsMdEnvironmentPath(name)
	if message, ok := f.failWrites[key]; ok {
		return fmt.Errorf("simulated write failure: %s", message)
	}
	f.files[key] = append([]byte(nil), data...)
	return nil
}

func (f *agentsMdEnvironmentFileSystem) CreateDirectory(_ context.Context, name string, _ bool, _ *execserver.FileSystemSandboxContext) error {
	f.dirs[normalizeAgentsMdEnvironmentPath(name)] = true
	return nil
}

func (f *agentsMdEnvironmentFileSystem) Remove(_ context.Context, name string, _ bool, _ bool, _ *execserver.FileSystemSandboxContext) error {
	delete(f.files, normalizeAgentsMdEnvironmentPath(name))
	return nil
}

type agentsMdEnvironmentProvider struct {
	fileSystem EnvironmentFileSystem
}

func (p agentsMdEnvironmentProvider) FileSystemFor(environmentID string) (EnvironmentFileSystem, bool) {
	if environmentID == "remote-x" {
		return p.fileSystem, true
	}
	return nil, false
}
