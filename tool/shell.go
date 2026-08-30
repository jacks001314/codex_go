package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codex_go/execpolicy"
	"codex_go/execserver"
	"codex_go/network"
	"codex_go/safety"
	"codex_go/sandbox"
	shellutil "codex_go/shell"
)

const (
	DefaultExecYieldTimeMS  uint64 = 10000
	DefaultWriteYieldTimeMS uint64 = 250
)

type ShellType string

const (
	ShellBash       ShellType = "bash"
	ShellZsh        ShellType = "zsh"
	ShellPowerShell ShellType = "powershell"
	ShellCmd        ShellType = "cmd"
	ShellUnknown    ShellType = "unknown"
)

type Shell struct {
	Type ShellType
	Path string
}

type ExecCommandArgs struct {
	Cmd                   string                               `json:"cmd"`
	EnvironmentID         string                               `json:"environment_id,omitempty"`
	CWD                   string                               `json:"cwd,omitempty"`
	Workdir               string                               `json:"workdir,omitempty"`
	Env                   map[string]string                    `json:"env,omitempty"`
	TimeoutMS             uint64                               `json:"timeout_ms,omitempty"`
	Shell                 string                               `json:"shell,omitempty"`
	Login                 *bool                                `json:"login,omitempty"`
	TTY                   bool                                 `json:"tty,omitempty"`
	YieldTimeMS           uint64                               `json:"yield_time_ms,omitempty"`
	MaxOutputTokens       *int                                 `json:"max_output_tokens,omitempty"`
	SandboxPermissions    sandbox.SandboxPermissions           `json:"sandbox_permissions,omitempty"`
	AdditionalPermissions *sandbox.AdditionalPermissionProfile `json:"additional_permissions,omitempty"`
	Justification         string                               `json:"justification,omitempty"`
	PrefixRule            []string                             `json:"prefix_rule,omitempty"`
	sandboxPermissionsSet bool
	justificationSet      bool
}

func (a *ExecCommandArgs) UnmarshalJSON(data []byte) error {
	if a == nil {
		return errors.New("exec command args are required")
	}
	type execCommandArgsAlias ExecCommandArgs
	var decoded execCommandArgsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*a = ExecCommandArgs(decoded)
	if value, ok := raw["sandbox_permissions"]; ok && strings.TrimSpace(string(value)) != "null" {
		a.sandboxPermissionsSet = true
	}
	if value, ok := raw["justification"]; ok && strings.TrimSpace(string(value)) != "null" {
		a.justificationSet = true
	}
	return nil
}

type ShellRequest struct {
	Command         []string
	HookCommand     string
	CWD             string
	TimeoutMS       uint64
	YieldTimeMS     uint64
	MaxOutputTokens *int
	Env             map[string]string
	// EnvPolicy, when set, filters the inherited environment for this command
	// (mirroring Rust create_env with the selected turn environment's
	// ShellEnvironmentPolicy, #38902). The command environment is built from
	// the parent environment through this policy, then req.Env overrides are
	// applied on top.
	EnvPolicy *execpolicy.EnvPolicy
	// ThreadID is exposed to model-reachable commands as CODEX_THREAD_ID when
	// an EnvPolicy builds the command environment.
	ThreadID                        string
	TTY                             bool
	SandboxPermissions              sandbox.SandboxPermissions
	AdditionalPermissions           *sandbox.AdditionalPermissionProfile
	SandboxProfile                  *ShellSandboxProfile
	PermissionProfileID             string
	PermissionProfile               *sandbox.PermissionProfile
	PermissionProfileJSON           string
	ProcessSandboxType              *sandbox.SandboxType
	WindowsSandboxLevel             sandbox.WindowsSandboxLevel
	WindowsSandboxPrivateDesktop    bool
	WindowsSandboxProxySettingsMode execserver.WindowsSandboxProxySettingsMode
	ApprovalPolicy                  sandbox.AskForApproval
	ApprovalRequired                bool
	ApprovalReason                  string
	Justification                   string
	PrefixRule                      []string
	UnifiedExecEventSink            UnifiedExecEventSink
	UnifiedExecThreadID             string
	UnifiedExecTurnID               string
	UnifiedExecRemoteURL            string
	UnifiedExecNoiseProvider        execserver.NoiseRendezvousConnectProvider
	UnifiedExecEnvironmentID        string
	EnforceManagedNetwork           bool
	ManagedNetwork                  *network.ProxyManagedNetworkSandboxContext
	RemoteNetworkProxy              *execserver.RemoteNetworkProxyLaunchConfig
	NetworkPolicyDecider            network.ProxyPolicyDecider
	NetworkPolicyDecisionTimeout    time.Duration
}

