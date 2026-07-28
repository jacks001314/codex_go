package windowssandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	coresandbox "codex_go/sandbox"
)

type ReadRootGrantRequest struct {
	PermissionProfile *coresandbox.PermissionProfile
	WorkspaceRoots    []string
	CommandCWD        string
	Env               map[string]string
	CodexHome         string
}

// GrantReadRootNonElevated refreshes the legacy Windows sandbox ACLs with one
// additional readable directory and returns the canonical directory path.
func GrantReadRootNonElevated(request *ReadRootGrantRequest, readRoot string) (string, error) {
	return grantReadRootNonElevated(request, readRoot, RunSetupRefreshForRequest)
}

func grantReadRootNonElevated(request *ReadRootGrantRequest, readRoot string, refresh func(*SandboxSetupRequest) error) (string, error) {
	readRoot = strings.TrimSpace(readRoot)
	if !filepath.IsAbs(readRoot) {
		return "", fmt.Errorf("path must be absolute: %s", readRoot)
	}
	info, err := os.Stat(readRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("path does not exist: %s", readRoot)
		}
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path must be a directory: %s", readRoot)
	}
	canonicalRoot, err := filepath.EvalSymlinks(readRoot)
	if err != nil {
		return "", err
	}
	canonicalRoot = filepath.Clean(canonicalRoot)
	if request == nil {
		return "", ErrInvalidRequest
	}
	permissions, err := ResolvePermissions(request.PermissionProfile, request.WorkspaceRoots)
	if err != nil {
		// Rust treats unsupported profiles as a no-op after validating the path.
		return canonicalRoot, nil
	}
	readRoots := GatherReadRoots(request.CommandCWD, permissions, request.Env, request.CodexHome)
	readRoots = append(readRoots, canonicalRoot)
	if refresh == nil {
		return "", ErrInvalidRequest
	}
	err = refresh(&SandboxSetupRequest{
		CodexHome:   request.CodexHome,
		CommandCWD:  request.CommandCWD,
		Env:         request.Env,
		Permissions: permissions,
		Overrides: SetupRootOverrides{
			ReadRoots:     readRoots,
			ReadRootsSet:  true,
			WriteRoots:    []string{},
			WriteRootsSet: true,
		},
	})
	if err != nil {
		return "", err
	}
	return canonicalRoot, nil
}
