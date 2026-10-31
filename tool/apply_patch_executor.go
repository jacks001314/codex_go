package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex_go/applypatch"
	"codex_go/execserver"
	"codex_go/safety"
	"codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
	"codex_go/utils"
)

const DefaultApplyPatchToolName = "apply_patch"

// Rust #50962: apply_patch reports this legacy message when no usable
// environment exists and `stable_environment_tools` is off.
const applyPatchUnavailableMessage = "apply_patch is unavailable in this session"

type ApplyPatchExecutorOptions struct {
	CWD                  string
	IncludeEnvironmentID bool
	ToolName             ToolName
	Approval             ApplyPatchApprovalFunc
	// DecisionSink receives the config-approved decisions for patches that need
	// no approval (Rust's skipped approval requirement).
	DecisionSink      ToolDecisionSink
	PermissionProfile *sandbox.PermissionProfile
	SandboxPolicy     *sandbox.SandboxPolicy
	// AllowedSymlinkedCodexHome is the host-resolved macOS Seatbelt opt-out
	// (Rust #42716). It guards local patches, which write in-process instead of
	// launching the filesystem helper.
	AllowedSymlinkedCodexHome string
	// EnvironmentCheck carries the turn readiness facts (Rust #50962) so a
	// patch-level environment id is resolved before verification.
	EnvironmentCheck *UnifiedExecEnvironmentCheck
	// EnvironmentFileSystems resolves the selected environment's filesystem so
	// the patch is verified and applied through that environment (Rust #20647).
	EnvironmentFileSystems EnvironmentFileSystemProvider
	// GrantedPermissions resolves permissions already approved for this turn and
	// environment (Rust #41928).
	GrantedPermissions func(turnID, environmentID string) *sandbox.AdditionalPermissionProfile
}

type ApplyPatchExecutor struct {
	cwdPath                   string
	includeEnvironmentID      bool
	toolName                  ToolName
	approval                  ApplyPatchApprovalFunc
	decisionSink              ToolDecisionSink
	permissionProfile         *sandbox.PermissionProfile
	sandboxPolicy             *sandbox.SandboxPolicy
	allowedSymlinkedCodexHome string
	environmentCheck          *UnifiedExecEnvironmentCheck
	environmentFileSystems    EnvironmentFileSystemProvider
	grantedPermissions        func(turnID, environmentID string) *sandbox.AdditionalPermissionProfile
}

type ApplyPatchApprovalDecision struct {
	Approved     bool
	AllowSession bool
	// DenyReason carries the rejection message when a policy layer denied the
	// patch, e.g. Rust's PermissionRequest hook denial
	// (ToolError::Rejected(message)).
	DenyReason string
}

