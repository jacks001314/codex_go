package scripts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filename))
}

// TestBwrapPrebuiltSourcesCoverLinuxTargets pins the fallback that lets a
// Windows or macOS release host stage the sandbox launcher for both Linux
// architectures without a Linux toolchain. Losing an entry silently produced an
// npm package that the managed daemon refused to install.
func TestBwrapPrebuiltSourcesCoverLinuxTargets(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "third_party", "bwrap", "sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Targets []struct {
			GOOS   string `json:"goos"`
			GOARCH string `json:"goarch"`
		} `json:"targets"`
		Prebuilt []struct {
			GOOS            string `json:"goos"`
			GOARCH          string `json:"goarch"`
			Target          string `json:"target"`
			Upstream        string `json:"upstream"`
			UpstreamRelease string `json:"upstreamRelease"`
			URL             string `json:"url"`
			SHA256          string `json:"sha256"`
			Bytes           int64  `json:"bytes"`
			Member          string `json:"member"`
		} `json:"prebuilt"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse sources.json: %v", err)
	}
	digest := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, target := range manifest.Targets {
		platform := target.GOOS + "-" + target.GOARCH
		found := false
		for _, entry := range manifest.Prebuilt {
			if entry.GOOS != target.GOOS || entry.GOARCH != target.GOARCH {
				continue
			}
			found = true
			if !strings.HasPrefix(entry.URL, "https://github.com/openai/codex/releases/download/") {
				t.Errorf("%s prebuilt url = %q, want a pinned openai/codex release asset", platform, entry.URL)
			}
			if entry.Upstream != "openai/codex" || !strings.HasPrefix(entry.UpstreamRelease, "rust-v") {
				t.Errorf("%s prebuilt upstream = %q %q", platform, entry.Upstream, entry.UpstreamRelease)
			}
			if !digest.MatchString(entry.SHA256) || entry.Bytes <= 0 || entry.Member == "" || entry.Target == "" {
				t.Errorf("%s prebuilt entry is not pinned: %+v", platform, entry)
			}
		}
		if !found {
			t.Errorf("no pinned prebuilt bubblewrap for %s", platform)
		}
	}
}

// TestPackagingFailsClosedOnMissingTools pins that an incomplete release fails
// before packing instead of publishing a package a managed daemon rejects.
func TestPackagingFailsClosedOnMissingTools(t *testing.T) {
	root := repositoryRoot(t)
	tests := []struct {
		path      string
		required  []string
		forbidden []string
	}{
		{
			path: filepath.Join(root, "npm", "scripts", "build-packages.mjs"),
			required: []string{
				"requireStagedArtifact",
				"third_party/bwrap/prepare_bwrap.py",
				"--prebuilt",
				"missing required package artifact",
			},
			forbidden: []string{
				"console.warn(`bubblewrap",
				"console.warn(`ripgrep",
			},
		},
		{
			path: filepath.Join(root, ".github", "workflows", "release.yml"),
			required: []string{
				"third_party/ripgrep/prepare_ripgrep.py",
				`third_party/bwrap/prepare_bwrap.py --platform "$target" --prebuilt`,
			},
		},
		{
			path: filepath.Join(root, "third_party", "bwrap", "prepare_bwrap.py"),
			required: []string{
				"prepare_from_prebuilt",
				"\"--prebuilt\"",
				"\"kind\": \"prebuilt\"",
			},
		},
	}
	for _, test := range tests {
		t.Run(filepath.Base(test.path), func(t *testing.T) {
			content, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(content)
			for _, required := range test.required {
				if !strings.Contains(text, required) {
					t.Fatalf("%s is missing %q", test.path, required)
				}
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s still warns instead of failing on %q", test.path, forbidden)
				}
			}
		})
	}
}

// TestBuildScriptsStagePreparedBwrap pins that a package layout produced by the
// local build scripts carries the Linux sandbox launcher, and that the warning
// names the cross-platform staging command.
func TestBuildScriptsStagePreparedBwrap(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{"build.sh", "build.ps1"} {
		path := filepath.Join(root, "scripts", name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		for _, required := range []string{
			"third_party/bwrap/build/",
			"bin/bwrap",
			"codex-resources",
			"prepare_bwrap.py",
			"--prebuilt",
		} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s is missing %q", path, required)
			}
		}
	}
}