type UnifiedExecEnvironment struct {
	ID            string
	CWD           string
	Shell         *Shell
	PlatformOS    string
	ExecServerURL string
	NoiseProvider execserver.NoiseRendezvousConnectProvider
	// ShellEnvironmentPolicy is this environment's resolved shell environment
	// policy table (empty when the thread-derived policy applies), mirroring
	// Rust protocol::EnvironmentConfig shell_environment_policy (#38902).
	ShellEnvironmentPolicy map[string]any
	// AllowLoginShell overrides the turn-level login-shell policy for this
	// environment when non-nil (Rust per-environment EnvironmentConfig,
	// #38521/#38673).
	AllowLoginShell *bool
	// PermissionProfile overrides the turn-level permission profile for this
	// environment when non-nil (#38673).
	PermissionProfile     *sandbox.PermissionProfile
	PermissionProfileID   string
	PermissionProfileJSON string
}

type ShellSandboxProfile struct {
	PolicyTag             string
	NetworkEnabled        bool
	WritableRoots         []string
	AdditionalPermissions *sandbox.AdditionalPermissionProfile
}

type ShellResult struct {
	ExitCode            int
	HasExitCode         bool
	ProcessID           *int
	Stdout              string
	Stderr              string
	Duration            time.Duration
	TimedOut            bool
	ChunkID             string
	EventCallID         string
	HookCommand         string
	MaxOutputTokensUsed *int
	UnifiedExecEvented  bool
}

type ShellValidationOptions struct {
	AdditionalPermissionsAllowed bool
	ApprovalPolicy               sandbox.AskForApproval
	// GranularSandboxApproval / GranularRules mirror Rust's granular
	// AskForApproval sub-config (#40024): sandbox escalation is rejected only
	// when the shared policy check rejects prompting.
	GranularSandboxApproval         bool
	GranularRules                   bool
	PermissionsPreapproved          bool
	AllowLoginShell                 bool
	ShellMode                       UnifiedExecShellMode
	ZshForkShell                    *Shell
	CWD                             string
	Env                             map[string]string
	DefaultTimeoutMS                uint64
	PermissionProfileID             string
	PermissionProfile               *sandbox.PermissionProfile
	WindowsSandboxLevel             sandbox.WindowsSandboxLevel
	WindowsSandboxPrivateDesktop    bool
	WindowsSandboxProxySettingsMode execserver.WindowsSandboxProxySettingsMode
	EnforceManagedNetwork           bool
	ManagedNetwork                  *network.ProxyManagedNetworkSandboxContext
	RemoteNetworkProxy              *execserver.RemoteNetworkProxyLaunchConfig
	NetworkPolicyDecider            network.ProxyPolicyDecider
	NetworkPolicyDecisionTimeout    time.Duration
}

type ResolvedCommand struct {
	Command   []string
	ShellType ShellType
}

type UnifiedExecShellMode string

const (
	UnifiedExecShellModeDirect  UnifiedExecShellMode = "direct"
	UnifiedExecShellModeZshFork UnifiedExecShellMode = "zsh_fork"
)

type CommandResolutionOptions struct {
	AllowLoginShell bool
	ShellMode       UnifiedExecShellMode
	ZshForkShell    *Shell
}

func NewDefaultShell() *Shell {
	if runtime.GOOS == "windows" {
		return &Shell{Type: ShellPowerShell, Path: "powershell"}
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return &Shell{Type: DetectShellType(shell), Path: shell}
	}
	return &Shell{Type: ShellBash, Path: "/bin/bash"}
}

func DetectShellType(path string) ShellType {
	name := strings.ToLower(filepath.Base(path))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "bash", "sh":
		return ShellBash
	case "zsh":
		return ShellZsh
	case "powershell", "pwsh":
		return ShellPowerShell
	case "cmd":
		return ShellCmd
	default:
		return ShellUnknown
	}
}

func (s *Shell) DeriveExecArgs(command string, useLoginShell bool) []string {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		s = NewDefaultShell()
	}
	switch s.Type {
	case ShellPowerShell:
		args := []string{s.Path}
		if !useLoginShell {
			args = append(args, "-NoProfile")
		}
		return append(args, "-Command", command)
	case ShellCmd:
		return []string{s.Path, "/c", command}
	case ShellZsh:
		if useLoginShell {
			return []string{s.Path, "-lc", command}
		}
		return []string{s.Path, "-c", command}
	default:
		if useLoginShell {
			return []string{s.Path, "-lc", command}
		}
		return []string{s.Path, "-c", command}
	}
}

