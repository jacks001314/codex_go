package windowssandbox

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	coresandbox "codex_go/internal/sandbox"
	json "github.com/goccy/go-json"
)

const (
	CodexWindowsSandboxArg1 = "--run-as-windows-sandbox"

	commandCWDFlag                       = "--command-cwd"
	codexHomeFlag                        = "--codex-home"
	denyReadPathsJSONFlag                = "--deny-read-paths-json"
	denyWritePathsJSONFlag               = "--deny-write-paths-json"
	envJSONFlag                          = "--env-json"
	permissionProfileFlag                = "--permission-profile"
	privateDesktopFlag                   = "--windows-sandbox-private-desktop"
	preserveProxySettingsFlag            = "--preserve-proxy-settings"
	proxyEnforcedFlag                    = "--proxy-enforced"
	readRootsIncludePlatformDefaultsFlag = "--read-roots-include-platform-defaults"
	readRootsJSONFlag                    = "--read-roots-json"
	sandboxLevelFlag                     = "--windows-sandbox-level"
	writeRootsJSONFlag                   = "--write-roots-json"
	workspaceRootFlag                    = "--workspace-root"
)

type WindowsSandboxLevel string

const (
	WindowsSandboxLevelDisabled        WindowsSandboxLevel = "disabled"
	WindowsSandboxLevelRestrictedToken WindowsSandboxLevel = "restricted-token"
	WindowsSandboxLevelElevated        WindowsSandboxLevel = "elevated"
)

type WindowsSandboxCommandArgsRequest struct {
	Command                          []string
	CommandCWD                       string
	WorkspaceRoots                   []string
	Env                              map[string]string
	PermissionProfile                *coresandbox.PermissionProfile
	WindowsSandboxLevel              WindowsSandboxLevel
	WindowsSandboxPrivateDesktop     bool
	ProxyEnforced                    bool
	ProxySettingsMode                ProxySettingsMode
	ReadRootsOverride                []string
	ReadRootsOverrideSet             bool
	ReadRootsIncludePlatformDefaults bool
	WriteRootsOverride               []string
	WriteRootsOverrideSet            bool
	DenyReadPathsOverride            []string
	DenyWritePathsOverride           []string
	CodexHome                        string
}

type WindowsSandboxWrapperRequest struct {
	WindowsSandboxCommandArgsRequest
}

func CreateWindowsSandboxCommandArgsForPermissionProfile(req WindowsSandboxCommandArgsRequest) ([]string, error) {
	if err := req.validateForArgs(); err != nil {
		return nil, err
	}
	permissionProfileJSON, err := json.Marshal(req.PermissionProfile)
	if err != nil {
		return nil, err
	}
	envJSON, err := json.Marshal(req.Env)
	if err != nil {
		return nil, err
	}
	args := []string{
		CodexWindowsSandboxArg1,
		codexHomeFlag,
		req.CodexHome,
		commandCWDFlag,
		req.CommandCWD,
		permissionProfileFlag,
		string(permissionProfileJSON),
		envJSONFlag,
		string(envJSON),
		sandboxLevelFlag,
		string(req.WindowsSandboxLevel),
	}
	workspaceRoots := req.WorkspaceRoots
	if len(workspaceRoots) == 0 {
		workspaceRoots = []string{req.CommandCWD}
	}
	for _, root := range workspaceRoots {
		args = append(args, workspaceRootFlag, root)
	}
	if req.WindowsSandboxPrivateDesktop {
		args = append(args, privateDesktopFlag)
	}
	if req.ProxyEnforced {
		args = append(args, proxyEnforcedFlag)
	}
	if req.ProxySettingsMode == ProxySettingsPreserve {
		args = append(args, preserveProxySettingsFlag)
	}
	if req.ReadRootsOverrideSet {
		args, err = pushJSONArg(args, readRootsJSONFlag, req.ReadRootsOverride)
		if err != nil {
			return nil, err
		}
	}
	if req.ReadRootsIncludePlatformDefaults {
		args = append(args, readRootsIncludePlatformDefaultsFlag)
	}
	if req.WriteRootsOverrideSet {
		args, err = pushJSONArg(args, writeRootsJSONFlag, req.WriteRootsOverride)
		if err != nil {
			return nil, err
		}
	}
	if len(req.DenyReadPathsOverride) > 0 {
		args, err = pushJSONArg(args, denyReadPathsJSONFlag, req.DenyReadPathsOverride)
		if err != nil {
			return nil, err
		}
	}
	if len(req.DenyWritePathsOverride) > 0 {
		args, err = pushJSONArg(args, denyWritePathsJSONFlag, req.DenyWritePathsOverride)
		if err != nil {
			return nil, err
		}
	}
	args = append(args, "--")
	args = append(args, cloneStrings(req.Command)...)
	return args, nil
}

