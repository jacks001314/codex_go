package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"codex_go/sandbox"
)

func TestApplyPatchExecutorRunsPatchAndFormatsOutput(t *testing.T) {
	dir := t.TempDir()
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: dir})
	patch := `*** Begin Patch
*** Add File: hello.txt
+hello
*** End Patch`

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-apply",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: patch},
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !output.Success || !strings.Contains(output.Body, "A hello.txt") {
		t.Fatalf("output = %#v", output)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "hello.txt")); err != nil || string(data) != "hello\n" {
		t.Fatalf("written file = %q/%v", string(data), err)
	}
	if output.Data["hook_response"] != output.Body {
		t.Fatalf("Data = %#v Body = %q", output.Data, output.Body)
	}
	if marker, ok := output.Data["fileChange"].(bool); !ok || !marker {
		t.Fatalf("fileChange marker = %#v", output.Data["fileChange"])
	}
	changes, ok := output.Data["changes"].([]map[string]any)
	if !ok || len(changes) != 1 {
		t.Fatalf("changes = %#v", output.Data["changes"])
	}
	kind, ok := changes[0]["kind"].(map[string]any)
	if !ok || kind["type"] != "add" || changes[0]["path"] != filepath.Join(dir, "hello.txt") || changes[0]["diff"] != "hello\n" {
		t.Fatalf("change = %#v", changes[0])
	}
	applied, ok := output.Data["appliedChanges"].([]map[string]any)
	if !ok || len(applied) != 1 || applied[0]["kind"] != "add" || applied[0]["newContent"] != "hello\n" {
		t.Fatalf("appliedChanges = %#v", output.Data["appliedChanges"])
	}
}

func TestApplyPatchExecutorWorkspacePolicyProtectsMetadataLikeRust(t *testing.T) {
	cwd := t.TempDir()
	for _, directory := range []string{".git", ".agents", ".codex"} {
		if err := os.MkdirAll(filepath.Join(cwd, directory), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", directory, err)
		}
	}
	policy := sandbox.NewWorkspaceWritePolicy()
	policy.ExcludeTmpdirEnvVar = true
	policy.ExcludeSlashTmp = true
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: cwd, SandboxPolicy: policy})

	allowed, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "workspace-root-allowed-patch",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: "*** Begin Patch\n*** Add File: allowed.txt\n+workspace root patch access\n*** End Patch\n"},
	})
	if err != nil || allowed == nil || !allowed.Success {
		t.Fatalf("allowed patch = %#v, %v", allowed, err)
	}
	assertApplyPatchFile(t, filepath.Join(cwd, "allowed.txt"), "workspace root patch access\n")

	for _, directory := range []string{".git", ".agents", ".codex"} {
		t.Run(directory, func(t *testing.T) {
			target := filepath.Join(cwd, directory, "protected.txt")
			patch := fmt.Sprintf("*** Begin Patch\n*** Add File: %s/protected.txt\n+metadata write\n*** End Patch\n", directory)
			output, err := executor.Execute(context.Background(), &Invocation{
				CallID:   "workspace-root-protected-" + directory,
				ToolName: PlainName(DefaultApplyPatchToolName),
				Payload:  Payload{Kind: PayloadCustom, Input: patch},
			})
			if err != nil || output == nil || output.Success || !strings.Contains(output.Body, "outside of the project") {
				t.Fatalf("protected patch output = %#v, %v", output, err)
			}
			assertApplyPatchMissing(t, target)
		})
	}
}

func TestApplyPatchExecutorWorkspacePolicyRejectsOutsideRootLikeRust(t *testing.T) {
	parent := t.TempDir()
	cwd := filepath.Join(parent, "workspace")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	policy := sandbox.NewWorkspaceWritePolicy()
	policy.ExcludeTmpdirEnvVar = true
	policy.ExcludeSlashTmp = true
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: cwd, SandboxPolicy: policy})
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "workspace-root-outside-patch",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: "*** Begin Patch\n*** Add File: ../outside.txt\n+outside\n*** End Patch\n"},
	})
	if err != nil || output == nil || output.Success || !strings.Contains(output.Body, "outside of the project") {
		t.Fatalf("outside patch output = %#v, %v", output, err)
	}
	assertApplyPatchMissing(t, filepath.Join(parent, "outside.txt"))
}

