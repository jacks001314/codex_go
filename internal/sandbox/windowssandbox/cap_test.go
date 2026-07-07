package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "github.com/goccy/go-json"
)

func TestLoadOrCreateCapabilitySIDsCreatesAndReloads(t *testing.T) {
	codexHome := t.TempDir()
	caps, err := LoadOrCreateCapabilitySIDs(codexHome)
	if err != nil {
		t.Fatalf("LoadOrCreateCapabilitySIDs() error = %v", err)
	}
	if !looksLikeCapabilitySID(caps.Workspace) || !looksLikeCapabilitySID(caps.Readonly) {
		t.Fatalf("LoadOrCreateCapabilitySIDs() = %#v, want capability SID strings", caps)
	}
	if caps.Workspace == caps.Readonly {
		t.Fatalf("workspace and readonly SIDs should be distinct: %q", caps.Workspace)
	}

	reloaded, err := LoadOrCreateCapabilitySIDs(codexHome)
	if err != nil {
		t.Fatalf("LoadOrCreateCapabilitySIDs() reload error = %v", err)
	}
	if reloaded.Workspace != caps.Workspace || reloaded.Readonly != caps.Readonly {
		t.Fatalf("reload = %#v, want stable %#v", reloaded, caps)
	}
}

func TestLoadOrCreateCapabilitySIDsMigratesLegacySingleSID(t *testing.T) {
	codexHome := t.TempDir()
	const legacy = "S-1-5-21-1-2-3-4"
	if err := os.WriteFile(CapSIDFile(codexHome), []byte(" \n"+legacy+"\n"), 0o600); err != nil {
		t.Fatalf("write legacy cap_sid: %v", err)
	}

	caps, err := LoadOrCreateCapabilitySIDs(codexHome)
	if err != nil {
		t.Fatalf("LoadOrCreateCapabilitySIDs() error = %v", err)
	}
	if caps.Workspace != legacy {
		t.Fatalf("Workspace = %q, want legacy %q", caps.Workspace, legacy)
	}
	if !looksLikeCapabilitySID(caps.Readonly) {
		t.Fatalf("Readonly = %q, want generated capability SID", caps.Readonly)
	}

	data, err := os.ReadFile(CapSIDFile(codexHome))
	if err != nil {
		t.Fatalf("read migrated cap_sid: %v", err)
	}
	var migrated CapabilitySIDs
	if err := json.Unmarshal(data, &migrated); err != nil {
		t.Fatalf("cap_sid was not migrated to JSON: %q: %v", data, err)
	}
	if migrated.Workspace != legacy {
		t.Fatalf("migrated Workspace = %q, want %q", migrated.Workspace, legacy)
	}
}

func TestWorkspaceCapabilitySIDForCWDDeduplicatesCanonicalPath(t *testing.T) {
	codexHome := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	first, err := WorkspaceCapabilitySIDForCWD(codexHome, workspace)
	if err != nil {
		t.Fatalf("WorkspaceCapabilitySIDForCWD() error = %v", err)
	}
	second, err := WorkspaceCapabilitySIDForCWD(codexHome, filepath.Join(workspace, "."))
	if err != nil {
		t.Fatalf("WorkspaceCapabilitySIDForCWD() alt error = %v", err)
	}
	if first != second {
		t.Fatalf("canonical equivalent cwd SID mismatch: %q != %q", first, second)
	}
	caps, err := LoadOrCreateCapabilitySIDs(codexHome)
	if err != nil {
		t.Fatalf("LoadOrCreateCapabilitySIDs() error = %v", err)
	}
	if len(caps.WorkspaceByCWD) != 1 {
		t.Fatalf("WorkspaceByCWD len = %d, want 1", len(caps.WorkspaceByCWD))
	}
}

func TestWorkspaceWriteCapabilitySIDForRootWithCWDUsesScopedSIDs(t *testing.T) {
	codexHome := t.TempDir()
	root := filepath.Join(t.TempDir(), "workspace")
	extra := filepath.Join(t.TempDir(), "extra")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := os.MkdirAll(extra, 0o700); err != nil {
		t.Fatalf("create extra: %v", err)
	}

	workspaceSID, err := WorkspaceWriteCapabilitySIDForRootWithCWD(codexHome, root, root)
	if err != nil {
		t.Fatalf("WorkspaceWriteCapabilitySIDForRootWithCWD(root) error = %v", err)
	}
	directWorkspaceSID, err := WorkspaceCapabilitySIDForCWD(codexHome, root)
	if err != nil {
		t.Fatalf("WorkspaceCapabilitySIDForCWD() error = %v", err)
	}
	if workspaceSID != directWorkspaceSID {
		t.Fatalf("workspace write SID = %q, want workspace SID %q", workspaceSID, directWorkspaceSID)
	}

	extraSID, err := WorkspaceWriteCapabilitySIDForRootWithCWD(codexHome, root, extra)
	if err != nil {
		t.Fatalf("WorkspaceWriteCapabilitySIDForRootWithCWD(extra) error = %v", err)
	}
	extraSIDAgain, err := WorkspaceWriteCapabilitySIDForRoot(codexHome, extra)
	if err != nil {
		t.Fatalf("WorkspaceWriteCapabilitySIDForRoot(extra) error = %v", err)
	}
	if extraSID != extraSIDAgain {
		t.Fatalf("extra root SID not stable: %q != %q", extraSID, extraSIDAgain)
	}
	if extraSID == workspaceSID {
		t.Fatalf("extra root SID should differ from workspace SID: %q", extraSID)
	}
}

func TestWorkspaceWriteRootPathHelpers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	child := filepath.Join(root, "child")
	sibling := filepath.Join(filepath.Dir(root), "workspace-other")

	if !WorkspaceWriteRootContainsPath(root, child) {
		t.Fatalf("expected root to contain child")
	}
	if WorkspaceWriteRootContainsPath(root, sibling) {
		t.Fatalf("sibling must not be treated as child")
	}
	if !WorkspaceWriteRootOverlapsPath(root, child) {
		t.Fatalf("expected root and child to overlap")
	}
	if WorkspaceWriteRootSpecificity(child) <= WorkspaceWriteRootSpecificity(root) {
		t.Fatalf("child should be more specific than root")
	}
}

func looksLikeCapabilitySID(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 8 {
		return false
	}
	return strings.Join(parts[:4], "-") == "S-1-5-21"
}
