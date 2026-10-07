package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	execserverclient "codex_go/execserver"
)

func pluginOwnershipStringPtr(value string) *string { return &value }

// ownershipProbeFromFiles records probe order and reports which candidate paths
// exist, mirroring Rust's SyntheticPluginFileSystem.
func ownershipProbeFromFiles(files map[string]bool, order *[]string) pluginManifestProbe {
	return func(path string) (bool, error) {
		if order != nil {
			*order = append(*order, path)
		}
		return files[remoteNormalizePathKey(path)], nil
	}
}

// TestExecutorPluginRootOwnershipSurvivesMalformedAncestorManifestsLikeRust mirrors
// Rust executor_provider_tests::root_ownership_survives_malformed_ancestor_manifests:
// a malformed manifest still marks the root plugin-owned, because ownership is
// classified before capability parsing.
func TestExecutorPluginRootOwnershipSurvivesMalformedAncestorManifestsLikeRust(t *testing.T) {
	root := "file:///tmp/plugin-package/skills"
	ctx := context.Background()

	standalone, err := executorPluginRootOwnership("example@marketplace", root, nil, ownershipProbeFromFiles(nil, nil))
	if err != nil || standalone != PluginRootOwnershipStandalone {
		t.Fatalf("ownership = %q, err = %v, want standalone", standalone, err)
	}

	// The ancestor manifest exists but its contents are malformed; ownership must
	// not depend on parsing it.
	manifest := remoteJoin("file:///tmp/plugin-package", ".codex-plugin/plugin.json")
	pluginOwned, err := executorPluginRootOwnership("example@marketplace", root, nil, ownershipProbeFromFiles(map[string]bool{remoteNormalizePathKey(manifest): true}, nil))
	if err != nil || pluginOwned != PluginRootOwnershipPlugin {
		t.Fatalf("ownership = %q, err = %v, want plugin", pluginOwned, err)
	}

	// A discovery that flagged a warning is not conclusive, so probing still runs.
	discovery := &execserverclient.CapabilityRootDiscovery{
		ID:       "example@marketplace",
		Path:     root,
		Warnings: []string{"unreadable manifest"},
	}
	pluginOwned, err = executorPluginRootOwnership("example@marketplace", root, discovery, ownershipProbeFromFiles(map[string]bool{remoteNormalizePathKey(manifest): true}, nil))
	if err != nil || pluginOwned != PluginRootOwnershipPlugin {
		t.Fatalf("ownership = %q, err = %v, want plugin", pluginOwned, err)
	}

	// Discovery failure is never evidence of standalone ownership.
	discovery.Error = pluginOwnershipStringPtr("discovery failed")
	if _, err := executorPluginRootOwnership("example@marketplace", root, discovery, ownershipProbeFromFiles(nil, nil)); !errors.Is(err, ErrExecutorPluginDiscoveryFailed) {
		t.Fatalf("discovery error = %v, want ErrExecutorPluginDiscoveryFailed", err)
	}
	_ = ctx
}

// TestExecutorPluginRootOwnershipUsesAncestorsNotNestedPluginsLikeRust mirrors
// Rust executor_provider_tests::discovery_ownership_uses_ancestors_not_nested_plugins.
func TestExecutorPluginRootOwnershipUsesAncestorsNotNestedPluginsLikeRust(t *testing.T) {
	root := "file:///tmp/selected-root"
	discovery := &execserverclient.CapabilityRootDiscovery{
		ID:   "example@marketplace",
		Path: root,
		NamespaceManifests: []execserverclient.CapabilityTextFile{{
			Path: remoteJoin(root, "nested/.codex-plugin/plugin.json"),
		}},
	}
	// A nested plugin manifest must not claim its parent root.
	ownership, err := executorPluginRootOwnership("example@marketplace", root, discovery, ownershipProbeFromFiles(nil, nil))
	if err != nil || ownership != PluginRootOwnershipStandalone {
		t.Fatalf("ownership = %q, err = %v, want standalone", ownership, err)
	}
	discovery.NamespaceManifests[0].Path = remoteJoin(root, ".codex-plugin/plugin.json")
	ownership, err = executorPluginRootOwnership("example@marketplace", root, discovery, ownershipProbeFromFiles(nil, nil))
	if err != nil || ownership != PluginRootOwnershipPlugin {
		t.Fatalf("ownership = %q, err = %v, want plugin", ownership, err)
	}
}

