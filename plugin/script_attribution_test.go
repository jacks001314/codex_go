package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedPluginRootsResolveRemotePluginScripts(t *testing.T) {
	home := t.TempDir()
	id, err := ParsePluginId("sample@openai-curated-remote")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewPluginStore(home)
	if err != nil {
		t.Fatal(err)
	}
	root := store.PluginRoot(id, "1.2.3")
	script := filepath.Join(root, "scripts", "run.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteRemotePluginID(id, "plugins~Plugin_sample"); err != nil {
		t.Fatal(err)
	}
	roots := NewTrustedPluginRoots(home, []string{id.Key()})
	shellScript := filepath.ToSlash(script)

	for _, command := range [][]string{
		{"python", "-u", script},
		{"sh", "-e", script},
		{"bash", "-lc", "python -u " + shellScript},
		{"pwsh.exe", "-NoProfile", "-Command", shellScript},
		{"cmd.exe", "/c", shellScript},
	} {
		got := roots.Resolve(command, root)
		if got == nil || got.PluginID != id.Key() || got.ScriptPath != "scripts/run.py" {
			t.Fatalf("Resolve(%q) = %#v", command, got)
		}
	}
}

func TestTrustedPluginRootsFailClosed(t *testing.T) {
	home := t.TempDir()
	id, _ := ParsePluginId("sample@openai-curated-remote")
	store, _ := NewPluginStore(home)
	root := store.PluginRoot(id, "1.2.3")
	script := filepath.Join(root, "scripts", "run.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := NewTrustedPluginRoots(home, []string{id.Key()}).Resolve([]string{"python", script}, root); got != nil {
		t.Fatalf("unverified remote plugin attributed as %#v", got)
	}
	if err := store.WriteRemotePluginID(id, "plugins~Plugin_sample"); err != nil {
		t.Fatal(err)
	}
	roots := NewTrustedPluginRoots(home, []string{id.Key()})
	for _, command := range [][]string{
		{"bash", "-lc", "python " + script + " && echo done"},
		{"python", "-m", "scripts.run"},
		{"python", filepath.Join(root, "scripts", "missing.py")},
	} {
		if got := roots.Resolve(command, root); got != nil {
			t.Fatalf("Resolve(%q) = %#v, want nil", command, got)
		}
	}

	localID, _ := ParsePluginId("local@openai-curated-remote")
	localRoot := store.PluginRoot(localID, DefaultPluginVersion)
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := NewTrustedPluginRoots(home, []string{localID.Key()}); len(got.roots) != 0 {
		t.Fatalf("local override roots = %#v", got.roots)
	}
}

func TestIsSafePluginRelativePath(t *testing.T) {
	if !IsSafePluginRelativePath("scripts/run.py") {
		t.Fatal("safe path rejected")
	}
	for _, path := range []string{"", "/tmp/run.py", "C:/run.py", "scripts/C:/run.py", `scripts\run.py`, "scripts//run.py", "scripts/./run.py", "scripts/../run.py"} {
		if IsSafePluginRelativePath(path) {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
}