type ApplyPatchApprovalRequest struct {
	Action  *applypatch.Action
	Changes []map[string]any
	CWD     string
	// EnvironmentID is the resolved environment the patch executes against
	// (Rust ApplyPatchRequest.turn_environment.selection.environment_id). The
	// implicit local environment is reported as `local`.
	EnvironmentID string
	// Patch is the raw patch body the approval applies, reported to
	// PermissionRequest hooks as `command` (Rust ApprovalAction::ApplyPatch's
	// permission_request_payload).
	Patch      string
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
	executor.decisionSink = options.DecisionSink
	executor.permissionProfile = options.PermissionProfile
	executor.sandboxPolicy = options.SandboxPolicy
	executor.allowedSymlinkedCodexHome = options.AllowedSymlinkedCodexHome
	executor.environmentCheck = options.EnvironmentCheck
	executor.environmentFileSystems = options.EnvironmentFileSystems
	executor.grantedPermissions = options.GrantedPermissions
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
	var environmentCheck *UnifiedExecEnvironmentCheck
	var environmentFileSystems EnvironmentFileSystemProvider
	if e != nil {
		environmentCheck = e.environmentCheck
		environmentFileSystems = e.environmentFileSystems
	}
	// Rust #20647: the patch resolves to a turn environment and executes on that
	// environment's filesystem, so a remote selection patches the executor's
	// files instead of the host process filesystem.
	resolvedEnvironmentID, err := resolveToolEnvironmentID(environmentCheck, environmentFileSystems, action.EnvironmentID, applyPatchUnavailableMessage)
	if err != nil {
		return nil, RespondToModel(err.Error())
	}
	fileSystem, err := ResolveToolEnvironmentFileSystem(environmentCheck, environmentFileSystems, action.EnvironmentID, applyPatchUnavailableMessage, e.cwd())
	if err != nil {
		return nil, RespondToModel(err.Error())
	}
	// The patch's working directory is the selected environment's, so the
	// resolved paths (and the environment-relative requests built from them)
	// address that environment's tree.
	cwd := e.cwd()
	if resolvedCWD := strings.TrimSpace(fileSystem.CWD()); resolvedCWD != "" {
		cwd = resolvedCWD
	}
	permissionProfile := e.permissionProfile
	sandboxPolicy := e.sandboxPolicy
	if e.grantedPermissions != nil {
		turnID := strings.TrimSpace(invocationContextString(invocation, "turn_id"))
		environmentID := strings.TrimSpace(action.EnvironmentID)
		if granted := e.grantedPermissions(turnID, environmentID); !granted.IsEmpty() {
			merged, mergeErr := sandbox.PermissionProfileWithAdditionalPermissionsForConvention(
				permissionProfile,
				granted,
				PathConventionForPath(cwd),
			)
			if mergeErr != nil {
				return nil, RespondToModel("apply_patch verification failed: " + mergeErr.Error())
			}
			permissionProfile = merged
			sandboxPolicy = merged.LegacySandboxPolicy()
		}
	}
	applyOptions := &applypatch.ApplyOptions{CWD: cwd, FS: newEnvironmentApplyPatchFileSystem(ctx, fileSystem)}
	// Rust #39659: when an otherwise-required sandbox is bypassed, disable
	// symlink traversal so a verified path cannot be swapped for a link to a
	// different file.
	applyOptions.NoFollowSymlinks = applyPatchSandboxBypassed(permissionProfile, sandboxPolicy)
	if err := action.FillDeleteContent(applyOptions); err != nil {
		return nil, RespondToModel("apply_patch verification failed: " + applypatch.FormatError(err))
	}
	if err := action.Verify(applyOptions); err != nil {
		return nil, RespondToModel("apply_patch verification failed: " + applypatch.FormatError(err))
	}
	changes := applyPatchFileChanges(action, cwd)
	deniedOutput := func(deniedPath string) *Output {
		body := fmt.Sprintf("apply_patch verification failed: path %s is outside of the project workspace roots", deniedPath)
		sandbox.RecordFileSystemPolicyViolation(sandbox.PlatformSandboxType(), deniedPath, body)
		return &Output{
			Success:    false,
			Body:       body,
			Error:      body,
			Data:       applyPatchApprovalData("failed", changes),
			LogPreview: shellLogPreview(body),
		}
	}
	approvalRequest := &ApplyPatchApprovalRequest{
		Action:        action,
		Changes:       changes,
		CWD:           cwd,
		EnvironmentID: firstNonEmptyString(resolvedEnvironmentID, execserver.LocalEnvironmentID),
		Patch:         patch,
		Invocation:    invocation,
	}
	approved := false
	deniedPath := applyPatchDeniedPath(action, cwd, permissionProfile, sandboxPolicy)
	if deniedPath != "" {
		// Rust #42391: an out-of-root patch is a user decision unless approval
		// settings reject sandbox approval. Go has no per-patch sandbox here, so
		// the approval must happen before the write grant is derived.
		if e == nil || e.approval == nil {
			return deniedOutput(deniedPath), nil
		}
		if output, ok := e.requestPatchApproval(ctx, approvalRequest, changes); !ok {
			return output, nil
		}
		approved = true
		granted := applyPatchWritePermissionsForPaths(action, cwd, permissionProfile, sandboxPolicy)
		permissionProfile, sandboxPolicy = applyPatchWithWritePermissions(permissionProfile, sandboxPolicy, granted)
		if deniedPath = applyPatchDeniedPath(action, cwd, permissionProfile, sandboxPolicy); deniedPath != "" {
			return deniedOutput(deniedPath), nil
		}
	}
	_, localFileSystem := fileSystem.(localEnvironmentFileSystem)
	if localFileSystem {
		policy := sandboxPolicy
		if permissionProfile != nil {
			policy = permissionProfile.LegacySandboxPolicy()
		}
		if err := sandbox.ValidateWritableRootsForPolicy(cwd, policy, e.allowedSymlinkedCodexHome); err != nil {
			body := "apply_patch verification failed: " + err.Error()
			return &Output{
				Success:    false,
				Body:       body,
				Error:      body,
				Data:       applyPatchApprovalData("failed", applyPatchFileChanges(action, cwd)),
				LogPreview: shellLogPreview(body),
			}, nil
		}
	}
	// Rust #42391: an executor-managed sandbox is always available, so a patch
	// constrained to its writable paths needs no user approval. Disabled and
	// external profiles intentionally run without an outer sandbox and also
	// auto-approve; other local patches stay on the conservative prompt path
	// because Go has no in-process sandbox around apply_patch.
	skipApproval := !approved && deniedPath == "" && (!localFileSystem || applyPatchRunsWithoutOuterSandbox(permissionProfile))
	if e.approval != nil && !approved && !skipApproval {
		if output, ok := e.requestPatchApproval(ctx, approvalRequest, changes); !ok {
			return output, nil
		}
	} else if e.decisionSink != nil && !approved {
		// Rust's orchestrator reports the config-approved decision for a tool whose
		// approval requirement is skipped (no approval was needed at all).
		toolName := e.toolName
		callID := ""
		if invocation != nil {
			if invocation.ToolName.Key() != "" {
				toolName = invocation.ToolName
			}
			callID = strings.TrimSpace(invocation.CallID)
		}
		e.decisionSink.AutoApproved(toolName, callID)
	}
	result, err := action.ApplyVerified(applyOptions)
	// Rust #51652: record committed AGENTS.md / AGENTS.override.md edits,
	// including the changes committed before a later patch failure.
	recordAgentsMdEditMetrics(result)
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
		Data:       applyPatchResultData(result, action, cwd),
		LogPreview: shellLogPreview(body),
	}, nil
}