func ResolveCommand(args *ExecCommandArgs, sessionShell *Shell, allowLoginShell bool) (*ResolvedCommand, error) {
	return ResolveCommandWithOptions(args, sessionShell, CommandResolutionOptions{AllowLoginShell: allowLoginShell})
}

func ResolveCommandWithOptions(args *ExecCommandArgs, sessionShell *Shell, opts CommandResolutionOptions) (*ResolvedCommand, error) {
	if args == nil {
		return nil, errors.New("exec command args are required")
	}
	if strings.TrimSpace(args.Cmd) == "" {
		return nil, errors.New("cmd must not be empty")
	}
	useLoginShell := opts.AllowLoginShell
	if args.Login != nil {
		if *args.Login && !opts.AllowLoginShell {
			return nil, errors.New("login shell is disabled by config; omit login or set it to false")
		}
		useLoginShell = *args.Login
	}
	if opts.ShellMode == UnifiedExecShellModeZshFork {
		if strings.TrimSpace(args.Shell) != "" {
			return nil, errors.New("`shell` is not supported for local zsh-fork exec; omit `shell` to use zsh-fork, or target a remote environment where `shell` is supported.")
		}
		shell := opts.ZshForkShell
		if shell == nil {
			shell = sessionShell
		}
		if shell == nil || shell.Type != ShellZsh || strings.TrimSpace(shell.Path) == "" {
			shell = &Shell{Type: ShellZsh, Path: "zsh"}
		}
		return &ResolvedCommand{
			Command:   shell.DeriveExecArgs(args.Cmd, useLoginShell),
			ShellType: ShellZsh,
		}, nil
	}
	shell := sessionShell
	if strings.TrimSpace(args.Shell) != "" {
		// A model-provided shell path only selects the shell type; the local
		// executable is resolved through normal discovery (Rust #39607).
		if shellType := DetectShellType(args.Shell); shellType != ShellUnknown {
			shell = ResolveShellByType(shellType)
		} else {
			shell = nil
		}
	}
	if shell == nil {
		shell = NewDefaultShell()
	}
	return &ResolvedCommand{
		Command:   shell.DeriveExecArgs(args.Cmd, useLoginShell),
		ShellType: shell.Type,
	}, nil
}

// ResolveShellByType discovers the local executable for a shell type
// (shell/command.go GetShell) and rejects unresolvable types.
func ResolveShellByType(shellType ShellType) *Shell {
	if detected := shellutil.GetShell(shellutil.ShellType(shellType)); detected != nil {
		return &Shell{Type: shellType, Path: detected.ShellPath}
	}
	return nil
}

