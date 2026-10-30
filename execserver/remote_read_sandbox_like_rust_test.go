package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Rust #58195: unrestricted reads omit the sandbox context so older executors
// do not sandbox them, while deny-read entries and explicit paths that the
// executor must validate keep it.
func TestReadSandboxForPathLikeRust(t *testing.T) {
	writeOnly := &FileSystemSandboxContext{
		Permissions: json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"slash_tmp"}},"access":"write"}]},"network":"restricted"}`),
		CWD:         "/workspace",
	}
	if got, err := ReadSandboxForPath(context.Background(), nil, writeOnly, "/workspace/file.txt"); err != nil || got != nil {
		t.Fatalf("unrestricted read = %#v, %v; want context omitted", got, err)
	}

	denyRead := &FileSystemSandboxContext{
		Permissions: json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"path","path":"/workspace/secret"},"access":"deny"}]},"network":"restricted"}`),
		CWD:         "/workspace",
	}
	if got, err := ReadSandboxForPath(context.Background(), nil, denyRead, "/workspace/file.txt"); err != nil || got != denyRead {
		t.Fatalf("restricted read = %#v, %v; want context kept", got, err)
	}

	// A relative target cannot be matched against an explicit path's convention,
	// so the executor must validate the explicit path itself.
	explicitPath := &FileSystemSandboxContext{
		Permissions: json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"path","path":"/workspace/extra"},"access":"read"}]},"network":"restricted"}`),
		CWD:         "/workspace",
	}
	if got, err := ReadSandboxForPath(context.Background(), nil, explicitPath, "relative/file.txt"); err != nil || got != explicitPath {
		t.Fatalf("relative target = %#v, %v; want context kept", got, err)
	}

	if got, err := ReadSandboxForPath(context.Background(), nil, nil, "/workspace/file.txt"); err != nil || got != nil {
		t.Fatalf("nil sandbox = %#v, %v", got, err)
	}
}

func TestValidateRemoteMXCLikeRust(t *testing.T) {
	if err := ValidateRemoteMXC(&EnvironmentInfo{}); !errors.Is(err, ErrRemoteMXCUnsupported) {
		t.Fatalf("unsupported error = %v", err)
	}
	supported := &EnvironmentInfo{}
	supported.Capabilities.WindowsMxc = true
	if err := ValidateRemoteMXC(supported); err != nil {
		t.Fatalf("supported error = %v", err)
	}
	if !isRemoteMXCSelection(&FileSystemSandboxContext{WindowsSandboxLevel: "mxc"}) {
		t.Fatal("mxc selection not recognized")
	}
	if isRemoteMXCSelection(&FileSystemSandboxContext{WindowsSandboxLevel: "elevated"}) {
		t.Fatal("elevated selection treated as mxc")
	}
}
