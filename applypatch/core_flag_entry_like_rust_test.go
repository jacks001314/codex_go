package applypatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoreFlagEntryTakesFirstArgumentLikeRust pins the
// `codex --codex-run-as-apply-patch <PATCH>` dispatch: exactly one argument is
// required, extra arguments are ignored, and stdin is never consulted.
func TestCoreFlagEntryTakesFirstArgumentLikeRust(t *testing.T) {
	valid := "*** Begin Patch\n*** Add File: x.txt\n+hi\n*** End Patch"
	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
		wantFile   string
	}{
		{
			name:       "no argument",
			args:       nil,
			wantCode:   1,
			wantStderr: "Error: --codex-run-as-apply-patch requires a UTF-8 PATCH argument.\n",
		},
		{
			name:       "empty argument",
			args:       []string{""},
			wantCode:   1,
			wantStderr: "Invalid patch: The last line of the patch must be '*** End Patch'\n",
		},
		{
			name:     "extra arguments are ignored",
			args:     []string{valid, "garbage"},
			wantCode: 0,
			wantFile: "x.txt",
		},
		{
			name:       "only the first argument is the patch",
			args:       []string{"garbage", valid},
			wantCode:   1,
			wantStderr: "Invalid patch: The first line of the patch must be '*** Begin Patch'\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			var stdout, stderr strings.Builder
			code := RunCoreFlagCLI(testCase.args, &stdout, &stderr, dir)
			if code != testCase.wantCode {
				t.Fatalf("RunCoreFlagCLI() code = %d, want %d (stderr %q)", code, testCase.wantCode, stderr.String())
			}
			if stderr.String() != testCase.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), testCase.wantStderr)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.wantFile == "" {
				if len(entries) != 0 {
					t.Fatalf("workspace has %d entries, want none", len(entries))
				}
				return
			}
			data, err := os.ReadFile(filepath.Join(dir, testCase.wantFile))
			if err != nil || string(data) != "hi\n" {
				t.Fatalf("%s = %q, %v; want %q", testCase.wantFile, data, err, "hi\n")
			}
		})
	}
}