func BuildShellRequest(args *ExecCommandArgs, sessionShell *Shell, opts ShellValidationOptions) (*ShellRequest, error) {
	if args == nil {
		return nil, errors.New("exec command args are required")
	}
	justificationPresent := args.justificationSet || args.Justification != ""
	sandboxPermissionsPresent := args.sandboxPermissionsSet || args.SandboxPermissions != ""
	if justificationPresent && !sandboxPermissionsPresent {
		return nil, errors.New("`justification` requires an explicit `sandbox_permissions`; use `sandbox_permissions: \"require_escalated\"` for unsandboxed execution, or omit `justification`.")
	}
	cwd, err := resolveShellRequestCWD(opts.CWD, firstNonEmptyString(args.CWD, args.Workdir))
	if err != nil {
		return nil, err
	}
	resolved, err := ResolveCommandWithOptions(args, sessionShell, CommandResolutionOptions{
		AllowLoginShell: opts.AllowLoginShell,
		ShellMode:       opts.ShellMode,
		ZshForkShell:    opts.ZshForkShell,
	})
	if err != nil {
		return nil, err
	}
	if err := validateWindowsShellSafety(args.Cmd, resolved.ShellType, cwd); err != nil {
		return nil, err
	}
	if resolved.ShellType == ShellPowerShell {
		resolved.Command = shellutil.PrefixPowerShellScriptWithUTF8(resolved.Command)
	}
	sandboxPermissions := args.SandboxPermissions
	if sandboxPermissions == "" {
		sandboxPermissions = sandbox.SandboxPermissionsUseDefault
	}
	sandboxPermissions = sandbox.SandboxPermissionsPreservingDeniedReads(sandboxPermissions, opts.PermissionProfile)
	additionalPermissions, err := sandbox.NormalizeAndValidateAdditionalPermissions(
		opts.AdditionalPermissionsAllowed,
		opts.ApprovalPolicy,
		sandboxPermissions,
		args.AdditionalPermissions,
		opts.PermissionsPreapproved,
		cwd,
	)
	if err != nil {
		return nil, err
	}
	permissionProfileID, permissionProfile, err := effectiveShellPermissionProfile(
		opts.PermissionProfileID,
		opts.PermissionProfile,
		sandboxPermissions,
		additionalPermissions,
		opts.PermissionsPreapproved,
	)
	if err != nil {
		return nil, err
	}
	sandboxProfile := buildShellSandboxProfile(permissionProfile, additionalPermissions, cwd)
	escalationApprovalRequired := RequestsSandboxOverride(sandboxPermissions) && !opts.PermissionsPreapproved
	if escalationApprovalRequired {
		// Rust #40024: use the shared policy rejection check so granular
		// sandbox_approval controls whether escalation may prompt.
		if _, rejected := safety.PromptRejectedByPolicy(shellApprovalPolicy(&opts) /*promptIsRule*/, false); rejected {
			return nil, fmt.Errorf("approval policy is %s; reject command - you cannot ask for escalated permissions if the approval policy is %s", opts.ApprovalPolicy, opts.ApprovalPolicy)
		}
	}
	// Rust #39159: commands with dynamic shell words (brace expansion, globs,
	// escapes, expansions) require approval under unless-trusted even when a
	// policy allow rule matches the unexpanded source text.
	policyApprovalRequired := opts.ApprovalPolicy == sandbox.ApprovalUnlessTrusted &&
		(!opts.PermissionsPreapproved || shellutil.ShellCommandHasDynamicWords(args.Cmd))
	approvalRequired := escalationApprovalRequired || policyApprovalRequired
	approvalReason := shellApprovalReason(args)
	if policyApprovalRequired && strings.TrimSpace(approvalReason) == "" {
		approvalReason = "approval required by policy"
	}
	yieldTimeMS := args.YieldTimeMS
	if yieldTimeMS == 0 {
		yieldTimeMS = DefaultExecYieldTimeMS
	}
	timeoutMS := opts.DefaultTimeoutMS
	if args.TimeoutMS != 0 {
		timeoutMS = args.TimeoutMS
	}
	return &ShellRequest{
		Command:                         resolved.Command,
		HookCommand:                     args.Cmd,
		CWD:                             cwd,
		TimeoutMS:                       timeoutMS,
		YieldTimeMS:                     yieldTimeMS,
		MaxOutputTokens:                 args.MaxOutputTokens,
		Env:                             mergeEnv(opts.Env, args.Env),
		TTY:                             args.TTY,
		SandboxPermissions:              sandboxPermissions,
		AdditionalPermissions:           additionalPermissions,
		SandboxProfile:                  sandboxProfile,
		PermissionProfileID:             permissionProfileID,
		PermissionProfile:               permissionProfile,
		WindowsSandboxLevel:             opts.WindowsSandboxLevel,
		WindowsSandboxPrivateDesktop:    opts.WindowsSandboxPrivateDesktop,
		WindowsSandboxProxySettingsMode: opts.WindowsSandboxProxySettingsMode,
		ApprovalPolicy:                  opts.ApprovalPolicy,
		EnforceManagedNetwork:           opts.EnforceManagedNetwork,
		ManagedNetwork:                  cloneManagedNetworkSandboxContext(opts.ManagedNetwork),
		RemoteNetworkProxy:              opts.RemoteNetworkProxy,
		NetworkPolicyDecider:            opts.NetworkPolicyDecider,
		NetworkPolicyDecisionTimeout:    opts.NetworkPolicyDecisionTimeout,
		ApprovalRequired:                approvalRequired,
		ApprovalReason:                  approvalReason,
		Justification:                   args.Justification,
		PrefixRule:                      cloneStrings(args.PrefixRule),
	}, nil
}

// shellApprovalPolicy maps the shell validation approval policy + granular
// sub-config into a safety.ApprovalPolicy for the shared rejection check.
func shellApprovalPolicy(opts *ShellValidationOptions) safety.ApprovalPolicy {
	mode := safety.ApprovalMode("")
	if opts != nil {
		switch opts.ApprovalPolicy {
		case sandbox.ApprovalNever:
			mode = safety.ApprovalNever
		case sandbox.ApprovalOnRequest:
			mode = safety.ApprovalOnRequest
		case sandbox.ApprovalUnlessTrusted:
			mode = safety.ApprovalUnlessTrusted
		case sandbox.ApprovalGranular:
			mode = safety.ApprovalGranular
		}
	}
	if opts == nil {
		return safety.ApprovalPolicy{Mode: mode}
	}
	return safety.ApprovalPolicy{Mode: mode, Rules: opts.GranularRules, SandboxApproval: opts.GranularSandboxApproval}
}

