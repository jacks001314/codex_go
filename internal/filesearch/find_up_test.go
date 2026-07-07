package filesearch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNearestAncestorWithMarkersFindsNearest(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte(""), 0o600); err != nil {
		t.Fatalf("write root marker: %v", err)
	}
	near := filepath.Join(root, "a", "b")
	if err := os.WriteFile(filepath.Join(near, "go.mod"), []byte("module x"), 0o600); err != nil {
		t.Fatalf("write near marker: %v", err)
	}
	got, ok, err := FindUpNearestAncestorWithMarkers(nested, []string{".git", "go.mod"}, FindUpPropagate)
	if err != nil || !ok || got != near {
		t.Fatalf("FindUpNearestAncestorWithMarkers() = %q, %v, %v", got, ok, err)
	}
}

func TestNearestAncestorErrorPolicy(t *testing.T) {
	boom := errors.New("boom")
	probe := func(path string) (os.FileInfo, error) {
		if filepath.Base(path) == "bad" {
			return nil, boom
		}
		return nil, os.ErrNotExist
	}
	_, _, err := FindUpNearestAncestor("x/y", &FindUpOptions{Markers: []string{"bad"}, Probe: probe, ErrorPolicy: FindUpPropagate})
	if !errors.Is(err, boom) {
		t.Fatalf("expected propagated error, got %v", err)
	}
	_, ok, err := FindUpNearestAncestor("x/y", &FindUpOptions{Markers: []string{"bad"}, Probe: probe, ErrorPolicy: FindUpIgnore})
	if err != nil || ok {
		t.Fatalf("ignore policy = %v, %v", ok, err)
	}
}

func TestFindUpAncestors(t *testing.T) {
	got := FindUpAncestors(filepath.Join("a", "b", "c"))
	if len(got) < 3 || got[0] != filepath.Clean(filepath.Join("a", "b", "c")) {
		t.Fatalf("unexpected ancestors: %#v", got)
	}
}