// TestExecutorPluginRootOwnershipDiscoveryIsConclusiveLikeRust pins the
// conclusive discovery branches and that a complete discovery without warnings
// is enough to declare a root standalone.
func TestExecutorPluginRootOwnershipDiscoveryIsConclusiveLikeRust(t *testing.T) {
	root := "file:///tmp/selected-root"
	plugin := &execserverclient.CapabilityRootDiscovery{ID: "id", Path: root, Plugin: &execserverclient.DiscoveredPluginFiles{}}
	ownership, err := executorPluginRootOwnership("id", root, plugin, nil)
	if err != nil || ownership != PluginRootOwnershipPlugin {
		t.Fatalf("ownership = %q, err = %v, want plugin", ownership, err)
	}
	complete := &execserverclient.CapabilityRootDiscovery{ID: "id", Path: root}
	ownership, err = executorPluginRootOwnership("id", root, complete, nil)
	if err != nil || ownership != PluginRootOwnershipStandalone {
		t.Fatalf("ownership = %q, err = %v, want standalone", ownership, err)
	}
}

// TestFindPluginManifestPathPreservesProbePriorityLikeRust mirrors Rust's
// ancestor_manifest_probes_pipeline_and_preserve_error_order: ancestors are
// probed in order, each manifest marker in priority order, and an earlier
// candidate's error or result cannot be outranked by a later one.
func TestFindPluginManifestPathPreservesProbePriorityLikeRust(t *testing.T) {
	root := "file:///a/b/c/d/e/f"
	owner := "file:///a/b/c"
	ownerManifest := remoteJoin(owner, ".codex-plugin/plugin.json")
	earlierCandidate := remoteJoin(root, ".codex-plugin/plugin.json")

	order := []string{}
	found, err := findPluginManifestPath(root, ownershipProbeFromFiles(map[string]bool{remoteNormalizePathKey(ownerManifest): true}, &order))
	if err != nil || found != ownerManifest {
		t.Fatalf("manifest = %q, err = %v, want %q", found, err, ownerManifest)
	}
	if len(order) == 0 || order[0] != earlierCandidate {
		t.Fatalf("first probe = %#v, want the selected root's first marker", order)
	}
	if order[len(order)-1] != ownerManifest {
		t.Fatalf("last probe = %q, want the owner manifest", order[len(order)-1])
	}
	// Ancestor order is preserved: the deeper ancestor precedes its parent.
	deeper := remoteJoin("file:///a/b/c/d/e", ".codex-plugin/plugin.json")
	shallower := remoteJoin("file:///a/b/c/d", ".codex-plugin/plugin.json")
	if indexOfString(order, deeper) >= indexOfString(order, shallower) {
		t.Fatalf("ancestor probe order = %#v", order)
	}

	// An earlier candidate's error aborts even though a later candidate exists.
	errProbe := func(path string) (bool, error) {
		if remoteNormalizePathKey(path) == remoteNormalizePathKey(earlierCandidate) {
			return false, errors.New("permission denied")
		}
		return remoteNormalizePathKey(path) == remoteNormalizePathKey(ownerManifest), nil
	}
	if _, err := findPluginManifestPath(root, errProbe); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("earlier error = %v, want it to abort the probe", err)
	}

	// A later candidate's error cannot outrank an earlier manifest hit.
	lateErrProbe := func(path string) (bool, error) {
		switch remoteNormalizePathKey(path) {
		case remoteNormalizePathKey(ownerManifest):
			return true, nil
		case remoteNormalizePathKey(remoteJoin(owner, ".claude-plugin/plugin.json")):
			return false, errors.New("permission denied")
		}
		return false, nil
	}
	found, err = findPluginManifestPath(root, lateErrProbe)
	if err != nil || found != ownerManifest {
		t.Fatalf("manifest = %q, err = %v, want %q", found, err, ownerManifest)
	}
}

