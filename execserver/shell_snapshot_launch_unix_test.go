//go:build !windows

package execserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"codex_go/utils"
)

// Rust #48078: an exec-server launch replays the captured state through the
// inherited descriptor, so the child never receives the state in its
// environment and the carrier descriptor is gone once the state is sourced.
func TestExecServerLaunchesWithDescriptorSnapshotLikeRust(t *testing.T) {
	bash := hostSnapshotBash()
	if bash == "" {
		t.Skip("bash is unavailable on this host")
	}
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })

	// Rust #48078 replays every bash/zsh capture through the descriptor, not
	// only the states that exceed the environment replay budget.
	for _, testCase := range []struct {
		name    string
		payload int
	}{
		{name: "small", payload: 16},
		{name: "large", payload: 480 * 1024},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := NewServer()
			t.Cleanup(func() { server.shellSnapshots.close() })
			payload := strings.Repeat("x", testCase.payload)
			server.shellSnapshots.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
				return []byte("# Snapshot file\nexport BIG=\"" + payload + "\"\nexport SMALL=restored\n\x00\x00\x00"), nil
			}
			cwd, err := utils.FromHostNativePath(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			processID := "snapshot-descriptor-" + testCase.name
			script := "if [ -e /dev/fd/3 ]; then printf 'fd-leaked'; fi; printf '%s:%s' \"${#BIG}\" \"${SMALL:-missing}\""
			if _, err := server.startProcess(context.Background(), &ExecParams{
				ProcessID: processID,
				Argv:      []string{bash, "-lc", script},
				CWD:       cwd.String(),
				ShellSnapshot: &ShellSnapshotRequest{
					ScopeID: "descriptor-launch-" + testCase.name,
					Shell:   ShellInfo{Name: "bash", Path: bash},
				},
				EnvPolicy: &ExecEnvPolicy{Inherit: "none", IgnoreDefaultExcludes: true},
				Env:       map[string]string{"PATH": "/usr/bin:/bin"},
			}); err != nil {
				t.Fatalf("startProcess() error = %v", err)
			}
			after := uint64(0)
			wait := uint64(10_000)
			read, err := server.readProcess(&ReadParams{ProcessID: processID, AfterSeq: &after, WaitMS: &wait})
			if err != nil {
				t.Fatalf("readProcess() error = %v", err)
			}
			output := decodedChunks(read.Chunks, "stdout")
			want := fmt.Sprintf("%d:restored", len(payload))
			if !strings.Contains(output, want) {
				t.Fatalf("stdout = %q, want %q", output, want)
			}
			if strings.Contains(output, "fd-leaked") {
				t.Fatalf("stdout = %q, the carrier descriptor stayed open", output)
			}
		})
	}
}
