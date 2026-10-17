package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"codex_go/tool"
)

// multiEnvCopyFileTurnProvider resolves the two environments the copy calls use,
// so a call that silently falls back to the host filesystem cannot satisfy it.
type multiEnvCopyFileTurnProvider struct {
	source *remoteEnvironmentFileSystemFake
	target *remoteEnvironmentFileSystemFake
}

func (p multiEnvCopyFileTurnProvider) FileSystemFor(environmentID string) (tool.EnvironmentFileSystem, bool) {
	switch environmentID {
	case "remote-a":
		return p.source, true
	case "remote-b":
		return p.target, true
	}
	return nil, false
}

func multiEnvCopyFileTurnInvocation(t *testing.T, sourcePath string, targetPath string) *tool.Invocation {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"source_environment_id": "remote-a",
		"source_path":           sourcePath,
		"target_environment_id": "remote-b",
		"target_path":           targetPath,
	})
	if err != nil {
		t.Fatalf("marshal copy arguments: %v", err)
	}
	return &tool.Invocation{
		CallID:   "call-copy",
		ToolName: tool.PlainName(tool.MultiEnvCopyFileToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: string(encoded)},
	}
}

// TestBuildToolRegistryGatesMultiEnvCopyFileLikeRust proves the host gate of
// Rust #54741: the file-copy tool reaches the model-visible surface only when
// the default-off `multi_env_copy_file` feature is enabled, and it stays
// advertised whether or not the turn selected an environment
// (codex-rs/ext/multi-env-copy-file/tests/suite/mod.rs::copy_file_is_opt_in).
func TestBuildToolRegistryGatesMultiEnvCopyFileLikeRust(t *testing.T) {
	source := newRemoteEnvironmentFileSystemFake("/remote-a")
	target := newRemoteEnvironmentFileSystemFake("/remote-b")
	payload := []byte("payload that must not enter model context\n")
	sourceKey := source.key("/remote-a/source.bin")
	targetKey := target.key("/remote-b/copied.bin")
	source.files[sourceKey] = append([]byte(nil), payload...)

	options := environmentFileSystemTestRegistryOptions(t, source)
	options.EnvironmentFileSystems = multiEnvCopyFileTurnProvider{source: source, target: target}

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	if _, ok := registry.Spec(tool.PlainName(tool.MultiEnvCopyFileToolName)); ok {
		t.Fatal("multi_env_copy_file is advertised while the feature is off")
	}

	options.EnableMultiEnvCopyFile = true
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(enabled) error = %v", err)
	}
	spec, ok := registry.Spec(tool.PlainName(tool.MultiEnvCopyFileToolName))
	if !ok {
		t.Fatal("multi_env_copy_file is missing from the registry while the feature is on")
	}
	if !strings.HasPrefix(spec.Description, "Copy a file between two different environments") {
		t.Fatalf("spec description = %q", spec.Description)
	}
	executor, ok := registry.Lookup(tool.PlainName(tool.MultiEnvCopyFileToolName))
	if !ok {
		t.Fatal("registry lookup missed the copy executor")
	}
	output, err := executor.Execute(context.Background(), multiEnvCopyFileTurnInvocation(t, "/remote-a/source.bin", "/remote-b/copied.bin"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := fmt.Sprintf(`{"bytes_copied":%d}`, len(payload)); output.Body != want {
		t.Fatalf("output body = %q, want %q", output.Body, want)
	}
	if copied := target.files[targetKey]; !bytes.Equal(copied, payload) {
		t.Fatalf("target environment holds %d bytes, want %d identical bytes", len(copied), len(payload))
	}
	if len(target.writes) != 1 || target.writes[0] != targetKey {
		t.Fatalf("target writes = %v, want [%s]", target.writes, targetKey)
	}

	// Rust keeps the tool visible with no selected environment at all.
	withoutEnvironments := DefaultToolRegistryOptions(t.TempDir())
	withoutEnvironments.EnableMultiEnvCopyFile = true
	registry, err = BuildToolRegistry(withoutEnvironments)
	if err != nil {
		t.Fatalf("BuildToolRegistry(no environments) error = %v", err)
	}
	if _, ok := registry.Spec(tool.PlainName(tool.MultiEnvCopyFileToolName)); !ok {
		t.Fatal("multi_env_copy_file must stay advertised without a selected environment")
	}
}