func TestApplyPatchExecutorCanonicalProfileWinsOverLegacyPolicyLikeRust(t *testing.T) {
	cwd := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.ExcludeTmpdirEnvVar = true
	profile.SandboxPolicy.ExcludeSlashTmp = true
	policy := sandbox.NewDangerFullAccessPolicy()
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD: cwd, PermissionProfile: &profile, SandboxPolicy: policy,
	})
	patch := "*** Begin Patch\n*** Add File: " + filepath.ToSlash(outside) + "\n+outside\n*** End Patch"
	output, err := executor.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadCustom, Input: patch}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || output.Success || !strings.Contains(output.Body, "outside of the project workspace roots") {
		t.Fatalf("output = %#v", output)
	}
}

func TestApplyPatchExecutorIncludesDeleteContentInFileChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: dir})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-delete",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload: Payload{Kind: PayloadCustom, Input: `*** Begin Patch
*** Delete File: old.txt
*** End Patch`},
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	changes, ok := output.Data["changes"].([]map[string]any)
	if !ok || len(changes) != 1 {
		t.Fatalf("changes = %#v", output.Data["changes"])
	}
	kind, ok := changes[0]["kind"].(map[string]any)
	if !ok || kind["type"] != "delete" || changes[0]["diff"] != "old\n" {
		t.Fatalf("change = %#v", changes[0])
	}
	applied, ok := output.Data["appliedChanges"].([]map[string]any)
	if !ok || len(applied) != 1 || applied[0]["kind"] != "delete" || applied[0]["oldContent"] != "old\n" {
		t.Fatalf("appliedChanges = %#v", output.Data["appliedChanges"])
	}
}

func TestApplyPatchExecutorApplicationFailureKeepsFileChangeData(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "blocker"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: cwd})
	invocation := &Invocation{
		CallID:   "call-apply-failed",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: "*** Begin Patch\n*** Add File: blocker/child.txt\n+content\n*** End Patch\n"},
	}
	output, err := executor.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || output.Success || !strings.Contains(output.Error, "apply_patch failed:") {
		t.Fatalf("failed output = %#v", output)
	}
	if output.Data["fileChange"] != true || output.Data["status"] != "failed" {
		t.Fatalf("failed file change data = %#v", output.Data)
	}
	changes, ok := output.Data["changes"].([]map[string]any)
	if !ok || len(changes) != 1 || changes[0]["path"] != filepath.Join(cwd, "blocker", "child.txt") {
		t.Fatalf("failed file changes = %#v", output.Data["changes"])
	}
}

func TestApplyPatchExecutorAcceptsAbsolutePathLikeRust(t *testing.T) {
	cwd := t.TempDir()
	external := t.TempDir()
	target := filepath.Join(external, "absolute.txt")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: cwd})
	patch := "*** Begin Patch\n*** Update File: " + target + "\n@@\n-before\n+after\n*** End Patch"
	output, err := executor.Execute(context.Background(), &Invocation{CallID: "absolute", ToolName: PlainName(DefaultApplyPatchToolName), Payload: Payload{Kind: PayloadCustom, Input: patch}})
	if err != nil || output == nil || !output.Success {
		t.Fatalf("Execute() = %#v, %v", output, err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "after\n" {
		t.Fatalf("target = %q, %v", data, err)
	}
}

func TestApplyPatchExecutorRejectsInvalidPatchWithGrammarError(t *testing.T) {
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: t.TempDir()})
	_, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-bad-patch",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: `*** Begin Patch`},
	})
	if err == nil || !strings.Contains(err.Error(), "apply_patch verification failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyPatchExecutorRejectsEmptyPatch(t *testing.T) {
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: t.TempDir()})
	_, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-empty-patch",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: `  `},
	})
	if err == nil || !strings.Contains(err.Error(), "requires a patch body") {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyPatchExecutorReportsApplyError(t *testing.T) {
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: t.TempDir()})
	_, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-apply-error",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload: Payload{Kind: PayloadCustom, Input: `*** Begin Patch
*** Delete File: missing.txt
*** End Patch`},
	})
	if err == nil || !strings.Contains(err.Error(), "apply_patch verification failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyPatchExecutorPrePostHookPayload(t *testing.T) {
	executor := NewApplyPatchExecutor(nil)
	patch := `*** Begin Patch
