package tool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codex_go/internal/sandbox"
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
}

type ShellRequest struct {
	Command               []string
	HookCommand           string
	CWD                   string
	TimeoutMS             uint64
	YieldTimeMS           uint64
	MaxOutputTokens       *int
	Env                   map[string]string
	TTY                   bool
	SandboxPermissions    sandbox.SandboxPermissions
	AdditionalPermissions *sandbox.AdditionalPermissionProfile
	SandboxProfile        *ShellSandboxProfile
	PermissionProfileID   string
	PermissionProfile     *sandbox.PermissionProfile
	ApprovalRequired      bool
	ApprovalReason        string
	Justification         string
	PrefixRule            []string
}

type ShellSandboxProfile struct {
	PolicyTag             string
	NetworkEnabled        bool
	WritableRoots         []string
	AdditionalPermissions *sandbox.AdditionalPermissionProfile
}

type ShellResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
	TimedOut bool
}

type ShellValidationOptions struct {
	AdditionalPermissionsAllowed bool
	ApprovalPolicy               sandbox.AskForApproval
	PermissionsPreapproved       bool
	AllowLoginShell              bool
	CWD                          string
	Env                          map[string]string
	DefaultTimeoutMS             uint64
	PermissionProfileID          string
	PermissionProfile            *sandbox.PermissionProfile
}

type ResolvedCommand struct {
	Command   []string
	ShellType ShellType
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
		args := []string{s.Path, "-NoLogo", "-NoProfile"}
		if useLoginShell {
			args = []string{s.Path, "-NoLogo"}
		}
		return append(args, "-Command", command)
	case ShellCmd:
		return []string{s.Path, "/C", command}
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
	if args == nil {
		return nil, errors.New("exec command args are required")
	}
	if strings.TrimSpace(args.Cmd) == "" {
		return nil, errors.New("cmd must not be empty")
	}
	useLoginShell := allowLoginShell
	if args.Login != nil {
		if *args.Login && !allowLoginShell {
			return nil, errors.New("login shell is disabled by config; omit login or set it to false")
		}
		useLoginShell = *args.Login
	}
	shell := sessionShell
	if strings.TrimSpace(args.Shell) != "" {
		shell = &Shell{Type: DetectShellType(args.Shell), Path: args.Shell}
	}
	if shell == nil {
		shell = NewDefaultShell()
	}
	return &ResolvedCommand{
		Command:   shell.DeriveExecArgs(args.Cmd, useLoginShell),
		ShellType: shell.Type,
	}, nil
}

func BuildShellRequest(args *ExecCommandArgs, sessionShell *Shell, opts ShellValidationOptions) (*ShellRequest, error) {
	if args == nil {
		return nil, errors.New("exec command args are required")
	}
	cwd, err := resolveShellRequestCWD(opts.CWD, firstNonEmptyString(args.CWD, args.Workdir))
	if err != nil {
		return nil, err
	}
	resolved, err := ResolveCommand(args, sessionShell, opts.AllowLoginShell)
	if err != nil {
		return nil, err
	}
	if err := validateWindowsShellSafety(args.Cmd, resolved.ShellType, cwd); err != nil {
		return nil, err
	}
	sandboxPermissions := args.SandboxPermissions
	if sandboxPermissions == "" {
		sandboxPermissions = sandbox.SandboxPermissionsUseDefault
	}
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
	permissionProfileID, permissionProfile := effectiveShellPermissionProfile(
		opts.PermissionProfileID,
		opts.PermissionProfile,
		sandboxPermissions,
		additionalPermissions,
		opts.PermissionsPreapproved,
	)
	sandboxProfile := buildShellSandboxProfile(permissionProfile, additionalPermissions, cwd)
	approvalRequired := RequestsSandboxOverride(sandboxPermissions) && !opts.PermissionsPreapproved
	if approvalRequired && opts.ApprovalPolicy != sandbox.ApprovalOnRequest {
		return nil, fmt.Errorf("approval policy is %s; reject command - you cannot ask for escalated permissions if the approval policy is %s", opts.ApprovalPolicy, opts.ApprovalPolicy)
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
		Command:               resolved.Command,
		HookCommand:           args.Cmd,
		CWD:                   cwd,
		TimeoutMS:             timeoutMS,
		YieldTimeMS:           yieldTimeMS,
		MaxOutputTokens:       args.MaxOutputTokens,
		Env:                   mergeEnv(opts.Env, args.Env),
		TTY:                   args.TTY,
		SandboxPermissions:    sandboxPermissions,
		AdditionalPermissions: additionalPermissions,
		SandboxProfile:        sandboxProfile,
		PermissionProfileID:   permissionProfileID,
		PermissionProfile:     permissionProfile,
		ApprovalRequired:      approvalRequired,
		ApprovalReason:        shellApprovalReason(args),
		Justification:         args.Justification,
		PrefixRule:            cloneStrings(args.PrefixRule),
	}, nil
}

func effectiveShellPermissionProfile(profileID string, profile *sandbox.PermissionProfile, permissions sandbox.SandboxPermissions, additional *sandbox.AdditionalPermissionProfile, preapproved bool) (string, *sandbox.PermissionProfile) {
	if permissions == sandbox.SandboxPermissionsRequireEscalated && preapproved {
		fullAccess := sandbox.FullAccessPermissionProfile()
		return sandbox.BuiltInPermissionProfileDangerFullAccess, &fullAccess
	}
	if profile == nil && additional == nil {
		return "", nil
	}
	effective := cloneShellPermissionProfile(profile)
	if effective == nil {
		workspace := sandbox.WorkspaceWritePermissionProfile()
		effective = &workspace
	}
	applyAdditionalPermissionsToShellProfile(effective, additional)
	if strings.TrimSpace(profileID) == "" {
		profileID = "resolved"
	}
	return strings.TrimSpace(profileID), effective
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

func cloneShellPermissionProfile(profile *sandbox.PermissionProfile) *sandbox.PermissionProfile {
	if profile == nil {
		return nil
	}
	clone := *profile
	if profile.SandboxPolicy != nil {
		policy := *profile.SandboxPolicy
		policy.WritableRoots = append([]string(nil), profile.SandboxPolicy.WritableRoots...)
		clone.SandboxPolicy = &policy
	}
	return &clone
}

func applyAdditionalPermissionsToShellProfile(profile *sandbox.PermissionProfile, additional *sandbox.AdditionalPermissionProfile) {
	if profile == nil || additional == nil || profile.Disabled {
		return
	}
	if additional.Network != nil && *additional.Network {
		profile.NetworkEnabled = true
		policy := profile.LegacySandboxPolicy()
		switch policy.Kind {
		case "external-sandbox":
			policy.ExternalNetwork = sandbox.NetworkEnabled
		default:
			policy.NetworkAccess = true
		}
		profile.SandboxPolicy = policy
	}
	if len(additional.FileSystem) == 0 {
		return
	}
	policy := profile.LegacySandboxPolicy()
	if policy.HasFullDiskWriteAccess() {
		profile.SandboxPolicy = policy
		return
	}
	if policy.Kind != sandbox.SandboxWorkspaceWrite {
		policy = sandbox.NewWorkspaceWritePolicy()
	}
	policy.WritableRoots = mergeStringSet(policy.WritableRoots, additional.FileSystem)
	profile.SandboxPolicy = policy
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
