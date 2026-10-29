//go:build windows

package mxc

import (
	"errors"
	"fmt"
	"path/filepath"

	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

// NativeRequest is the portable projection of the request MXC's native
// launcher consumes. Go has no binding to the native SDK, so this type exists
// for policy parity and regression tests rather than for serialization.
type NativeRequest struct {
	// ScriptCode is the exact argv rendered with Windows CreateProcess
	// quoting; the launcher never re-parses it.
	ScriptCode string
	// WorkingDirectory is the helper's directory, which is deliberately
	// separate from the policy working directory.
	WorkingDirectory string
	// Env is the filtered child environment, or nil to inherit nothing.
	Env    []string
	Policy NativeContainerPolicy
}

// NativeContainerPolicy carries the kernel policy the native launcher applies.
type NativeContainerPolicy struct {
	Capabilities         []string
	ReadWritePaths       []string
	ReadOnlyPaths        []string
	DeniedPaths          []string
	NetworkEgress        NetworkEgressPolicy
	NetworkIngress       NetworkIngressPolicy
	NetworkSpecified     bool
	NetworkModeSpecified bool
	// UI keeps clipboard, input-injection, and system-control restrictions in
	// place. Only Win32k calls and desktop handles are required, and those come
	// from the base-process desktop isolation below (Rust #44903).
	UI            UIPolicy
	BaseProcessUI BaseProcessUI
}

// UIPolicy mirrors MXC's UiPolicy for the fields the helper sets explicitly.
// The remaining fields inherit the SDK defaults, which keep the restrictions.
type UIPolicy struct {
	Disable bool
}

// BaseProcessUI mirrors MXC's BaseProcessUiConfig. PowerShell needs desktop
// handles during DLL startup, so the helper isolates to a desktop.
type BaseProcessUI struct {
	Isolation string
}

// BuildRequest translates a decoded launcher request into the native request
// (Rust `policy::build_request`). The policy working directory comes from the
// request; the command directory comes from the helper's own cwd.
func BuildRequest(request *Request, commandCWD string, env map[string]string) (*NativeRequest, error) {
	if request == nil {
		return nil, errors.New("invalid MXC launcher request")
	}
	if len(request.Command) == 0 {
		return nil, errors.New("MXC command must not be empty")
	}
	if !filepath.IsAbs(request.SandboxPolicyCWD) {
		return nil, errors.New("MXC requires an absolute policy working directory")
	}
	if !filepath.IsAbs(commandCWD) {
		return nil, errors.New("MXC requires an absolute command working directory")
	}
	if err := ValidateManagedNetwork(request.ManagedNetwork); err != nil {
		return nil, err
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(request.Permissions))
	if err != nil {
		return nil, fmt.Errorf("MXC requires a managed permission profile: %w", err)
	}
	policy, err := PrepareFileSystemPolicy(profile, request.SandboxPolicyCWD, env, commandCWD)
	if err != nil {
		return nil, err
	}
	egress, ingress, err := TranslateNetworkPolicy(profile.AllowsNetwork(), request.ManagedNetwork)
	if err != nil {
		return nil, err
	}
	return &NativeRequest{
		ScriptCode:       windowssandbox.ArgvToCommandLine(request.Command),
		WorkingDirectory: commandCWD,
		Env:              envList(env),
		Policy: NativeContainerPolicy{
			Capabilities:         []string{"registryRead"},
			ReadWritePaths:       policy.ReadWritePaths,
			ReadOnlyPaths:        policy.ReadOnlyPaths,
			DeniedPaths:          policy.DeniedPaths,
			NetworkEgress:        egress,
			NetworkIngress:       ingress,
			NetworkSpecified:     true,
			NetworkModeSpecified: true,
			UI:                   UIPolicy{Disable: false},
			BaseProcessUI:        BaseProcessUI{Isolation: "desktop"},
		},
	}, nil
}
