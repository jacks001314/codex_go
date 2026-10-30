package execserver

// Remote read sandbox compatibility with older executors.
//
// Rust parity: codex-rs/exec-server/src/remote_file_system.rs::read_sandbox and
// sandbox_selection.rs::validate_remote_mxc (#58195). Older executors sandbox
// any supplied policy, even an unrestricted one, so an explicitly selected MXC
// Windows sandbox must be rejected before it reaches them.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/utils"
)

// ErrRemoteMXCUnsupported is returned when the caller requests the MXC Windows
// sandbox from an executor that does not advertise windows_mxc support.
var ErrRemoteMXCUnsupported = errors.New("exec-server does not support the requested MXC Windows sandbox; update the executor")

// ValidateRemoteMXC mirrors Rust validate_remote_mxc.
func ValidateRemoteMXC(info *EnvironmentInfo) error {
	if info == nil || !info.Capabilities.WindowsMxc {
		return ErrRemoteMXCUnsupported
	}
	return nil
}

func isRemoteMXCSelection(sandbox *FileSystemSandboxContext) bool {
	return sandbox != nil && strings.EqualFold(strings.TrimSpace(sandbox.WindowsSandboxLevel), windowsSandboxLevelMxc)
}

// ReadSandboxForPath returns the sandbox context to send with a remote read.
// Reads that need enforcement keep their context; unrestricted reads drop it
// unless an explicit permission path cannot be validated for the target path
// convention, in which case the executor must validate it.
func ReadSandboxForPath(ctx context.Context, client *Client, sandbox *FileSystemSandboxContext, path string) (*FileSystemSandboxContext, error) {
	if sandbox == nil {
		return nil, nil
	}
	requires, _, _, _, _, err := prepareFSSandboxForOperation(sandbox, MethodFSReadFile)
	if err != nil {
		// Keep a malformed context so the executor reports it exactly as before.
		return sandbox, nil
	}
	keep := requires
	if !keep {
		convention, ok := readTargetConvention(path)
		for _, entry := range explicitPermissionPaths(sandbox.Permissions) {
			if !ok || utils.ValidateConfigPathText(entry, convention) != nil {
				keep = true
				break
			}
		}
	}
	if !keep {
		return nil, nil
	}
	if isRemoteMXCSelection(sandbox) {
		info, err := client.EnvironmentInfo(ctx)
		if err != nil {
			return nil, err
		}
		if err := ValidateRemoteMXC(info); err != nil {
			return nil, err
		}
	}
	return sandbox, nil
}

func readTargetConvention(path string) (utils.PathConvention, bool) {
	legacy := utils.NewLegacyAppPathString(strings.TrimSpace(path))
	return legacy.InferAbsolutePathConvention()
}

// explicitPermissionPaths returns the literal paths of managed restricted
// entries. Glob and special entries are validated executor-side already.
func explicitPermissionPaths(raw json.RawMessage) []string {
	var wire struct {
		Type       string `json:"type"`
		FileSystem struct {
			Type    string `json:"type"`
			Entries []struct {
				Path struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"path"`
			} `json:"entries"`
		} `json:"file_system"`
		FileSystemCamel struct {
			Type    string `json:"type"`
			Entries []struct {
				Path struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"path"`
			} `json:"entries"`
		} `json:"fileSystem"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &wire) != nil {
		return nil
	}
	entries := wire.FileSystem.Entries
	fileSystemType := wire.FileSystem.Type
	if len(entries) == 0 && len(wire.FileSystemCamel.Entries) > 0 {
		entries = wire.FileSystemCamel.Entries
		fileSystemType = wire.FileSystemCamel.Type
	}
	if wire.Type != "managed" || fileSystemType != "restricted" {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Path.Type != "" && entry.Path.Type != "path" {
			continue
		}
		if strings.TrimSpace(entry.Path.Path) != "" {
			paths = append(paths, entry.Path.Path)
		}
	}
	return paths
}

// validateDiscoveryMXC checks a discover request's sandbox selections before it
// is sent to an executor (Rust #58195).
func (c *Client) validateDiscoveryMXC(ctx context.Context, sandboxes []*FileSystemSandboxContext) error {
	for _, sandbox := range sandboxes {
		if !isRemoteMXCSelection(sandbox) {
			continue
		}
		info, err := c.EnvironmentInfo(ctx)
		if err != nil {
			return err
		}
		if err := ValidateRemoteMXC(info); err != nil {
			return err
		}
	}
	return nil
}
