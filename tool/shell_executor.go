package tool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"codex_go/envutil"
	"codex_go/execserver"
	"codex_go/network"
	"codex_go/plugin"
	"codex_go/sandbox"
	"codex_go/utils"
)

const (
	DefaultExecCommandToolName        = "exec_command"
	DefaultShellCommandToolName       = "shell_command"
	DefaultExecCommandMaxOutputTokens = 10000
)

type ShellExecutorOptions struct {
	Runner              ShellRunner
	Shell               *Shell
	Validation          ShellValidationOptions
	ToolName            ToolName
	Approval            ShellApprovalFunc
	MaxOutputTokens     *int
	UnifiedExec         *UnifiedExecManager
	UnifiedExecEvents   UnifiedExecEventSink
	UnifiedExecThreadID string
	UnifiedExecTurnID   string
	// SessionID mirrors Rust 97729885d4: the shared root-session ID is exposed
	// to model-reachable shell commands as CODEX_SESSION_ID.
	SessionID               string
	UnifiedExecEnvironments []UnifiedExecEnvironment
	ManagedNetworkResolver  ManagedNetworkResolver
	// PreserveLineEndings mirrors Rust Feature::ApplyPatchPreserveLineEndings
	// (c9c6c0daa9): carry the rollout state into shell child processes so
	// arg0-dispatched apply_patch preserves CRLF/CR line endings.
	PreserveLineEndings bool
	// PluginMetricsResolver resolves a trusted plugin analytics operation for
	// one shell command (Rust #38252).
	PluginMetricsResolver func(command []string, cwd string) *plugin.ResolvedPluginMetricsOperation
	// PluginMeasurementTracker publishes a validated plugin measurement batch.
	PluginMeasurementTracker func(context.Context, plugin.PluginMeasurementBatch)
}

type ShellExecutor struct {
	runner                   ShellRunner
	shell                    *Shell
	validation               ShellValidationOptions
	toolName                 ToolName
	approval                 ShellApprovalFunc
	maxOutputTokens          *int
	unifiedExec              *UnifiedExecManager
	unifiedExecEvents        UnifiedExecEventSink
	unifiedExecThreadID      string
	unifiedExecTurnID        string
	sessionID                string
	unifiedExecEnvironments  []UnifiedExecEnvironment
	managedNetworkResolver   ManagedNetworkResolver
	preserveLineEndings      bool
	pluginMetricsResolver    func(command []string, cwd string) *plugin.ResolvedPluginMetricsOperation
	pluginMeasurementTracker func(context.Context, plugin.PluginMeasurementBatch)
}

type ShellApprovalDecision struct {
	Approved     bool
	AllowSession bool
}

type ShellApprovalRequest struct {
	Request    *ShellRequest
	Invocation *Invocation
}

type ShellApprovalFunc func(context.Context, *ShellApprovalRequest) (ShellApprovalDecision, error)

type ManagedNetworkResolution struct {
	Env                          map[string]string
	ManagedNetwork               *network.ProxyManagedNetworkSandboxContext
	RemoteNetworkProxy           *execserver.RemoteNetworkProxyLaunchConfig
	NetworkPolicyDecider         network.ProxyPolicyDecider
	NetworkPolicyDecisionTimeout time.Duration
}

type ManagedNetworkResolver func(environmentID string, remote bool) (*ManagedNetworkResolution, error)

func NewShellExecutor(options *ShellExecutorOptions) *ShellExecutor {
	executor := &ShellExecutor{
		runner:     NewLocalShellRunner(),
		shell:      NewDefaultShell(),
		validation: ShellValidationOptions{ApprovalPolicy: sandbox.ApprovalOnRequest},
		toolName:   PlainName(DefaultExecCommandToolName),
	}
	if options == nil {
		return executor
	}
	if options.Runner != nil {
		executor.runner = options.Runner
	}
	if options.Shell != nil {
		executor.shell = options.Shell
	}
	executor.validation = options.Validation
	if executor.validation.ApprovalPolicy == "" {
		executor.validation.ApprovalPolicy = sandbox.ApprovalOnRequest
	}
	if options.ToolName.Key() != "" {
		executor.toolName = options.ToolName
	}
	executor.approval = options.Approval
	executor.maxOutputTokens = cloneNonNegativeInt(options.MaxOutputTokens)
	executor.unifiedExec = options.UnifiedExec
	executor.unifiedExecEvents = options.UnifiedExecEvents
	executor.unifiedExecThreadID = options.UnifiedExecThreadID
	executor.unifiedExecTurnID = options.UnifiedExecTurnID
	executor.sessionID = options.SessionID
	executor.unifiedExecEnvironments = cloneUnifiedExecEnvironments(options.UnifiedExecEnvironments)
	executor.managedNetworkResolver = options.ManagedNetworkResolver
	executor.preserveLineEndings = options.PreserveLineEndings
	executor.pluginMetricsResolver = options.PluginMetricsResolver
	executor.pluginMeasurementTracker = options.PluginMeasurementTracker
	return executor
}

