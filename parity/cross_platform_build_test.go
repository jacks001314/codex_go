package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCrossPlatformBuildsCompile mirrors the cross-platform release gate: the
// tree must keep compiling for the non-host release targets with CGO disabled.
// A Windows-only source that portable code references, or a Windows-only stub
// that imports the Windows syscall package, breaks the gate at build time and
// must never reach main again (Round 1273 fixed two such regressions).
func TestCrossPlatformBuildsCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-platform builds take a few seconds per target")
	}
	targets := []string{"linux/amd64", "darwin/arm64"}
	for _, target := range targets {
		goos, goarch, _ := strings.Cut(target, "/")
		cmd := exec.Command("go", "build", "./...")
		cmd.Dir = filepath.Join("..")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go build ./... for %s failed: %v\n%s", target, err, out)
		}
	}
}
