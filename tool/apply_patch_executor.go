package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"codex_go/applypatch"
	"codex_go/sandbox"
)

const DefaultApplyPatchToolName = "apply_patch"

type ApplyPatchExecutorOptions struct {
	CWD                  string
	IncludeEnvironmentID bool
	ToolName             ToolName
	Approval             ApplyPatchApprovalFunc
	PermissionProfile    *sandbox.PermissionProfile
	SandboxPolicy        *sandbox.SandboxPolicy
}

type ApplyPatchExecutor struct {
	cwdPath              string
	includeEnvironmentID bool
	toolName             ToolName
	approval             ApplyPatchApprovalFunc
	permissionProfile    *sandbox.PermissionProfile
	sandboxPolicy        *sandbox.SandboxPolicy
}

type ApplyPatchApprovalDecision struct {
	Approved     bool
	AllowSession bool
}

type ApplyPatchApprovalRequest struct {
	Action     *applypatch.Action
	Changes    []map[string]any
	CWD        string
	Invocation *Invocation
}

type ApplyPatchApprovalFunc func(context.Context, *ApplyPatchApprovalRequest) (ApplyPatchApprovalDecision, error)

func NewApplyPatchExecutor(options *ApplyPatchExecutorOptions) *ApplyPatchExecutor {
	executor := &ApplyPatchExecutor{toolName: PlainName(DefaultApplyPatchToolName)}
	if options == nil {
		return executor
	}
	executor.cwdPath = options.CWD
	executor.includeEnvironmentID = options.IncludeEnvironmentID
	executor.approval = options.Approval
	executor.permissionProfile = options.PermissionProfile
	executor.sandboxPolicy = options.SandboxPolicy
	if options.ToolName.Key() != "" {
		executor.toolName = options.ToolName
	}
	return executor
}

func RegisterApplyPatchHandler(registry *Registry, options *ApplyPatchExecutorOptions) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", ErrToolInvalidCall)
	}
	return registry.Register(NewApplyPatchExecutor(options))
}

func (e *ApplyPatchExecutor) Spec() Spec {
	spec := applypatch.CreateFreeformTool(e != nil && e.includeEnvironmentID)
	name := PlainName(DefaultApplyPatchToolName)
	if e != nil && e.toolName.Key() != "" {
		name = e.toolName
	}
	return Spec{
		Name:        name,
		Description: spec.Description,
		Freeform: &FreeformSpec{
			Syntax:     spec.Format.Syntax,
			Definition: spec.Format.Definition,
		},
		Parallel: false,
	}
}

func (e *ApplyPatchExecutor) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	_ = ctx
	patch, ok := applyPatchPayloadCommand(invocation)
	if !ok {
		return nil, RespondToModel(applyPatchUnsupportedPayloadMessage(invocation))
	}
	if strings.TrimSpace(patch) == "" {
		return nil, RespondToModel("apply_patch requires a patch body")
	}
	action, err := applypatch.Parse(patch)
	if err != nil {
		return nil, RespondToModel("apply_patch verification failed: " + applypatch.FormatError(err))
	}
	applyOptions := &applypatch.ApplyOptions{CWD: e.cwd()}
	if deniedPath := applyPatchDeniedPath(action, e.cwd(), e.permissionProfile, e.sandboxPolicy); deniedPath != "" {
		body := fmt.Sprintf("apply_patch verification failed: path %s is outside of the project workspace roots", deniedPath)
		sandbox.RecordFileSystemPolicyViolation(sandbox.PlatformSandboxType(), deniedPath, body)
		return &Output{
			Success:    false,
			Body:       body,
			Error:      body,
			Data:       applyPatchApprovalData("failed", applyPatchFileChanges(action, e.cwd())),
			LogPreview: shellLogPreview(body),
		}, nil
	}
	if err := action.FillDeleteContent(applyOptions); err != nil {
		return nil, RespondToModel("apply_patch verification failed: " + applypatch.FormatError(err))
	}
	if err := action.Verify(applyOptions); err != nil {
		return nil, RespondToModel("apply_patch verification failed: " + applypatch.FormatError(err))
	}
	changes := applyPatchFileChanges(action, e.cwd())
	if e.approval != nil {
		decision, approvalErr := e.approval(ctx, &ApplyPatchApprovalRequest{
			Action:     action,
			Changes:    changes,
			CWD:        e.cwd(),
			Invocation: invocation,
		})
		if approvalErr != nil {
			body := "Patch approval request failed: " + approvalErr.Error()
			return &Output{
				Success:    false,
				Body:       body,
				Error:      body,
				Data:       applyPatchApprovalData("failed", changes),
				LogPreview: shellLogPreview(body),
			}, nil
		}
		if !decision.Approved {
			body := "Patch approval denied before applying changes."
			return &Output{
				Success:    false,
				Body:       body,
				Error:      body,
				Data:       applyPatchApprovalData("declined", changes),
				LogPreview: shellLogPreview(body),
			}, nil
		}
	}
	result, err := action.ApplyVerified(applyOptions)
	if err != nil {
		body := "apply_patch failed: " + applypatch.FormatError(err)
		return &Output{
			Success:    false,
			Body:       body,
			Error:      body,
			Data:       applyPatchApprovalData("failed", changes),
			LogPreview: shellLogPreview(body),
		}, nil
	}
	body := result.Summary()
	return &Output{
		Success:    true,
		Body:       body,
		Data:       applyPatchResultData(result, action, e.cwd()),
		LogPreview: shellLogPreview(body),
	}, nil
}