func RegisterShellHandler(registry *Registry, options *ShellExecutorOptions) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", ErrToolInvalidCall)
	}
	return registry.Register(NewShellExecutor(options))
}

func (e *ShellExecutor) Spec() Spec {
	if e == nil || e.toolName.Key() == "" {
		return Spec{Name: PlainName(DefaultExecCommandToolName)}
	}
	if e.toolName.Key() == DefaultShellCommandToolName {
		return e.shellCommandSpec()
	}
	if e.unifiedExec != nil {
		return e.unifiedExecSpec()
	}
	return Spec{
		Name:        e.toolName,
		Description: "Runs a shell command in the current workspace.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"cmd"},
			"properties": map[string]any{
				"cmd": map[string]any{
					"type":        "string",
					"description": "The shell command to execute.",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for the command. Relative paths are resolved from the session cwd.",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Alias for cwd.",
				},
				"env": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"timeout_ms":    map[string]any{"type": "integer", "minimum": 0},
				"yield_time_ms": map[string]any{"type": "integer", "minimum": 0},
				"max_output_tokens": map[string]any{
					"type":    "integer",
					"minimum": 0,
				},
				"shell":               map[string]any{"type": "string"},
				"login":               map[string]any{"type": "boolean"},
				"tty":                 map[string]any{"type": "boolean"},
				"sandbox_permissions": map[string]any{"type": "string"},
				"additional_permissions": map[string]any{
					"type": "object",
				},
				"justification": map[string]any{"type": "string"},
				"prefix_rule":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		Parallel: true,
	}
}

func (e *ShellExecutor) shellCommandSpec() Spec {
	properties := map[string]any{
		"command": map[string]any{
			"type":        "string",
			"description": "Shell script to run in the user's default shell.",
		},
		"workdir": map[string]any{
			"type":        "string",
			"description": "Working directory for the command. Defaults to the turn cwd.",
		},
		"timeout_ms": map[string]any{
			"type":        "number",
			"description": "Maximum command runtime. Defaults to 10000 ms.",
		},
	}
	if e.validation.AllowLoginShell {
		properties["login"] = map[string]any{
			"type":        "boolean",
			"description": "True runs with login shell semantics; false disables them. Defaults to true.",
		}
	}
	for key, schema := range unifiedExecApprovalProperties(e.validation.AdditionalPermissionsAllowed) {
		properties[key] = schema
	}
	description := "Runs a shell command and returns its output."
	if runtime.GOOS == "windows" {
		description = `Runs a Powershell command (Windows) and returns its output.

Examples of valid command strings:

- ls -a (show hidden): "Get-ChildItem -Force"
- recursive find by name: "Get-ChildItem -Recurse -Filter *.py"
- recursive grep: "Get-ChildItem -Path C:\\myrepo -Recurse | Select-String -Pattern 'TODO' -CaseSensitive"
- ps aux | grep python: "Get-Process | Where-Object { $_.ProcessName -like '*python*' }"
- setting an env var: "$env:FOO='bar'; echo $env:FOO"
- running an inline Python script: "@'\nprint('Hello, world!')\n'@ | python -"

` + unifiedExecWindowsShellGuidance
	} else {
		description += "\n- Always set the `workdir` param when using the shell_command function. Do not use `cd` unless absolutely necessary."
	}
	return Spec{
		Name:        e.toolName,
		Description: description,
		InputSchema: map[string]any{
			"type":                 "object",
			"required":             []string{"command"},
			"additionalProperties": false,
			"properties":           properties,
		},
		Parallel: true,
	}
}

func IsShellCommandToolName(name ToolName) bool {
	if name.Namespace != "" {
		return false
	}
	return name.Name == DefaultExecCommandToolName || name.Name == DefaultShellCommandToolName
}

func (e *ShellExecutor) unifiedExecSpec() Spec {
	yieldTimeDescription := "Wait before yielding output. Defaults to 10000 ms; effective range is 250-30000 ms."
	if runtime.GOOS == "windows" {
		yieldTimeDescription = "Maximum time to wait before returning a session ID for a still-running command. Commands that finish sooner return immediately. For ordinary commands, omit this parameter to use the 10000 ms default. Effective range on Windows is 10000-30000 ms."
	}
	properties := map[string]any{
		"cmd": map[string]any{
			"type":        "string",
			"description": "Shell command to execute.",
		},
		"workdir": map[string]any{
			"type":        "string",
			"description": "Working directory for the command. Defaults to the turn cwd.",
		},
		"tty": map[string]any{
			"type":        "boolean",
			"description": "True allocates a PTY for the command; false or omitted uses plain pipes.",
		},
		"yield_time_ms": map[string]any{
			"type":        "number",
			"description": yieldTimeDescription,
		},
		"max_output_tokens": map[string]any{
			"type":        "number",
			"description": "Output token budget. Defaults to 10000 tokens; larger requests may be capped by policy.",
		},
		"shell": map[string]any{
			"type":        "string",
			"description": "Shell binary to launch. Defaults to the user's default shell.",
		},
	}
	if e.validation.AllowLoginShell {
		properties["login"] = map[string]any{
			"type":        "boolean",
			"description": "True runs the shell with -l/-i semantics; false disables them. Defaults to true.",
		}
	}
	if len(e.unifiedExecEnvironments) > 1 {
		properties["environment_id"] = map[string]any{
			"type":        "string",
			"description": "Environment id from <environment_context>. Omit to use the primary environment.",
		}
	}
	for key, schema := range unifiedExecApprovalProperties(e.validation.AdditionalPermissionsAllowed) {
		properties[key] = schema
	}
	description := "Runs a command in a PTY, returning output or a session ID for ongoing interaction."
	if shell := e.modelVisibleShell(); shell != nil && shell.Type == ShellPowerShell {
		description += "\n\nThe selected execution environment uses PowerShell. Write PowerShell-compatible commands. POSIX heredocs such as `python - <<'PY'` are not supported; use `python -c`, a PowerShell here-string piped to Python, or a temporary `.py` file instead."
	} else if shell != nil && shell.Type == ShellCmd {
		description += "\n\nThe selected execution environment uses Windows cmd.exe. Write cmd-compatible commands; POSIX heredocs are not supported."
	}
	if runtime.GOOS == "windows" {
		description += "\n\n" + unifiedExecWindowsShellGuidance
	}
	return Spec{
		Name:        e.toolName,
		Description: description,
		InputSchema: map[string]any{
			"type":                 "object",
			"required":             []string{"cmd"},
			"additionalProperties": false,
			"properties":           properties,
		},
		OutputSchema: unifiedExecOutputSchema(),
		Parallel:     true,
	}
}

// modelVisibleShell returns the shell that an exec_command without an explicit
// environment_id will actually use. Keep this aligned with Execute: the
// primary selected environment overrides the session shell.
func (e *ShellExecutor) modelVisibleShell() *Shell {
	if e == nil {
		return nil
	}
	if len(e.unifiedExecEnvironments) > 0 && e.unifiedExecEnvironments[0].Shell != nil {
		return e.unifiedExecEnvironments[0].Shell
	}
	return e.sessionShell()
}

func unifiedExecApprovalProperties(additionalPermissions bool) map[string]any {
	values := []any{string(sandbox.SandboxPermissionsUseDefault)}
	description := "Per-command sandbox override. Defaults to `use_default`; use `require_escalated` for unsandboxed execution."
	if additionalPermissions {
		values = append(values, string(sandbox.SandboxPermissionsWithAdditionalPermissions))
		description = "Per-command sandbox override. Defaults to `use_default`; use `with_additional_permissions` with `additional_permissions`, or `require_escalated` for unsandboxed execution."
	}
	values = append(values, string(sandbox.SandboxPermissionsRequireEscalated))
	properties := map[string]any{
		"sandbox_permissions": map[string]any{
			"type":        "string",
			"enum":        values,
			"description": description,
		},
		"justification": map[string]any{
			"type":        "string",
			"description": "User-facing approval question for `require_escalated`; omit otherwise.",
		},
		"prefix_rule": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": `Reusable approval prefix for ` + "`cmd`" + `, only with ` + "`sandbox_permissions: \"require_escalated\"`" + `; for example ["git", "pull"].`,
		},
	}
	if additionalPermissions {
		properties["additional_permissions"] = unifiedExecAdditionalPermissionsSchema()
	}
	return properties
}

func unifiedExecAdditionalPermissionsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          `Sandboxed filesystem or network access for this command; only with ` + "`sandbox_permissions: \"with_additional_permissions\"`" + `.`,
		"additionalProperties": false,
		"properties": map[string]any{
			"network": map[string]any{
				"type":                 "object",
				"description":          "Network access request.",
				"additionalProperties": false,
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean", "description": "True requests network access; false or omitted requests none."},
				},
			},
			"file_system": map[string]any{
				"type":                 "object",
				"description":          "Filesystem access request.",
				"additionalProperties": false,
				"properties": map[string]any{
					"read":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Absolute paths to grant read access; omit when none are needed."},
					"write": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Absolute paths to grant write access; omit when none are needed."},
				},
			},
		},
	}
}

const unifiedExecWindowsShellGuidance = "Windows safety rules:\n" +
	"- Do not compose destructive filesystem commands across shells. Do not enumerate paths in PowerShell and then pass them to `cmd /c`, batch builtins, or another shell for deletion or moving. Use one shell end-to-end, prefer native PowerShell cmdlets such as `Remove-Item` / `Move-Item` with `-LiteralPath`, and avoid string-built shell commands for file operations.\n" +
	"- Before any recursive delete or move on Windows, verify the resolved absolute target paths stay within the intended workspace or explicitly named target directory. Never issue a recursive delete or move against a computed path if the final target has not been checked.\n" +
	"- When using `Start-Process` to launch a background helper or service, pass `-WindowStyle Hidden` unless the user explicitly asked for a visible interactive window. Use visible windows only for interactive tools the user needs to see or control."

