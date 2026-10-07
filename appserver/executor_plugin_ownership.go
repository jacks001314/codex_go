package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	execserverclient "codex_go/execserver"
)

// maxPluginManifestAncestorProbes bounds the ancestor walk while probing for a
// plugin manifest, matching the existing executor-skill ancestor probing limit.
const maxPluginManifestAncestorProbes = 64

// execServerFSNotExistCode mirrors fsOperationFailure's os.IsNotExist
// classification on the exec-server wire (-32004).
const execServerFSNotExistCode = -32004

// PluginRootOwnership classifies a selected capability root independently of
// whether its manifest can be parsed (Rust #51491 PluginRootOwnership).
type PluginRootOwnership string

const (
	PluginRootOwnershipPlugin     PluginRootOwnership = "plugin"
	PluginRootOwnershipStandalone PluginRootOwnership = "standalone"
)

// ErrExecutorPluginDiscoveryFailed reports that capability discovery failed for
// a selected root. A discovery failure is not evidence that the root is
// standalone, so it is surfaced as an error instead.
var ErrExecutorPluginDiscoveryFailed = errors.New("discovery failed for selected capability root")

// pluginManifestProbe reports whether a discoverable plugin manifest exists at
// path. It must return a non-nil error for a metadata failure (which must not be
// read as "absent"); a missing path is reported as (false, nil).
type pluginManifestProbe func(path string) (bool, error)

// executorPluginRootOwnership classifies a selected root before capability
// parsing. Discovery results are conclusive when available; otherwise the
// selected root and its ancestors are probed for a discoverable plugin manifest.
// Nested plugin manifests never establish ownership of their parent root.
func executorPluginRootOwnership(rootID string, rootPath string, discovery *execserverclient.CapabilityRootDiscovery, probe pluginManifestProbe) (PluginRootOwnership, error) {
	rootKey := remoteNormalizePathKey(rootPath)
	if discovery != nil {
		if discovery.Error != nil {
			return "", fmt.Errorf("%w: %s", ErrExecutorPluginDiscoveryFailed, strings.TrimSpace(rootID))
		}
		if discovery.Plugin != nil {
			return PluginRootOwnershipPlugin, nil
		}
		for _, manifest := range discovery.NamespaceManifests {
			pluginRoot, _, _, ok := remotePluginRootFromManifestPath(manifest.Path)
			if ok && remotePathAtOrBelow(rootKey, remoteNormalizePathKey(pluginRoot)) {
				return PluginRootOwnershipPlugin, nil
			}
		}
		// A discovery that completed without warnings is conclusive evidence that
		// the root is not plugin-owned.
		if len(discovery.Warnings) == 0 {
			return PluginRootOwnershipStandalone, nil
		}
	}
	if rootKey == "" {
		return "", errors.New("capability root path is required to classify plugin ownership")
	}
	manifestPath, err := findPluginManifestPath(rootKey, probe)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(manifestPath) != "" {
		return PluginRootOwnershipPlugin, nil
	}
	return PluginRootOwnershipStandalone, nil
}

// findPluginManifestPath probes a root and its ancestors for a discoverable
// plugin manifest, preserving ancestor order and, within each root, the manifest
// priority order shared with plugin resolution (Rust find_manifest).
func findPluginManifestPath(rootPath string, probe pluginManifestProbe) (string, error) {
	if probe == nil {
		return "", errors.New("plugin manifest probe is required")
	}
	current := remoteNormalizePathKey(rootPath)
	for index := 0; current != "" && index < maxPluginManifestAncestorProbes; index++ {
		for _, relativePath := range remoteDiscoverablePluginManifestPaths {
			candidate := remoteJoin(current, relativePath)
			exists, err := probe(candidate)
			if err != nil {
				return "", err
			}
			if exists {
				return candidate, nil
			}
		}
		parent := remoteNormalizePathKey(remoteSkillDir(current))
		if parent == "" || parent == current {
			break
		}
		current = parent
	}
	return "", nil
}

// remotePathAtOrBelow reports whether path is root itself or a descendant. Rust
// compares PathUri::starts_with with the manifest's grandparent directory, so a
// nested plugin manifest cannot claim its parent root.
func remotePathAtOrBelow(path string, root string) bool {
	if path == "" || root == "" {
		return false
	}
	root = strings.TrimSuffix(root, "/")
	return path == root || strings.HasPrefix(path, root+"/")
}

// remotePluginManifestProbe adapts a remote environment filesystem caller into a
// manifest probe. Missing paths continue the ancestor walk; any other metadata
// failure aborts the classification instead of being read as "standalone".
func remotePluginManifestProbe(ctx context.Context, caller remoteEnvironmentFSCaller, nextID *int) pluginManifestProbe {
	return func(path string) (bool, error) {
		metadata, err := getRemoteEnvironmentMetadata(ctx, caller, nextID, path)
		if err != nil {
			if remoteFSNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to inspect capability manifest %s: %w", path, err)
		}
		return metadata != nil && metadata.IsFile, nil
	}
}

// remoteFSNotExist reports whether a remote filesystem error means the entry is
// missing, across both the client and websocket transports.
func remoteFSNotExist(err error) bool {
	if err == nil {
		return false
	}
	if execserverclient.IsFSNotExistError(err) {
		return true
	}
	var rpcErr *execServerRPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == execServerFSNotExistCode
}