func applyPatchDeniedPath(action *applypatch.Action, cwd string, profile *sandbox.PermissionProfile, legacyPolicy *sandbox.SandboxPolicy) string {
	policy := legacyPolicy
	if profile != nil {
		policy = profile.LegacySandboxPolicy()
	}
	if action == nil || policy == nil || policy.HasFullDiskWriteAccess() {
		return ""
	}
	roots := policy.GetWritableRootsWithCWD(cwd)
	for _, path := range action.FilePaths() {
		resolved := filepath.Clean(path)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(cwd, resolved)
		}
		resolved, err := filepath.Abs(resolved)
		if err != nil {
			return path
		}
		writable := false
		for i := range roots {
			if roots[i].IsPathWritable(resolved) {
				writable = true
				break
			}
		}
		if !writable {
			return path
		}
	}
	return ""
}

func ApplyPatchChanges(invocation *Invocation, cwd string) []map[string]any {
	patch, ok := applyPatchPayloadCommand(invocation)
	if !ok || strings.TrimSpace(patch) == "" {
		return nil
	}
	action, err := applypatch.Parse(patch)
	if err != nil {
		return nil
	}
	return applyPatchFileChanges(action, cwd)
}

func (e *ApplyPatchExecutor) cwd() string {
	if e == nil {
		return ""
	}
	return e.cwdPath
}

func (e *ApplyPatchExecutor) PreToolUsePayload(invocation *Invocation) (*PreToolUsePayload, bool) {
	command, ok := applyPatchPayloadCommand(invocation)
	if !ok {
		return nil, false
	}
	return &PreToolUsePayload{
		ToolName:  applyPatchHookToolName(),
		ToolInput: map[string]any{"command": command},
	}, true
}

func (e *ApplyPatchExecutor) PostToolUsePayload(invocation *Invocation, output *Output) (*PostToolUsePayload, bool) {
	command, ok := applyPatchPayloadCommand(invocation)
	if !ok || output == nil {
		return nil, false
	}
	response := output.Body
	if value, ok := output.Data["hook_response"].(string); ok {
		response = value
	}
	return &PostToolUsePayload{
		ToolName:     applyPatchHookToolName(),
		ToolUseID:    firstNonEmptyString(output.CallID, invocation.CallID),
		ToolInput:    map[string]any{"command": command},
		ToolResponse: response,
	}, true
}

func (e *ApplyPatchExecutor) WithUpdatedHookInput(invocation *Invocation, updatedInput any) (*Invocation, error) {
	command, err := updatedHookCommand(updatedInput)
	if err != nil {
		return nil, err
	}
	if invocation == nil || invocation.Payload.Kind != PayloadCustom {
		return nil, RespondToModel("hook input rewrite received unsupported apply_patch payload")
	}
	updated := cloneInvocation(invocation)
	updated.Payload.Input = command
	return updated, nil
}

func applyPatchPayloadCommand(invocation *Invocation) (string, bool) {
	if invocation == nil {
		return "", false
	}
	switch invocation.Payload.Kind {
	case PayloadCustom:
		return invocation.Payload.Input, true
	case PayloadFunction:
		arguments := strings.TrimSpace(invocation.Payload.Arguments)
		if arguments == "" {
			return "", false
		}
		if strings.HasPrefix(arguments, "*** Begin Patch") {
			return arguments, true
		}
		if !json.Valid([]byte(arguments)) {
			if patch := embeddedApplyPatch(arguments); patch != "" {
				return patch, true
			}
		}
		var wrapped map[string]any
		if err := json.Unmarshal([]byte(arguments), &wrapped); err == nil {
			for _, key := range []string{"patch", "input", "command"} {
				if value, ok := wrapped[key].(string); ok {
					if patch := embeddedApplyPatch(value); patch != "" {
						return patch, true
					}
				}
			}
		}
		var quoted string
		if err := json.Unmarshal([]byte(arguments), &quoted); err == nil {
			if patch := embeddedApplyPatch(quoted); patch != "" {
				return patch, true
			}
		}
		if patch := embeddedApplyPatch(arguments); patch != "" && strings.Contains(patch, "\n") {
			return patch, true
		}
	}
	return "", false
}

func embeddedApplyPatch(value string) string {
	value = strings.TrimSpace(value)
	if start := strings.Index(value, "*** Begin Patch"); start >= 0 {
		return value[start:]
	}
	return ""
}