*** Add File: hook.txt
+hook
*** End Patch`
	invocation := &Invocation{
		CallID:   "call-apply",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: patch},
	}

	pre, ok := executor.PreToolUsePayload(invocation)
	if !ok {
		t.Fatal("PreToolUsePayload() ok = false")
	}
	if pre.ToolName == nil || pre.ToolName.Name != "apply_patch" {
		t.Fatalf("pre.ToolName = %#v", pre.ToolName)
	}
	if len(pre.ToolName.MatcherAliases) != 2 || pre.ToolName.MatcherAliases[0] != "Write" || pre.ToolName.MatcherAliases[1] != "Edit" {
		t.Fatalf("aliases = %#v", pre.ToolName.MatcherAliases)
	}
	if input, ok := pre.ToolInput.(map[string]any); !ok || input["command"] != patch {
		t.Fatalf("pre.ToolInput = %#v", pre.ToolInput)
	}

	post, ok := executor.PostToolUsePayload(invocation, &Output{CallID: "call-apply", Body: "Success"})
	if !ok {
		t.Fatal("PostToolUsePayload() ok = false")
	}
	if post.ToolName == nil || post.ToolName.Name != "apply_patch" || post.ToolUseID != "call-apply" {
		t.Fatalf("post = %#v", post)
	}
	if post.ToolResponse != "Success" {
		t.Fatalf("ToolResponse = %#v", post.ToolResponse)
	}
}

func TestApplyPatchExecutorUpdatedHookInputRewritesCustomPayload(t *testing.T) {
	executor := NewApplyPatchExecutor(nil)
	updated, err := executor.WithUpdatedHookInput(&Invocation{
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: "old"},
	}, map[string]any{"command": "new patch"})
	if err != nil {
		t.Fatalf("WithUpdatedHookInput() error = %v", err)
	}
	if updated.Payload.Input != "new patch" {
		t.Fatalf("updated payload = %#v", updated.Payload)
	}

	_, err = executor.WithUpdatedHookInput(&Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{}`},
	}, map[string]any{"command": "new"})
	if err == nil || !strings.Contains(err.Error(), "unsupported apply_patch payload") {
		t.Fatalf("error = %v", err)
	}
}

func TestRegisterApplyPatchHandler(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterApplyPatchHandler(registry, &ApplyPatchExecutorOptions{CWD: t.TempDir()}); err != nil {
		t.Fatalf("RegisterApplyPatchHandler() error = %v", err)
	}
	spec, ok := registry.Spec(PlainName(DefaultApplyPatchToolName))
	if !ok {
		t.Fatal("apply_patch handler not registered")
	}
	if spec.Freeform == nil || spec.Freeform.Syntax != "lark" || !strings.Contains(spec.Freeform.Definition, "*** Begin Patch") {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestApplyPatchExecutorAcceptsFunctionWrappedPatchPayload(t *testing.T) {
	for _, arguments := range []string{
		`{"patch":"*** Begin Patch\n*** Add File: wrapped.txt\n+wrapped\n*** End Patch"}`,
		`{"input":"*** Begin Patch\n*** Add File: wrapped.txt\n+wrapped\n*** End Patch"}`,
		`{"command":"*** Begin Patch\n*** Add File: wrapped.txt\n+wrapped\n*** End Patch"}`,
		`"*** Begin Patch\n*** Add File: wrapped.txt\n+wrapped\n*** End Patch"`,
		`prefix *** Begin Patch
*** Add File: wrapped.txt
+wrapped
*** End Patch`,
	} {
		cwd := t.TempDir()
		executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: cwd})
		output, err := executor.Execute(context.Background(), &Invocation{
			CallID:   "call-wrapped",
			ToolName: PlainName(DefaultApplyPatchToolName),
			Payload:  Payload{Kind: PayloadFunction, Arguments: arguments},
		})
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", arguments, err)
		}
		if output == nil || !output.Success {
			t.Fatalf("Execute(%s) output = %#v", arguments, output)
		}
		data, err := os.ReadFile(filepath.Join(cwd, "wrapped.txt"))
		if err != nil || string(data) != "wrapped\n" {
			t.Fatalf("wrapped file = %q, %v", data, err)
		}
	}
}