// applyPatchRunsWithoutOuterSandbox reports the profiles Rust auto-approves
// without consulting platform sandbox availability.
func applyPatchRunsWithoutOuterSandbox(profile *sandbox.PermissionProfile) bool {
	if profile == nil {
		return false
	}
	if profile.Disabled {
		return true
	}
	policy := profile.LegacySandboxPolicy()
	return policy != nil && policy.Kind == "external-sandbox"
}

// requestPatchApproval runs the configured approval callback and turns a
// callback error or denial into the failed/declined tool output. ok reports
// whether the patch may proceed.
func (e *ApplyPatchExecutor) requestPatchApproval(ctx context.Context, request *ApplyPatchApprovalRequest, changes []map[string]any) (*Output, bool) {
	if e == nil || e.approval == nil {
		return nil, true
	}
	decision, err := e.approval(ctx, request)
	if err != nil {
		body := "Patch approval request failed: " + err.Error()
		return &Output{
			Success:    false,
			Body:       body,
			Error:      body,
			Data:       applyPatchApprovalData("failed", changes),
			LogPreview: shellLogPreview(body),
		}, false
	}
	if decision.Approved {
		return nil, true
	}
	body := "Patch approval denied before applying changes."
	if reason := strings.TrimSpace(decision.DenyReason); reason != "" {
		body = reason
	}
	return &Output{
		Success:    false,
		Body:       body,
		Error:      body,
		Data:       applyPatchApprovalData("declined", changes),
		LogPreview: shellLogPreview(body),
	}, false
}