func applyPatchUnsupportedPayloadMessage(invocation *Invocation) string {
	if invocation == nil {
		return "apply_patch handler received nil invocation"
	}
	return fmt.Sprintf(
		"apply_patch handler received unsupported payload (kind=%q input_bytes=%d arguments_bytes=%d)",
		invocation.Payload.Kind,
		len(invocation.Payload.Input),
		len(invocation.Payload.Arguments),
	)
}

func applyPatchHookToolName() *HookToolName {
	return &HookToolName{Name: DefaultApplyPatchToolName, MatcherAliases: []string{"Write", "Edit"}}
}

func applyPatchResultData(result *applypatch.ApplyResult, action *applypatch.Action, cwd string) map[string]any {
	data := map[string]any{"hook_response": "", "fileChange": true, "status": "completed"}
	if result == nil {
		return data
	}
	updated := make([]map[string]any, 0, len(result.Updated))
	for _, file := range result.Updated {
		updated = append(updated, map[string]any{
			"kind": file.Kind,
			"path": file.Path,
		})
	}
	summary := result.Summary()
	data["updated"] = updated
	data["changes"] = applyPatchFileChanges(action, cwd)
	data["appliedChanges"] = applyPatchAppliedChanges(result)
	data["hook_response"] = summary
	data["stdout"] = summary
	data["stderr"] = ""
	return data
}

func applyPatchApprovalData(status string, changes []map[string]any) map[string]any {
	return map[string]any{
		"hook_response": "",
		"fileChange":    true,
		"status":        status,
		"changes":       changes,
		"stdout":        "",
	}
}

func applyPatchAppliedChanges(result *applypatch.ApplyResult) []map[string]any {
	if result == nil {
		return []map[string]any{}
	}
	changes := make([]map[string]any, 0, len(result.Changes))
	for index := range result.Changes {
		change := &result.Changes[index]
		entry := map[string]any{
			"kind":       string(change.Kind),
			"path":       change.Path,
			"oldContent": change.OldContent,
			"newContent": change.NewContent,
		}
		if strings.TrimSpace(change.MovePath) != "" {
			entry["movePath"] = change.MovePath
		}
		if change.OverwrittenContent != nil {
			entry["overwrittenContent"] = *change.OverwrittenContent
		}
		changes = append(changes, entry)
	}
	return changes
}

func applyPatchFileChanges(action *applypatch.Action, cwd string) []map[string]any {
	if action == nil {
		return []map[string]any{}
	}
	changes := make([]map[string]any, 0, len(action.Hunks))
	for _, hunk := range action.Hunks {
		kind := applyPatchChangeKindData(&hunk)
		if movePath, ok := kind["move_path"].(string); ok && strings.TrimSpace(movePath) != "" {
			kind["move_path"] = applyPatchDisplayPath(cwd, movePath)
		}
		change := map[string]any{
			"path": applyPatchDisplayPath(cwd, hunk.Path),
			"kind": kind,
			"diff": applyPatchChangeDiff(&hunk),
		}
		changes = append(changes, change)
	}
	return changes
}

func applyPatchDisplayPath(cwd string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	base := strings.TrimSpace(cwd)
	if base == "" {
		base = "."
	}
	abs, err := filepath.Abs(filepath.Join(base, path))
	if err != nil {
		return path
	}
	return filepath.Clean(abs)
}

func applyPatchChangeKindData(change *applypatch.Change) map[string]any {
	if change == nil {
		return map[string]any{"type": string(applypatch.ChangeUpdate), "move_path": nil}
	}
	switch change.Kind {
	case applypatch.ChangeAdd, applypatch.ChangeDelete:
		return map[string]any{"type": string(change.Kind)}
	default:
		kind := map[string]any{"type": string(applypatch.ChangeUpdate), "move_path": nil}
		if strings.TrimSpace(change.MovePath) != "" {
			kind["move_path"] = change.MovePath
		}
		return kind
	}
}

func applyPatchChangeDiff(change *applypatch.Change) string {
	if change == nil {
		return ""
	}
	switch change.Kind {
	case applypatch.ChangeAdd:
		return change.Content
	case applypatch.ChangeDelete:
		return change.Content
	default:
		diff := change.UnifiedDiff
		if strings.TrimSpace(change.MovePath) != "" {
			return fmt.Sprintf("%s\n\nMoved to: %s", strings.TrimRight(diff, "\n"), change.MovePath)
		}
		return diff
	}
}

func applyPatchErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("apply_patch %s error: %s", applypatch.ClassifyError(err), applypatch.FormatError(err))
}

var _ Executor = (*ApplyPatchExecutor)(nil)
var _ PreToolUsePayloadProvider = (*ApplyPatchExecutor)(nil)
var _ PostToolUsePayloadProvider = (*ApplyPatchExecutor)(nil)
var _ HookInputUpdater = (*ApplyPatchExecutor)(nil)