func TestApplyPatchExecutorComplexChangeMatrix(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "folder with spaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"modify.txt":                    "old\n",
		"delete.txt":                    "remove me\n",
		"folder with spaces/source.txt": "move old\n",
		"unicode-涓枃.txt":               "unicode old\n",
	} {
		path := filepath.Join(cwd, filepath.FromSlash(name))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	patch := `*** Begin Patch
*** Add File: added.txt
+added
*** Update File: modify.txt
@@
-old
+new
*** Delete File: delete.txt
*** Update File: folder with spaces/source.txt
*** Move to: folder with spaces/moved.txt
@@
-move old
+move new
*** Update File: unicode-涓枃.txt
@@
-unicode old
+unicode new
*** End Patch`
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: cwd})
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID: "call-complex", ToolName: PlainName(DefaultApplyPatchToolName),
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"patch":` + strconv.Quote(patch) + `}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	assertApplyPatchFile(t, filepath.Join(cwd, "added.txt"), "added\n")
	assertApplyPatchFile(t, filepath.Join(cwd, "modify.txt"), "new\n")
	assertApplyPatchMissing(t, filepath.Join(cwd, "delete.txt"))
	assertApplyPatchMissing(t, filepath.Join(cwd, "folder with spaces", "source.txt"))
	assertApplyPatchFile(t, filepath.Join(cwd, "folder with spaces", "moved.txt"), "move new\n")
	assertApplyPatchFile(t, filepath.Join(cwd, "unicode-涓枃.txt"), "unicode new\n")
	changes, ok := output.Data["changes"].([]map[string]any)
	if !ok || len(changes) != 5 {
		t.Fatalf("changes = %#v", output.Data["changes"])
	}
	moveKind, _ := changes[3]["kind"].(map[string]any)
	if moveKind["type"] != "update" || moveKind["move_path"] != filepath.Join(cwd, "folder with spaces", "moved.txt") {
		t.Fatalf("move change = %#v", changes[3])
	}
}

func TestApplyPatchExecutorInvalidMatrixDoesNotMutateWorkspace(t *testing.T) {
	tests := []struct {
		name              string
		patch             string
		verificationError bool
	}{
		{name: "missing_end", patch: "*** Begin Patch\n*** Add File: bad.txt\n+bad"},
		{name: "delete_missing", patch: "*** Begin Patch\n*** Delete File: absent.txt\n*** End Patch"},
		{name: "context_mismatch", patch: "*** Begin Patch\n*** Update File: keep.txt\n@@\n-not-present\n+changed\n*** End Patch", verificationError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cwd := t.TempDir()
			if err := os.WriteFile(filepath.Join(cwd, "keep.txt"), []byte("keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: cwd})
			output, err := executor.Execute(context.Background(), &Invocation{
				CallID: "call-invalid", ToolName: PlainName(DefaultApplyPatchToolName),
				Payload: Payload{Kind: PayloadCustom, Input: test.patch},
			})
			if err == nil {
				t.Fatalf("Execute() = %#v, nil; want verification error", output)
			}
			if test.verificationError && !strings.Contains(err.Error(), "failed to find expected lines") {
				t.Fatalf("Execute() error = %v; want missing-context verification error", err)
			}
			assertApplyPatchFile(t, filepath.Join(cwd, "keep.txt"), "keep\n")
			assertApplyPatchMissing(t, filepath.Join(cwd, "bad.txt"))
		})
	}
}

func assertApplyPatchFile(t *testing.T, path string, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != expected {
		t.Fatalf("file %s = %q, %v; want %q", path, data, err, expected)
	}
}

func assertApplyPatchMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path %s exists or stat error = %v", path, err)
	}
}

func TestApplyPatchExecutorPreserveLineEndingsOption(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "crlf.txt")
	run := func(executor *ApplyPatchExecutor) string {
		t.Helper()
		if err := os.WriteFile(target, []byte("before\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		patch := "*** Begin Patch\n*** Update File: crlf.txt\n@@\n-before\n+after\n*** End Patch"
		output, err := executor.Execute(context.Background(), &Invocation{
			CallID:   "call-apply-crlf",
			ToolName: PlainName(DefaultApplyPatchToolName),
			Payload:  Payload{Kind: PayloadCustom, Input: patch},
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !output.Success {
			t.Fatalf("output = %#v", output)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	if got := run(NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: dir})); got != "after\n" {
		t.Fatalf("default mode target = %q, want %q", got, "after\n")
	}
	if got := run(NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: dir, PreserveLineEndings: true})); got != "after\r\n" {
		t.Fatalf("preserve mode target = %q, want %q", got, "after\r\n")
	}
}