func ParseWindowsSandboxWrapperArgs(args []string) (*WindowsSandboxWrapperRequest, error) {
	req := WindowsSandboxCommandArgsRequest{
		ProxySettingsMode: ProxySettingsReconcile,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case CodexWindowsSandboxArg1:
			continue
		case codexHomeFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			req.CodexHome, i = value, next
		case commandCWDFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			req.CommandCWD, i = value, next
		case workspaceRootFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			req.WorkspaceRoots, i = append(req.WorkspaceRoots, value), next
		case envJSONFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(value), &req.Env); err != nil {
				return nil, fmt.Errorf("failed to parse env json: %w", err)
			}
			i = next
		case permissionProfileFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			var profile coresandbox.PermissionProfile
			if err := json.Unmarshal([]byte(value), &profile); err != nil {
				return nil, fmt.Errorf("failed to parse permission profile: %w", err)
			}
			req.PermissionProfile = &profile
			i = next
		case sandboxLevelFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			level, err := ParseWindowsSandboxLevel(value)
			if err != nil {
				return nil, err
			}
			req.WindowsSandboxLevel, i = level, next
		case privateDesktopFlag:
			req.WindowsSandboxPrivateDesktop = true
		case proxyEnforcedFlag:
			req.ProxyEnforced = true
		case preserveProxySettingsFlag:
			req.ProxySettingsMode = ProxySettingsPreserve
		case readRootsIncludePlatformDefaultsFlag:
			req.ReadRootsIncludePlatformDefaults = true
		case readRootsJSONFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(value), &req.ReadRootsOverride); err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", arg, err)
			}
			req.ReadRootsOverrideSet, i = true, next
		case writeRootsJSONFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(value), &req.WriteRootsOverride); err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", arg, err)
			}
			req.WriteRootsOverrideSet, i = true, next
		case denyReadPathsJSONFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(value), &req.DenyReadPathsOverride); err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", arg, err)
			}
			i = next
		case denyWritePathsJSONFlag:
			value, next, err := nextFlagValue(args, i, arg)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(value), &req.DenyWritePathsOverride); err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", arg, err)
			}
			i = next
		case "--":
			req.Command = cloneStrings(args[i+1:])
			i = len(args)
		default:
			return nil, fmt.Errorf("unexpected windows sandbox wrapper argument: %s", arg)
		}
	}
	if err := req.validateParsed(); err != nil {
		return nil, err
	}
	return &WindowsSandboxWrapperRequest{WindowsSandboxCommandArgsRequest: req}, nil
}

func ParseWindowsSandboxLevel(value string) (WindowsSandboxLevel, error) {
	switch WindowsSandboxLevel(strings.TrimSpace(value)) {
	case WindowsSandboxLevelDisabled:
		return WindowsSandboxLevelDisabled, nil
	case WindowsSandboxLevelRestrictedToken:
		return WindowsSandboxLevelRestrictedToken, nil
	case WindowsSandboxLevelElevated:
		return WindowsSandboxLevelElevated, nil
	default:
		return "", fmt.Errorf("invalid windows sandbox level: %s", value)
	}
}