func (e *ShellExecutor) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	args, err := decodeExecCommandInvocation(invocation)
	if err != nil {
		return nil, err
	}
	args.MaxOutputTokens = clampShellMaxOutputTokens(args.MaxOutputTokens, e.maxOutputTokens)
	validation := e.validationOptions()
	sessionShell := e.sessionShell()
	environment, err := e.resolveUnifiedExecEnvironment(args.EnvironmentID)
	if err != nil {
		return nil, RespondToModel(err.Error())
	}
	if environment != nil {
		environmentCWD := environment.CWD
		remoteEnvironment := environment.ExecServerURL != "" || environment.NoiseProvider != nil
		if remoteEnvironment {
			environmentCWD, err = resolveRemoteUnifiedExecCWD(environment.CWD, firstNonEmptyString(args.CWD, args.Workdir))
			if err != nil {
				return nil, RespondToModel(err.Error())
			}
			args.CWD = ""
			args.Workdir = ""
		}
		validation.CWD = environmentCWD
		if environment.Shell != nil {
			sessionShell = environment.Shell
		}
		if remoteEnvironment && strings.TrimSpace(args.Shell) != "" {
			if environment.Shell == nil {
				return nil, RespondToModel(fmt.Sprintf("environment `%s` does not report a shell", environment.ID))
			}
			if DetectShellType(args.Shell) != environment.Shell.Type {
				return nil, RespondToModel(fmt.Sprintf("environment `%s` only supports `%s`", environment.ID, environment.Shell.Type))
			}
			args.Shell = ""
		}
	}
	if e.managedNetworkResolver != nil {
		environmentID := "local"
		remoteEnvironment := false
		if environment != nil && strings.TrimSpace(environment.ID) != "" {
			environmentID = environment.ID
			remoteEnvironment = environment.ExecServerURL != "" || environment.NoiseProvider != nil
		}
		resolvedNetwork, resolveErr := e.managedNetworkResolver(environmentID, remoteEnvironment)
		if resolveErr != nil {
			return nil, RespondToModel(fmt.Sprintf("failed to prepare network proxy for environment `%s`: %v", environmentID, resolveErr))
		}
		if resolvedNetwork != nil {
			validation.Env = resolvedNetwork.Env
			validation.EnforceManagedNetwork = resolvedNetwork.ManagedNetwork != nil || resolvedNetwork.RemoteNetworkProxy != nil
			validation.ManagedNetwork = resolvedNetwork.ManagedNetwork
			validation.RemoteNetworkProxy = resolvedNetwork.RemoteNetworkProxy
			validation.NetworkPolicyDecider = resolvedNetwork.NetworkPolicyDecider
			validation.NetworkPolicyDecisionTimeout = resolvedNetwork.NetworkPolicyDecisionTimeout
		}
	}
	if invocationPermissionPreapproved(invocation) {
		validation.PermissionsPreapproved = true
	}
	// Rust c9c6c0daa9: the active feature configuration is authoritative over
	// inherited, shell snapshot, and client-provided environment values.
	validation.Env = envutil.InjectApplyPatchEnv(validation.Env, e.preserveLineEndings)
	// Rust 97729885d4: expose the shared root-session identity to shell commands.
	validation.Env = injectSessionIDEnv(validation.Env, e.sessionID)
	req, err := BuildShellRequest(args, sessionShell, validation)
	if err != nil {
		return nil, RespondToModel(err.Error())
	}
	if req.ApprovalRequired {
		if e.approval != nil {
			decision, approvalErr := e.approval(ctx, &ShellApprovalRequest{Request: req, Invocation: invocation})
			if approvalErr != nil {
				body := "Approval request failed: " + approvalErr.Error()
				return &Output{
					Success:    false,
					Body:       body,
					Error:      body,
					Data:       shellApprovalDeniedData(req, "error"),
					LogPreview: shellLogPreview(body),
				}, nil
			}
			if !decision.Approved {
				body := "Approval denied before running command."
				return &Output{
					Success:    false,
					Body:       body,
					Error:      body,
					Data:       shellApprovalDeniedData(req, "deny"),
					LogPreview: shellLogPreview(body),
				}, nil
			}
			validation.PermissionsPreapproved = true
			req, err = BuildShellRequest(args, sessionShell, validation)
			if err != nil {
				return nil, RespondToModel(err.Error())
			}
		} else {
			body := shellApprovalRequiredMessage(req)
			return &Output{
				Success:    false,
				Body:       body,
				Error:      body,
				Data:       shellApprovalRequestData(req),
				LogPreview: shellLogPreview(body),
			}, nil
		}
	}
	var metricsSidecar *plugin.PluginMetricsSidecar
	remoteEnvironment := environment != nil && (environment.ExecServerURL != "" || environment.NoiseProvider != nil)
	if e.pluginMetricsResolver != nil && !remoteEnvironment {
		if req.Env == nil {
			req.Env = map[string]string{}
		}
		plugin.StripPluginMetricsOutputEnv(req.Env)
		if resolved := e.pluginMetricsResolver(req.Command, req.CWD); resolved != nil {
			metricsSidecar = plugin.NewPluginMetricsSidecar(*resolved)
			if metricsSidecar != nil {
				metricsSidecar.InstallOutputEnv(req.Env)
				req.AdditionalPermissions = sandbox.MergePermissionProfiles(req.AdditionalPermissions, &sandbox.AdditionalPermissionProfile{
					FileSystem: []string{metricsSidecar.OutputDir()},
				})
			}
		}
	}
	if environment != nil {
		req.UnifiedExecEnvironmentID = environment.ID
		req.UnifiedExecRemoteURL = environment.ExecServerURL
		req.UnifiedExecNoiseProvider = environment.NoiseProvider
	}
	if req.RemoteNetworkProxy != nil {
		launch := *req.RemoteNetworkProxy
		if executionID := strings.TrimSpace(invocation.CallID); executionID != "" {
			launch.ExecutionID = &executionID
		}
		req.RemoteNetworkProxy = &launch
	}
	var result *ShellResult
	if e.shouldUseUnifiedExec(req) {
		req, err = prepareUnifiedExecShellRequest(req)
		if err != nil {
			return nil, err
		}
		req.UnifiedExecEventSink = e.unifiedExecEvents
		req.UnifiedExecThreadID = e.unifiedExecThreadID
		req.UnifiedExecTurnID = e.unifiedExecTurnID
		result, err = e.unifiedExec.Exec(ctx, req, invocation.CallID)
	} else {
		result, err = e.shellRunner().Run(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	if metricsSidecar != nil {
		if result.ProcessID != nil {
			metricsSidecar.Cleanup()
		} else {
			exitCode := 0
			if result.HasExitCode {
				exitCode = result.ExitCode
			}
			if batch := metricsSidecar.Finish(exitCode); batch != nil && e.pluginMeasurementTracker != nil {
				e.pluginMeasurementTracker(ctx, *batch)
			}
		}
	}
	if result.EventCallID == "" {
		result.EventCallID = invocation.CallID
	}
	if result.HookCommand == "" {
		result.HookCommand = req.HookCommand
	}
	result.UnifiedExecEvented = result.UnifiedExecEvented || req.UnifiedExecEventSink != nil
	if result.ChunkID == "" {
		result.ChunkID = generateShellChunkID()
	}
	if result.MaxOutputTokensUsed == nil {
		result.MaxOutputTokensUsed = cloneNonNegativeInt(req.MaxOutputTokens)
	}
	body := shellResultModelTextWithMetadata(result, result.MaxOutputTokensUsed, result.ChunkID)
	return &Output{
		Success:    true,
		Body:       body,
		Data:       shellResultData(result, result.MaxOutputTokensUsed, result.ChunkID),
		LogPreview: shellLogPreview(body),
	}, nil
}

func (e *ShellExecutor) resolveUnifiedExecEnvironment(requested string) (*UnifiedExecEnvironment, error) {
	if e == nil || len(e.unifiedExecEnvironments) == 0 {
		if requested != "" {
			return nil, fmt.Errorf("unknown turn environment id `%s`", requested)
		}
		return nil, nil
	}
	if requested == "" {
		requested = e.unifiedExecEnvironments[0].ID
	}
	for i := range e.unifiedExecEnvironments {
		if e.unifiedExecEnvironments[i].ID == requested {
			environment := e.unifiedExecEnvironments[i]
			return &environment, nil
		}
	}
	return nil, fmt.Errorf("unknown turn environment id `%s`", requested)
}

func cloneUnifiedExecEnvironments(values []UnifiedExecEnvironment) []UnifiedExecEnvironment {
	out := make([]UnifiedExecEnvironment, len(values))
	for i := range values {
		out[i] = values[i]
		if values[i].Shell != nil {
			shell := *values[i].Shell
			out[i].Shell = &shell
		}
	}
	return out
}

func resolveRemoteUnifiedExecCWD(base string, workdir string) (string, error) {
	base = strings.TrimSpace(base)
	basePath := utils.NewLegacyAppPathString(base)
	convention, ok := basePath.InferAbsolutePathConvention()
	if !ok {
		return "", fmt.Errorf("path `%s` does not use absolute POSIX or Windows path syntax", base)
	}
	if workdir == "" {
		return utils.LexicalClean(base, convention), nil
	}
	overridePath := utils.NewLegacyAppPathString(workdir)
	if overrideConvention, absolute := overridePath.InferAbsolutePathConvention(); absolute {
		if overrideConvention != convention {
			return "", fmt.Errorf("path `%s` does not use the selected environment's %s path convention", workdir, convention)
		}
		return utils.LexicalClean(workdir, convention), nil
	}
	directoryBase := base
	if convention == utils.ConventionWindows {
		if !strings.HasSuffix(directoryBase, `\`) && !strings.HasSuffix(directoryBase, "/") {
			directoryBase += `\`
		}
	} else if !strings.HasSuffix(directoryBase, "/") {
		directoryBase += "/"
	}
	baseURI, err := utils.FromAbsoluteNativePath(directoryBase, convention)
	if err != nil {
		return "", err
	}
	joined, err := baseURI.Join(workdir)
	if err != nil {
		return "", err
	}
	rendered, err := utils.LegacyAppPathStringFromURI(joined, convention)
	if err != nil {
		return "", err
	}
	return rendered.Value, nil
}

func (e *ShellExecutor) shouldUseUnifiedExec(req *ShellRequest) bool {
	if e == nil || e.unifiedExec == nil || req == nil {
		return false
	}
	if strings.TrimSpace(req.UnifiedExecRemoteURL) != "" || req.UnifiedExecNoiseProvider != nil {
		return true
	}
	if runtime.GOOS == "windows" && req.PermissionProfile != nil && !req.PermissionProfile.Disabled {
		return true
	}
	return req.PermissionProfile == nil || req.PermissionProfile.Disabled || runtime.GOOS == "linux"
}

func prepareUnifiedExecShellRequest(req *ShellRequest) (*ShellRequest, error) {
	if req == nil || strings.TrimSpace(req.UnifiedExecRemoteURL) != "" || req.UnifiedExecNoiseProvider != nil || req.PermissionProfile == nil || req.PermissionProfile.Disabled {
		return req, nil
	}
	plan, err := sandbox.BuildCommandRunPlan(&sandbox.CommandRunRequest{
		ResolvedPermissionProfile:     req.PermissionProfile,
		ResolvedPermissionProfileID:   req.PermissionProfileID,
		ResolvedPermissionProfileJSON: req.PermissionProfileJSON,
		CWD:                           req.CWD,
		Command:                       append([]string(nil), req.Command...),
	})
	if err != nil {
		return nil, err
	}
	if err := plan.UnsupportedError(); err != nil {
		return nil, err
	}
	prepared := *req
	prepared.Command = append([]string(nil), plan.Command...)
	prepared.CWD = plan.CWD
	prepared.ProcessSandboxType = cloneSandboxType(plan.SandboxType)
	prepared.Env = cloneEnv(req.Env)
	if prepared.Env == nil {
		prepared.Env = map[string]string{}
	}
	if profileID := strings.TrimSpace(plan.PermissionProfileID); profileID != "" {
		prepared.Env["CODEX_PERMISSION_PROFILE"] = profileID
	}
	return &prepared, nil
}

func cloneSandboxType(value sandbox.SandboxType) *sandbox.SandboxType {
	cloned := value
	return &cloned
}

func clampShellMaxOutputTokens(requested *int, policy *int) *int {
	requested = cloneNonNegativeInt(requested)
	policy = cloneNonNegativeInt(policy)
	if policy == nil {
		return requested
	}
	if requested == nil || *requested > *policy {
		return policy
	}
	return requested
}

func cloneNonNegativeInt(value *int) *int {
	if value == nil || *value < 0 {
		return nil
	}
	cloned := *value
	return &cloned
}

func (e *ShellExecutor) PreToolUsePayload(invocation *Invocation) (*PreToolUsePayload, bool) {
	args, err := decodeExecCommandInvocation(invocation)
	if err != nil {
		return nil, false
	}
	return &PreToolUsePayload{
		ToolName:  bashHookToolName(),
		ToolInput: shellHookInput(args),
	}, true
}

func (e *ShellExecutor) PostToolUsePayload(invocation *Invocation, output *Output) (*PostToolUsePayload, bool) {
	args, err := decodeExecCommandInvocation(invocation)
	if err != nil || strings.TrimSpace(args.Cmd) == "" {
		return nil, false
	}
	if output == nil {
		return nil, false
	}
	if output.Data != nil && output.Data["process_id"] != nil {
		return nil, false
	}
	response, ok := output.Data["hook_response"].(string)
	if !ok {
		response = output.Body
	}
	if strings.TrimSpace(response) == "" && output.Data == nil {
		return nil, false
	}
	toolUseID := firstNonEmptyString(output.CallID, invocation.CallID)
	if eventCallID, ok := output.Data["event_call_id"].(string); ok && eventCallID != "" {
		toolUseID = eventCallID
	}
	return &PostToolUsePayload{
		ToolName:     bashHookToolName(),
		ToolUseID:    toolUseID,
		ToolInput:    shellHookInput(args),
		ToolResponse: response,
	}, true
}

func (e *ShellExecutor) WithUpdatedHookInput(invocation *Invocation, updatedInput any) (*Invocation, error) {
	command, err := updatedHookCommand(updatedInput)
	if err != nil {
		return nil, err
	}
	var args map[string]any
	if invocation == nil || invocation.Payload.Kind != PayloadFunction {
		return nil, RespondToModel("hook input rewrite received unsupported exec_command payload")
	}
	if strings.TrimSpace(invocation.Payload.Arguments) == "" {
		args = map[string]any{}
	} else if err := json.Unmarshal([]byte(invocation.Payload.Arguments), &args); err != nil {
		return nil, RespondToModel(fmt.Sprintf("failed to parse exec_command arguments for hook rewrite: %v", err))
	}
	commandField := "cmd"
	if invocation.ToolName.Key() == DefaultShellCommandToolName {
		commandField = "command"
	}
	args[commandField] = command
	data, err := json.Marshal(args)
	if err != nil {
		return nil, RespondToModel(fmt.Sprintf("failed to serialize rewritten exec_command arguments: %v", err))
	}
	updated := cloneInvocation(invocation)
	updated.Payload.Arguments = string(data)
	return updated, nil
}

func ShellResultModelText(result *ShellResult, maxOutputTokens *int) string {
	return shellResultModelTextWithMetadata(result, maxOutputTokens, "")
}

func shellResultModelTextWithMetadata(result *ShellResult, maxOutputTokens *int, chunkID string) string {
	if result == nil {
		return ""
	}
	hookResponse := ShellResultHookResponse(result, maxOutputTokens)
	sections := []string{}
	if chunkID != "" {
		sections = append(sections, "Chunk ID: "+chunkID)
	}
	sections = append(sections, fmt.Sprintf("Wall time: %.4f seconds", result.Duration.Seconds()))
	if result.TimedOut {
		sections = append(sections, "Process timed out")
	} else if result.ProcessID == nil || result.HasExitCode {
		sections = append(sections, fmt.Sprintf("Process exited with code %d", result.ExitCode))
	}
	if result.ProcessID != nil {
		sections = append(sections, fmt.Sprintf("Process running with session ID %d", *result.ProcessID))
	}
	sections = append(sections, fmt.Sprintf("Original token count: %d", utils.ApproxTokenCount(shellOutputText(result))))
	sections = append(sections, "Output:", hookResponse)
	return strings.Join(sections, "\n")
}

func ShellResultHookResponse(result *ShellResult, maxOutputTokens *int) string {
	response := shellResultHookResponse(result, maxOutputTokens)
	return response.Text
}

type shellTruncatedText struct {
	Text               string
	Truncated          bool
	OriginalTokenCount int
	TotalLines         int
}

func shellResultHookResponse(result *ShellResult, maxOutputTokens *int) *shellTruncatedText {
	if result == nil {
		return &shellTruncatedText{}
	}
	output := shellOutputText(result)
	maxTokens := DefaultExecCommandMaxOutputTokens
	if maxOutputTokens != nil {
		maxTokens = *maxOutputTokens
	}
	return truncateShellText(output, maxTokens)
}

func decodeExecCommandInvocation(invocation *Invocation) (*ExecCommandArgs, error) {
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	var args ExecCommandArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Cmd) == "" && invocation.ToolName.Key() == DefaultShellCommandToolName {
		var legacy struct {
			Command string `json:"command"`
		}
		if err := invocation.DecodeArguments(&legacy); err != nil {
			return nil, err
		}
		args.Cmd = legacy.Command
	}
	return &args, nil
}

func (e *ShellExecutor) sessionShell() *Shell {
	if e == nil || e.shell == nil {
		return NewDefaultShell()
	}
	return e.shell
}

func (e *ShellExecutor) shellRunner() ShellRunner {
	if e == nil || e.runner == nil {
		return NewLocalShellRunner()
	}
	return e.runner
}

func (e *ShellExecutor) validationOptions() ShellValidationOptions {
	if e == nil {
		return ShellValidationOptions{ApprovalPolicy: sandbox.ApprovalOnRequest}
	}
	opts := e.validation
	if opts.ApprovalPolicy == "" {
		opts.ApprovalPolicy = sandbox.ApprovalOnRequest
	}
	return opts
}

func bashHookToolName() *HookToolName {
	return &HookToolName{Name: "Bash"}
}

// injectSessionIDEnv exposes the shared root-session identity to shell child
// processes as CODEX_SESSION_ID (Rust 97729885d4). The runtime-selected value is
// authoritative over inherited and client-provided values.
func injectSessionIDEnv(env map[string]string, sessionID string) map[string]string {
	if env == nil {
		env = map[string]string{}
	}
	for key := range env {
		if strings.EqualFold(key, "CODEX_SESSION_ID") {
			delete(env, key)
		}
	}
	if strings.TrimSpace(sessionID) != "" {
		env["CODEX_SESSION_ID"] = strings.TrimSpace(sessionID)
	}
	return env
}

func shellHookInput(args *ExecCommandArgs) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	input := map[string]any{"command": args.Cmd}
	if strings.TrimSpace(args.CWD) != "" {
		input["cwd"] = args.CWD
	}
	if strings.TrimSpace(args.Workdir) != "" {
		input["workdir"] = args.Workdir
	}
	if len(args.Env) > 0 {
		input["env"] = cloneEnv(args.Env)
	}
	if args.TimeoutMS != 0 {
		input["timeout_ms"] = args.TimeoutMS
	}
	return input
}

func shellResultData(result *ShellResult, maxOutputTokens *int, chunkID string) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	maxTokens := DefaultExecCommandMaxOutputTokens
	if maxOutputTokens != nil {
		maxTokens = *maxOutputTokens
	}
	stdout := truncateShellText(result.Stdout, maxTokens)
	stderr := truncateShellText(result.Stderr, maxTokens)
	hookResponse := shellResultHookResponse(result, maxOutputTokens)
	data := map[string]any{
		"chunk_id":                chunkID,
		"wall_time_seconds":       result.Duration.Seconds(),
		"original_token_count":    utils.ApproxTokenCount(shellOutputText(result)),
		"output":                  hookResponse.Text,
		"stdout":                  result.Stdout,
		"stderr":                  result.Stderr,
		"stdout_truncated":        stdout.Truncated,
		"stderr_truncated":        stderr.Truncated,
		"duration_ms":             result.Duration.Milliseconds(),
		"timed_out":               result.TimedOut,
		"hook_response":           hookResponse.Text,
		"hook_response_truncated": hookResponse.Truncated,
	}
	if result.ProcessID != nil {
		data["process_id"] = *result.ProcessID
		data["session_id"] = *result.ProcessID
	} else {
		data["exit_code"] = result.ExitCode
	}
	if result.EventCallID != "" {
		data["event_call_id"] = result.EventCallID
	}
	if result.HookCommand != "" {
		data["hook_command"] = result.HookCommand
	}
	if result.UnifiedExecEvented {
		data["unified_exec_evented"] = true
		data["source"] = "unifiedExecStartup"
		if result.ProcessID != nil {
			data["status"] = "inProgress"
		} else if result.ExitCode == 0 {
			data["status"] = "completed"
		} else {
			data["status"] = "failed"
		}
	}
	return data
}

var shellChunkIDFallback atomic.Uint32

func generateShellChunkID() string {
	buffer := make([]byte, 3)
	if _, err := rand.Read(buffer); err == nil {
		return hex.EncodeToString(buffer)
	}
	return fmt.Sprintf("%06x", shellChunkIDFallback.Add(1)&0x00ff_ffff)
}

func shellApprovalRequiredMessage(req *ShellRequest) string {
	if req == nil {
		return "Approval required before running command."
	}
	reason := strings.TrimSpace(req.ApprovalReason)
	if reason == "" {
		reason = "command requested sandbox permissions"
	}
	return fmt.Sprintf("Approval required before running command: %s", reason)
}

func shellApprovalRequestData(req *ShellRequest) map[string]any {
	data := map[string]any{
		"approval_required": true,
		"retry_context":     map[string]any{"permissions_preapproved": true},
	}
	if req == nil {
		return data
	}
	data["command"] = cloneStrings(req.Command)
	data["hook_command"] = req.HookCommand
	data["cwd"] = req.CWD
	data["sandbox_permissions"] = req.SandboxPermissions
	data["reason"] = req.ApprovalReason
	data["justification"] = req.Justification
	if len(req.PrefixRule) > 0 {
		data["prefix_rule"] = cloneStrings(req.PrefixRule)
	}
	if req.AdditionalPermissions != nil {
		data["additional_permissions"] = req.AdditionalPermissions
	}
	if req.SandboxProfile != nil {
		data["sandbox_profile"] = req.SandboxProfile
	}
	return data
}

func shellApprovalDeniedData(req *ShellRequest, decision string) map[string]any {
	data := shellApprovalRequestData(req)
	data["approval_required"] = false
	data["approval_decision"] = decision
	return data
}

func invocationPermissionPreapproved(invocation *Invocation) bool {
	if invocation == nil || invocation.Context == nil {
		return false
	}
	if value, ok := invocation.Context["permissions_preapproved"].(bool); ok && value {
		return true
	}
	if value, ok := invocation.Context["approval_preapproved"].(bool); ok && value {
		return true
	}
	retryContext, ok := invocation.Context["retry_context"].(map[string]any)
	if !ok {
		return false
	}
	value, _ := retryContext["permissions_preapproved"].(bool)
	return value
}

func shellOutputText(result *ShellResult) string {
	if result == nil {
		return ""
	}
	if result.Stderr == "" {
		return result.Stdout
	}
	if result.Stdout == "" {
		return result.Stderr
	}
	return strings.TrimRight(result.Stdout, "\r\n") + "\n\nstderr:\n" + result.Stderr
}

func truncateShellText(content string, maxTokens int) *shellTruncatedText {
	if content == "" {
		return &shellTruncatedText{}
	}
	policy := utils.TokensPolicy(maxTokens)
	if len(content) <= (&policy).ByteBudget() {
		return &shellTruncatedText{Text: content, TotalLines: shellOutputLineCount(content)}
	}
	return &shellTruncatedText{
		Text:               utils.FormattedTruncateText(content, policy),
		Truncated:          true,
		OriginalTokenCount: utils.ApproxTokenCount(content),
		TotalLines:         shellOutputLineCount(content),
	}
}

func shellOutputLineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

func shellLogPreview(body string) string {
	body = strings.TrimSpace(body)
	if len(body) <= 200 {
		return body
	}
	return body[:200]
}

func updatedHookCommand(updatedInput any) (string, error) {
	value, ok := updatedInput.(map[string]any)
	if !ok {
		return "", RespondToModel("hook returned updatedInput without string field `command`")
	}
	command, ok := value["command"].(string)
	if !ok {
		return "", RespondToModel("hook returned updatedInput without string field `command`")
	}
	return command, nil
}

var _ Executor = (*ShellExecutor)(nil)
var _ PreToolUsePayloadProvider = (*ShellExecutor)(nil)
var _ PostToolUsePayloadProvider = (*ShellExecutor)(nil)
var _ HookInputUpdater = (*ShellExecutor)(nil)
