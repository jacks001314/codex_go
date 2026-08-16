package tea

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"codex_go/sandbox"
)

// Rust 73abda8bfe (#38830): external editor buffers can contain the current
// composer text and must not be placed in directories exposed as writable by a
// restricted filesystem policy. Editor buffer files are created under a
// protected <codex_home>/editor directory, trying the configured Codex home,
// the default Codex home, and a workspace fallback, rejecting candidates that
// overlap writable roots or resolve through symbolic links.
const (
	editorDirectoryName  = "editor"
	editorDirectoryPerm  = 0o700
	errEditorWritable    = "editor directory must not be writable"
	errEditorSymlink     = "editor directory must not contain symbolic links"
	errEditorNoCandidate = "editor directory has no usable candidate"
)

// EditorDirectory resolves a protected directory for external editor buffer
// files (Rust editor_directory). candidateHomes are tried in order; the first
// home whose <home>/editor directory is not writable under the filesystem
// policy is created (0o700) and returned. Policies with full-disk write
// access skip the writable-root rejection.
func EditorDirectory(candidateHomes []string, policy *sandbox.SandboxPolicy, cwd string) (string, error) {
	writableRoots := policy.GetWritableRootsWithCWD(cwd)
	var lastErr error
	rejectedWritable := false
	for _, candidateHome := range candidateHomes {
		if strings.TrimSpace(candidateHome) == "" {
			lastErr = errors.New("editor directory has no parent")
			continue
		}
		canonicalHome, err := canonicalizeAllowMissing(candidateHome)
		if err != nil {
			lastErr = err
			continue
		}
		editorDir := filepath.Join(canonicalHome, editorDirectoryName)
		logicalEditorDir := filepath.Join(candidateHome, editorDirectoryName)
		if !policy.HasFullDiskWriteAccess() && editorDirectoryIsWritable(logicalEditorDir, editorDir, writableRoots) {
			lastErr = errors.New(errEditorWritable)
			rejectedWritable = true
			continue
		}
		if err := os.MkdirAll(editorDir, editorDirectoryPerm); err != nil {
			lastErr = err
			continue
		}
		canonical, err := filepath.EvalSymlinks(editorDir)
		if err != nil {
			lastErr = err
			continue
		}
		if !sameCleanPath(canonical, editorDir) {
			lastErr = errors.New(errEditorSymlink)
			continue
		}
		return editorDir, nil
	}
	if rejectedWritable {
		return "", errors.New(errEditorWritable)
	}
	if lastErr == nil {
		lastErr = errors.New(errEditorNoCandidate)
	}
	return "", lastErr
}

// editorDirectoryIsWritable reports whether the editor directory (or its
// parent) is exposed as writable by the resolved writable roots, or whether a
// writable root itself resolves inside the editor directory (Rust
// can_write_path_with_cwd / root.root.starts_with(directory)).
func editorDirectoryIsWritable(logicalDir string, canonicalDir string, roots []sandbox.WritableRoot) bool {
	for _, root := range roots {
		if root.IsPathWritable(canonicalDir) || root.IsPathWritable(logicalDir) {
			return true
		}
		if pathWithin(root.Root, canonicalDir) {
			return true
		}
	}
	return false
}

// canonicalizeAllowMissing resolves path to an absolute canonical form. When
// the path does not exist yet, the nearest existing ancestor is canonicalized
// and the missing tail is appended (Rust canonicalize with NotFound handling).
func canonicalizeAllowMissing(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return canonical, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return "", err
	}
	canonicalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(canonicalParent, filepath.Base(absolute)), nil
}

func sameCleanPath(a string, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func pathWithin(child string, parent string) bool {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}
