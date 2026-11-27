package exec

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

func resumePermissionTestConfig() *config.Config {
	return &config.Config{Values: map[string]any{"sandbox_mode": "workspace-write"}}
}

// Rust #62194: a resume restores the saved permission selection instead of
// applying the current process's default sandbox.
func TestResolveExecPermissionProfileRestoresSavedSelectionLikeRust(t *testing.T) {
	runner := &Runner{}
	req := &Request{}
	req.Exec.Shared.CWD = t.TempDir()
	cfg := resumePermissionTestConfig()

	restored, err := runner.resolveExecPermissionProfileForRun(cfg, req, &execResumePermissionState{
		Found:    true,
		ActiveID: sandbox.BuiltInPermissionProfileReadOnly,
	})
	if err != nil {
		t.Fatalf("restore error = %v", err)
	}
	if sandboxPermissionProfileID(restored) != sandbox.BuiltInPermissionProfileReadOnly {
		t.Fatalf("restored profile = %q, want read-only", sandboxPermissionProfileID(restored))
	}

	// Without a resume the configured default still applies.
	plain, err := runner.resolveExecPermissionProfileForRun(cfg, req, nil)
	if err != nil {
		t.Fatalf("plain resolution error = %v", err)
	}
	if sandboxPermissionProfileID(plain) != "workspace-write" {
		t.Fatalf("plain resolution = %q, want the workspace-write default", sandboxPermissionProfileID(plain))
	}

	// An explicit --sandbox selection wins over the saved one.
	req.Exec.Shared.Sandbox = sandbox.BuiltInPermissionProfileDangerFullAccess
	explicit, err := runner.resolveExecPermissionProfileForRun(cfg, req, &execResumePermissionState{
		Found:    true,
		ActiveID: sandbox.BuiltInPermissionProfileReadOnly,
	})
	if err != nil {
		t.Fatalf("explicit resolution error = %v", err)
	}
	if sandboxPermissionProfileID(explicit) != sandbox.BuiltInPermissionProfileDangerFullAccess {
		t.Fatalf("explicit profile = %q", sandboxPermissionProfileID(explicit))
	}
}

// An unnamed recorded profile is restored verbatim, and older histories without
// any recorded selection recover read-only instead of widening.
func TestResolveExecPermissionProfileRestoresUnnamedOrReadOnlyLikeRust(t *testing.T) {
	runner := &Runner{}
	req := &Request{}
	req.Exec.Shared.CWD = t.TempDir()
	cfg := resumePermissionTestConfig()

	raw, err := sandbox.RuntimePermissionProfileJSON(sandbox.ReadOnlyPermissionProfile())
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	unnamed, err := runner.resolveExecPermissionProfileForRun(cfg, req, &execResumePermissionState{Found: true, Unnamed: []byte(raw)})
	if err != nil {
		t.Fatalf("unnamed restore error = %v", err)
	}
	if profile := sandboxPermissionProfile(unnamed); profile == nil || profile.AllowsNetwork() {
		t.Fatalf("unnamed restore = %#v, want a restricted read-only profile", unnamed)
	}

	legacy, err := runner.resolveExecPermissionProfileForRun(cfg, req, &execResumePermissionState{})
	if err != nil {
		t.Fatalf("legacy restore error = %v", err)
	}
	if sandboxPermissionProfileID(legacy) != sandbox.BuiltInPermissionProfileReadOnly {
		t.Fatalf("legacy restore = %q, want read-only", sandboxPermissionProfileID(legacy))
	}

	if _, err := runner.resolveExecPermissionProfileForRun(cfg, req, &execResumePermissionState{Found: true, ActiveID: "deleted-profile"}); err == nil || !strings.Contains(err.Error(), "--sandbox") {
		t.Fatalf("missing named profile error = %v", err)
	}
}

// --add-dir appends the requested directories to the workspace-write roots.
func TestResolveExecPermissionProfileAppendsAddDirsLikeRust(t *testing.T) {
	runner := &Runner{}
	cwd := t.TempDir()
	external := t.TempDir()
	req := &Request{}
	req.Exec.Shared.CWD = cwd
	req.Exec.Shared.AddDirs = []string{"relative-dir", external, external}
	cfg := resumePermissionTestConfig()

	resolution, err := runner.resolveExecPermissionProfileForRun(cfg, req, nil)
	if err != nil {
		t.Fatalf("resolution error = %v", err)
	}
	profile := sandboxPermissionProfile(resolution)
	if profile == nil {
		t.Fatalf("resolution = %#v", resolution)
	}
	raw, err := sandbox.RuntimePermissionProfileJSON(*profile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	// JSON escapes Windows separators; compare against the decoded path text.
	unescaped := strings.ReplaceAll(raw, `\\`, `\`)
	for _, want := range []string{cwd, filepath.Join(cwd, "relative-dir"), external} {
		if !strings.Contains(unescaped, want) {
			t.Fatalf("permission profile missing --add-dir root %q:\n%s", want, raw)
		}
	}
	var decoded struct {
		FileSystem struct {
			Entries []struct {
				Path struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"path"`
			} `json:"entries"`
		} `json:"file_system"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("Unmarshal permission profile error = %v", err)
	}
	directRootCount := 0
	for _, entry := range decoded.FileSystem.Entries {
		if entry.Path.Type == "path" && entry.Path.Path == external {
			directRootCount++
		}
	}
	if directRootCount != 1 {
		t.Fatalf("--add-dir duplicated a root:\n%s", raw)
	}
}