func RunWindowsSandboxWrapperMain(args []string) int {
	exitCode, err := RunWindowsSandboxWrapperExitCode(args, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return exitCode
}

func RunWindowsSandboxWrapper(args []string) error {
	exitCode, err := RunWindowsSandboxWrapperExitCode(args, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return &WrapperExitError{ExitCode: exitCode}
	}
	return nil
}

type WrapperExitError struct {
	ExitCode int
}

func (e *WrapperExitError) Error() string {
	return fmt.Sprintf("windows sandbox command exited with status %d", e.ExitCode)
}

func RunWindowsSandboxWrapperExitCode(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) (int, error) {
	req, err := ParseWindowsSandboxWrapperArgs(args)
	if err != nil {
		return 1, err
	}
	return RunWindowsSandboxWrapperRequest(req, stdin, stdout, stderr)
}

func RunWindowsSandboxWrapperRequest(req *WindowsSandboxWrapperRequest, stdin io.Reader, stdout io.Writer, stderr io.Writer) (int, error) {
	if req == nil {
		return 1, ErrInvalidRequest
	}
	if len(req.Command) == 0 {
		return 1, fmt.Errorf("%w: command is required", ErrInvalidRequest)
	}
	switch req.WindowsSandboxLevel {
	case WindowsSandboxLevelDisabled:
		return runWrapperCommandDirect(req, stdin, stdout, stderr)
	case WindowsSandboxLevelRestrictedToken:
		result, err := RunWindowsSandboxCaptureWithFilesystemOverrides(wrapperCaptureRequest(req))
		if err != nil {
			return 1, err
		}
		return writeCaptureResult(result, stdout, stderr)
	case WindowsSandboxLevelElevated:
		result, err := RunWindowsSandboxCaptureForPermissionProfileElevated(&ElevatedSandboxProfileCaptureRequest{Capture: *wrapperCaptureRequest(req)})
		if err != nil {
			return 1, err
		}
		return writeCaptureResult(result, stdout, stderr)
	default:
		return 1, fmt.Errorf("invalid windows sandbox level: %s", req.WindowsSandboxLevel)
	}
}

func wrapperCaptureRequest(req *WindowsSandboxWrapperRequest) *CaptureRequest {
	return &CaptureRequest{
		PermissionProfile:                req.PermissionProfile,
		WorkspaceRoots:                   cloneStrings(req.WorkspaceRoots),
		CodexHome:                        req.CodexHome,
		Command:                          cloneStrings(req.Command),
		CWD:                              req.CommandCWD,
		Env:                              cloneEnv(req.Env),
		UsePrivateDesktop:                req.WindowsSandboxPrivateDesktop,
		ProxyEnforced:                    req.ProxyEnforced,
		ProxySettingsMode:                req.ProxySettingsMode,
		ReadRootsOverride:                cloneStrings(req.ReadRootsOverride),
		ReadRootsOverrideSet:             req.ReadRootsOverrideSet,
		ReadRootsIncludePlatformDefaults: req.ReadRootsIncludePlatformDefaults,
		WriteRootsOverride:               cloneStrings(req.WriteRootsOverride),
		WriteRootsOverrideSet:            req.WriteRootsOverrideSet,
		DenyReadPaths:                    cloneStrings(req.DenyReadPathsOverride),
		DenyWritePaths:                   cloneStrings(req.DenyWritePathsOverride),
	}
}

func writeCaptureResult(result *CaptureResult, stdout io.Writer, stderr io.Writer) (int, error) {
	if result == nil {
		return 1, fmt.Errorf("windows sandbox backend returned nil result")
	}
	if stdout != nil && len(result.Stdout) > 0 {
		if _, err := stdout.Write(result.Stdout); err != nil {
			return 1, err
		}
	}
	if stderr != nil && len(result.Stderr) > 0 {
		if _, err := stderr.Write(result.Stderr); err != nil {
			return 1, err
		}
	}
	return result.ExitCode, nil
}

func runWrapperCommandDirect(req *WindowsSandboxWrapperRequest, stdin io.Reader, stdout io.Writer, stderr io.Writer) (int, error) {
	cmd := exec.Command(req.Command[0], req.Command[1:]...)
	cmd.Dir = req.CommandCWD
	cmd.Env = envSlice(req.Env)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return 1, err
}

func envSlice(envMap map[string]string) []string {
	if envMap == nil {
		return os.Environ()
	}
	out := make([]string, 0, len(envMap))
	for key, value := range envMap {
		out = append(out, key+"="+value)
	}
	return out
}

func pushJSONArg(args []string, flag string, value any) ([]string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(args, flag, string(data)), nil
}

func nextFlagValue(args []string, index int, flag string) (string, int, error) {
	next := index + 1
	if next >= len(args) {
		return "", index, fmt.Errorf("missing value for %s", flag)
	}
	return args[next], next, nil
}

func (r *WindowsSandboxCommandArgsRequest) validateForArgs() error {
	if r == nil {
		return ErrInvalidRequest
	}
	if len(r.Command) == 0 {
		return fmt.Errorf("%w: command is required", ErrInvalidRequest)
	}
	if r.PermissionProfile == nil {
		return fmt.Errorf("%w: permission profile is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.CommandCWD) == "" {
		return fmt.Errorf("%w: command cwd is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.CodexHome) == "" {
		return fmt.Errorf("%w: codex home is required", ErrInvalidRequest)
	}
	if r.WindowsSandboxLevel == "" {
		r.WindowsSandboxLevel = WindowsSandboxLevelRestrictedToken
	}
	if r.ProxySettingsMode == "" {
		r.ProxySettingsMode = ProxySettingsReconcile
	}
	return nil
}

func (r *WindowsSandboxCommandArgsRequest) validateParsed() error {
	if err := r.validateForArgs(); err != nil {
		return err
	}
	if !isWindowsSandboxAbs(r.CodexHome) {
		return fmt.Errorf("%s must be absolute: %s", codexHomeFlag, r.CodexHome)
	}
	if !isWindowsSandboxAbs(r.CommandCWD) {
		return fmt.Errorf("%s must be absolute: %s", commandCWDFlag, r.CommandCWD)
	}
	for _, root := range r.WorkspaceRoots {
		if !isWindowsSandboxAbs(root) {
			return fmt.Errorf("%s must be absolute: %s", workspaceRootFlag, root)
		}
	}
	if len(r.WorkspaceRoots) == 0 {
		r.WorkspaceRoots = []string{filepath.Clean(r.CommandCWD)}
	}
	return nil
}