func applyPatchDeniedPath(action *applypatch.Action, cwd string, profile *sandbox.PermissionProfile, legacyPolicy *sandbox.SandboxPolicy) string {
	policy := legacyPolicy
	if profile != nil {
		policy = profile.LegacySandboxPolicy()
	}
	if action == nil || policy == nil || policy.HasFullDiskWriteAccess() {
		return ""
	}
	// Rust #42391: patch targets can use a different path convention from the
	// Codex host, so host-native conversion can misclassify writable roots.
	// Compare those targets as path URIs under the executor's convention.
	if patchPathUsesExecutorConvention(cwd) {
		return applyPatchDeniedPathForConvention(action, cwd, policy)
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

// patchPathUsesExecutorConvention reports whether the executor cwd is spelled
// as a path URI or with a foreign path convention, in which case host filepath
// operations cannot classify patch targets correctly (Rust #42391).
func patchPathUsesExecutorConvention(cwd string) bool {
	if strings.Contains(cwd, "://") {
		return true
	}
	convention := PathConventionForPath(cwd)
	return convention != "" && convention != utils.NativePathConvention()
}

// applyPatchDeniedPathForConvention classifies patch targets against the
// executor's filesystem policy context. Mirrors Rust #42391: the workspace
// cwd, the policy's writable roots, and the symbolic `/tmp` root are resolved
// under the executor path convention, and workspace metadata names stay
// read-only inside a writable root.
func applyPatchDeniedPathForConvention(action *applypatch.Action, cwd string, policy *sandbox.SandboxPolicy) string {
	paths := action.FilePaths()
	denyFirst := func() string {
		if len(paths) == 0 {
			return ""
		}
		return paths[0]
	}
	cwdURI, ok := utils.InferredPathURI(cwd)
	if !ok || cwdURI == nil {
		return denyFirst()
	}
	if policy.Kind != sandbox.SandboxWorkspaceWrite {
		return denyFirst()
	}
	context := safety.FileSystemPolicyContext{
		CWD:                    cwdURI,
		ExtraWritableRoots:     []*utils.PathURI{cwdURI},
		ProtectedMetadataNames: sandboxpath.DefaultProtectedMetadataNames(),
	}
	// `:slash_tmp` is symbolic only under POSIX semantics (Rust #42391's
	// `has_write_narrowing_entries` convention check). A POSIX executor also
	// owns `/tmp`; the host temp directory is meaningless for a foreign
	// executor, so it is never added here.
	if convention, ok := cwdURI.InferConvention(); ok && convention == utils.ConventionPosix && !policy.ExcludeSlashTmp {
		if tmpURI, err := utils.FromAbsoluteNativePath("/tmp", convention); err == nil {
			context.ExtraWritableRoots = append(context.ExtraWritableRoots, tmpURI)
		}
	}
	filePolicy := safety.FileSystemPolicy{WritableRoots: policy.WritableRoots}
	for _, path := range paths {
		target, ok := context.ResolvePath(path)
		if !ok || !context.CanWritePath(&filePolicy, target) {
			return path
		}
	}
	return ""
}

// applyPatchWritePermissionsForPaths mirrors Rust #42391's
// write_permissions_for_paths for the targets Go can authorize per patch. A
// target already inside a writable root needs no grant; otherwise the patch
// asks for its parent directory, resolved with the executor's path convention,
// so a foreign-convention parent is never reinterpreted by the host.
func applyPatchWritePermissionsForPaths(action *applypatch.Action, cwd string, profile *sandbox.PermissionProfile, legacyPolicy *sandbox.SandboxPolicy) *sandbox.AdditionalPermissionProfile {
	policy := legacyPolicy
	if profile != nil {
		policy = profile.LegacySandboxPolicy()
	}
	if action == nil || policy == nil || policy.HasFullDiskWriteAccess() {
		return nil
	}
	context, filePolicy, ok := applyPatchFileSystemPolicyContext(cwd, policy)
	if !ok {
		return nil
	}
	var writeRoots []string
	seen := map[string]bool{}
	for _, path := range action.FilePaths() {
		target, ok := context.ResolvePath(path)
		if !ok || target == nil || context.CanWritePath(&filePolicy, target) {
			continue
		}
		parent, ok := target.Parent()
		if !ok || parent == nil {
			parent = target
		}
		if context.CanWritePath(&filePolicy, parent) {
			continue
		}
		native := parent.NativePathString()
		if native == "" || seen[native] {
			continue
		}
		seen[native] = true
		writeRoots = append(writeRoots, native)
	}
	sort.Strings(writeRoots)
	if len(writeRoots) == 0 {
		return nil
	}
	return &sandbox.AdditionalPermissionProfile{FileSystem: writeRoots}
}

// applyPatchFileSystemPolicyContext builds the executor-convention policy view
// used to derive per-patch write grants.
func applyPatchFileSystemPolicyContext(cwd string, policy *sandbox.SandboxPolicy) (safety.FileSystemPolicyContext, safety.FileSystemPolicy, bool) {
	if policy == nil || (policy.Kind != sandbox.SandboxWorkspaceWrite && policy.Kind != sandbox.SandboxReadOnly) {
		return safety.FileSystemPolicyContext{}, safety.FileSystemPolicy{}, false
	}
	cwdURI, ok := utils.InferredPathURI(cwd)
	if !ok || cwdURI == nil {
		return safety.FileSystemPolicyContext{}, safety.FileSystemPolicy{}, false
	}
	context := safety.FileSystemPolicyContext{
		CWD:                    cwdURI,
		ProtectedMetadataNames: sandboxpath.DefaultProtectedMetadataNames(),
	}
	if policy.Kind == sandbox.SandboxWorkspaceWrite {
		context.ExtraWritableRoots = []*utils.PathURI{cwdURI}
		convention, hasConvention := cwdURI.InferConvention()
		if hasConvention && convention == utils.ConventionPosix && !policy.ExcludeSlashTmp {
			if tmpURI, err := utils.FromAbsoluteNativePath("/tmp", convention); err == nil {
				context.ExtraWritableRoots = append(context.ExtraWritableRoots, tmpURI)
			}
		}
		if !policy.ExcludeTmpdirEnvVar && hasConvention && convention == utils.NativePathConvention() {
			if tmpdirURI, ok := utils.InferredPathURI(os.TempDir()); ok && tmpdirURI != nil {
				context.ExtraWritableRoots = append(context.ExtraWritableRoots, tmpdirURI)
			}
		}
	}
	return context, safety.FileSystemPolicy{WritableRoots: policy.WritableRoots}, true
}

// applyPatchWithWritePermissions adds the approved patch-parent grants to the
// policy used for this patch. The copy keeps a session's configured policy
// unchanged after the call.
func applyPatchWithWritePermissions(profile *sandbox.PermissionProfile, legacyPolicy *sandbox.SandboxPolicy, permissions *sandbox.AdditionalPermissionProfile) (*sandbox.PermissionProfile, *sandbox.SandboxPolicy) {
	if permissions == nil || len(permissions.FileSystem) == 0 {
		return profile, legacyPolicy
	}
	policy := legacyPolicy
	if profile != nil {
		policy = profile.LegacySandboxPolicy()
	}
	if policy == nil {
		return profile, legacyPolicy
	}
	merged := *policy
	merged.WritableRoots = append(append([]string(nil), policy.WritableRoots...), permissions.FileSystem...)
	if merged.Kind == sandbox.SandboxReadOnly {
		merged.Kind = sandbox.SandboxWorkspaceWrite
		merged.ExcludeTmpdirEnvVar = true
		merged.ExcludeSlashTmp = true
	}
	if profile != nil {
		profileCopy := *profile
		profileCopy.SandboxPolicy = &merged
		return &profileCopy, &merged
	}
	return profile, &merged
}

// applyPatchSandboxBypassed reports whether the patch runs without a
// filesystem sandbox (full disk write access with no deny entries), in which
// case no-follow filesystem operations are required (Rust #39659).
func applyPatchSandboxBypassed(profile *sandbox.PermissionProfile, legacyPolicy *sandbox.SandboxPolicy) bool {
	policy := legacyPolicy
	if profile != nil {
		policy = profile.LegacySandboxPolicy()
	}
	if policy == nil {
		return false
	}
	return policy.HasFullDiskWriteAccess()
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

// HookEnvironmentIDs reports the environment the patch resolves to (Rust
// `Tool::environments_for_input` for apply_patch). An unresolvable patch leaves
// the hook inventory unscoped so the executor can report its own verification
// error.
func (e *ApplyPatchExecutor) HookEnvironmentIDs(invocation *Invocation) []string {
	patch, ok := applyPatchPayloadCommand(invocation)
	if !ok {
		return nil
	}
	action, err := applypatch.Parse(patch)
	if err != nil {
		return nil
	}
	var environmentCheck *UnifiedExecEnvironmentCheck
	var environmentFileSystems EnvironmentFileSystemProvider
	if e != nil {
		environmentCheck = e.environmentCheck
		environmentFileSystems = e.environmentFileSystems
	}
	id, err := resolveToolEnvironmentID(environmentCheck, environmentFileSystems, action.EnvironmentID, applyPatchUnavailableMessage)
	if err != nil {
		return nil
	}
	return []string{firstNonEmptyString(strings.TrimSpace(id), execserver.LocalEnvironmentID)}
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
var _ HookEnvironmentSelector = (*ApplyPatchExecutor)(nil)
var _ HookInputUpdater = (*ApplyPatchExecutor)(nil)
