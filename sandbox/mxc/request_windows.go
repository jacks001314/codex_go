//go:build windows

package mxc

import (
	"errors"
	"fmt"
	"path/filepath"

	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

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
