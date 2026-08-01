package memories

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSeedExtensionInstructionsDoesNotOverwrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memories")
	if err := SeedExtensionInstructions(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ExtensionsRoot(root), "ad_hoc", "instructions.md")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != adHocInstructions {
		t.Fatalf("seeded instructions = %q, %v", data, err)
	}
	if err := os.WriteFile(path, []byte("custom instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SeedExtensionInstructions(root); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "custom instructions" {
		t.Fatalf("reseeded instructions = %q, %v", data, err)
	}
}

func TestPruneOldExtensionResourcesMatchesRustCutoff(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memories")
	chronicle := filepath.Join(ExtensionsRoot(root), "chronicle")
	resources := filepath.Join(chronicle, "resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chronicle, "instructions.md"), []byte("instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []string{
		"2026-04-06T11-59-59-abcd-10min-old.md",
		"2026-04-07T12-00-00-abcd-10min-cutoff.md",
		"2026-04-08T12-00-00-abcd-10min-recent.md",
		"not-a-timestamp.md",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(resources, name), []byte("resource"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ignored := filepath.Join(ExtensionsRoot(root), "ignored", "resources")
	if err := os.MkdirAll(ignored, 0o755); err != nil {
		t.Fatal(err)
	}
	ignoredOld := filepath.Join(ignored, files[0])
	if err := os.WriteFile(ignoredOld, []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	PruneOldExtensionResources(root, now)
	for _, name := range files[:2] {
		if _, err := os.Stat(filepath.Join(resources, name)); !os.IsNotExist(err) {
			t.Fatalf("old resource %s remains: %v", name, err)
		}
	}
	for _, name := range files[2:] {
		if _, err := os.Stat(filepath.Join(resources, name)); err != nil {
			t.Fatalf("retained resource %s missing: %v", name, err)
		}
	}
	if _, err := os.Stat(ignoredOld); err != nil {
		t.Fatalf("resource without instructions was pruned: %v", err)
	}
	parsed, ok := extensionResourceTimestamp(files[0])
	if !ok || parsed.Unix() != 1_775_476_799 {
		t.Fatalf("parsed timestamp = %v/%v", parsed, ok)
	}
}