// TestExecutorPluginRootOwnershipProbeFailureIsNotStandaloneLikeRust pins Rust's
// central invariant: a metadata failure returns an error instead of being read
// as evidence that the root is standalone.
func TestExecutorPluginRootOwnershipProbeFailureIsNotStandaloneLikeRust(t *testing.T) {
	probe := func(string) (bool, error) { return false, errors.New("permission denied") }
	ownership, err := executorPluginRootOwnership("id", "file:///root", nil, probe)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("ownership = %q, err = %v, want the probe failure", ownership, err)
	}
	if ownership != "" {
		t.Fatalf("ownership = %q, want no classification on failure", ownership)
	}
	if _, err := findPluginManifestPath("file:///root", nil); err == nil {
		t.Fatal("a missing probe was accepted")
	}
}

// remoteMetadataCaller answers fs/getMetadata with either a payload or an error.
type remoteMetadataCaller struct {
	files map[string]bool
	err   error
}

func (c remoteMetadataCaller) Call(_ context.Context, _ int, method string, params any) (json.RawMessage, error) {
	if method != "fs/getMetadata" {
		return nil, fmt.Errorf("unsupported method %s", method)
	}
	values, _ := params.(map[string]any)
	path, _ := values["path"].(string)
	if c.err != nil {
		return nil, c.err
	}
	return json.Marshal(remoteFSGetMetadataResponse{IsFile: c.files[remoteNormalizePathKey(path)]})
}

// TestRemotePluginManifestProbeClassifiesMissingPathsLikeRust pins the remote
// probe: a missing entry continues the ancestor walk, while any other metadata
// failure aborts it (Rust maps io::ErrorKind::NotFound the same way).
func TestRemotePluginManifestProbeClassifiesMissingPathsLikeRust(t *testing.T) {
	ctx := context.Background()
	missing := &execServerRPCError{RequestID: 1, Code: execServerFSNotExistCode, Message: "no such file"}
	nextID := 1
	exists, err := remotePluginManifestProbe(ctx, remoteMetadataCaller{err: missing}, &nextID)("file:///a/.codex-plugin/plugin.json")
	if err != nil || exists {
		t.Fatalf("missing manifest = (%v, %v), want (false, nil)", exists, err)
	}
	// The websocket transport error and the wrapped os error both classify.
	if !remoteFSNotExist(fmt.Errorf("wrapped: %w", &execServerRPCError{Code: execServerFSNotExistCode})) {
		t.Fatal("wrapped websocket not-found was not classified")
	}
	if !remoteFSNotExist(fmt.Errorf("wrapped: %w", os.ErrNotExist)) {
		t.Fatal("wrapped os.ErrNotExist was not classified")
	}
	if !execserverclient.IsFSNotExistError(fmt.Errorf("wrapped: %w", os.ErrNotExist)) {
		t.Fatal("execserver.IsFSNotExistError missed an os.ErrNotExist")
	}
	if execserverclient.IsFSNotExistError(fmt.Errorf("wrapped: %w", &execServerRPCError{Code: -32603})) {
		t.Fatal("a non-NotFound code was classified as missing")
	}

	nextID = 1
	exists, err = remotePluginManifestProbe(ctx, remoteMetadataCaller{err: fmt.Errorf("permission denied")}, &nextID)("file:///a/.codex-plugin/plugin.json")
	if err == nil || exists {
		t.Fatalf("failed metadata = (%v, %v), want an error", exists, err)
	}

	nextID = 1
	probe := remotePluginManifestProbe(ctx, remoteMetadataCaller{files: map[string]bool{remoteNormalizePathKey("file:///a/.codex-plugin/plugin.json"): true}}, &nextID)
	exists, err = probe("file:///a/.codex-plugin/plugin.json")
	if err != nil || !exists {
		t.Fatalf("present manifest = (%v, %v), want (true, nil)", exists, err)
	}
}

func indexOfString(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}
	return -1
}
