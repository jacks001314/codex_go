package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestApplyPatchExecutorRejectsInvalidPatchWithGrammarError(t *testing.T) {
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: t.TempDir()})
	_, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-bad-patch",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: `*** Begin Patch`},
	})
	if err == nil || !strings.Contains(err.Error(), "apply_patch grammar error") {
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
	if err == nil || !strings.Contains(err.Error(), "apply_patch apply error") {
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