func cloneManagedNetworkSandboxContext(value *network.ProxyManagedNetworkSandboxContext) *network.ProxyManagedNetworkSandboxContext {
	if value == nil {
		return nil
	}
	return &network.ProxyManagedNetworkSandboxContext{
		LoopbackPorts:     append([]uint16(nil), value.LoopbackPorts...),
		AllowLocalBinding: value.AllowLocalBinding,
	}
}

func effectiveShellPermissionProfile(profileID string, profile *sandbox.PermissionProfile, permissions sandbox.SandboxPermissions, additional *sandbox.AdditionalPermissionProfile, preapproved bool) (string, *sandbox.PermissionProfile, error) {
	if permissions == sandbox.SandboxPermissionsRequireEscalated && preapproved {
		fullAccess := sandbox.FullAccessPermissionProfile()
		return sandbox.BuiltInPermissionProfileDangerFullAccess, &fullAccess, nil
	}
	if profile == nil && additional == nil {
		return "", nil, nil
	}
	effective, err := sandbox.PermissionProfileWithAdditionalPermissions(profile, additional)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(profileID) == "" {
		profileID = "resolved"
	}
	return strings.TrimSpace(profileID), effective, nil
}

func RequestsSandboxOverride(permissions sandbox.SandboxPermissions) bool {
	return permissions == sandbox.SandboxPermissionsRequireEscalated ||
		permissions == sandbox.SandboxPermissionsWithAdditionalPermissions
}

func shellApprovalReason(args *ExecCommandArgs) string {
	if args == nil {
		return ""
	}
	if strings.TrimSpace(args.Justification) != "" {
		return strings.TrimSpace(args.Justification)
	}
	switch args.SandboxPermissions {
	case sandbox.SandboxPermissionsRequireEscalated:
		return "command requested escalated sandbox permissions"
	case sandbox.SandboxPermissionsWithAdditionalPermissions:
		return "command requested additional sandbox permissions"
	default:
		return ""
	}
}

func buildShellSandboxProfile(profile *sandbox.PermissionProfile, additional *sandbox.AdditionalPermissionProfile, cwd string) *ShellSandboxProfile {
	if profile == nil && additional == nil {
		return nil
	}
	if profile == nil {
		profile = &sandbox.PermissionProfile{SandboxPolicy: sandbox.NewWorkspaceWritePolicy()}
	}
	policy := profile.LegacySandboxPolicy()
	out := &ShellSandboxProfile{
		PolicyTag:             sandbox.SandboxPolicyTag(policy, cwd),
		NetworkEnabled:        profile.AllowsNetwork(),
		AdditionalPermissions: cloneAdditionalPermissions(additional),
	}
	if policy != nil {
		for _, root := range policy.GetWritableRootsWithCWD(cwd) {
			out.WritableRoots = append(out.WritableRoots, root.Root)
		}
	}
	if additional != nil {
		if additional.Network != nil && *additional.Network {
			out.NetworkEnabled = true
		}
		out.WritableRoots = mergeStringSet(out.WritableRoots, additional.FileSystem)
	}
	return out
}

func resolveShellRequestCWD(base string, override string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", errors.New("cwd must not be empty")
	}
	cwd, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	override = strings.TrimSpace(override)
	if override != "" {
		cwd = override
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(base, cwd)
		}
		cwd, err = filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
	}
	return filepath.Clean(cwd), nil
}

func cloneEnv(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeEnv(base map[string]string, overrides map[string]string) map[string]string {
	if base == nil && overrides == nil {
		return nil
	}
	out := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overrides {
		out[key] = value
	}
	return out
}

func cloneAdditionalPermissions(in *sandbox.AdditionalPermissionProfile) *sandbox.AdditionalPermissionProfile {
	if in == nil {
		return nil
	}
	out := &sandbox.AdditionalPermissionProfile{FileSystem: append([]string(nil), in.FileSystem...)}
	if in.Network != nil {
		value := *in.Network
		out.Network = &value
	}
	return out
}

func mergeStringSet(base []string, extra []string) []string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make([]string, 0, len(base)+len(extra))
	seen := map[string]bool{}
	for _, value := range base {
		value = filepath.Clean(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	for _, value := range extra {
		value = filepath.Clean(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
