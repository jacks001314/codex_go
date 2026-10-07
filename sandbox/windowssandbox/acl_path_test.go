package windowssandbox

import (
	"strings"
	"testing"
)

// Covers the spelling rules #49058 needs for runtime ACL repair: a nested
// runtime path beyond the legacy Windows limit is handed to the security APIs
// in its extended-length form, while short, relative, device and
// already-extended paths keep the caller's spelling.
func TestExtendedACLPathSpellsLongRuntimePathsForSecurityAPIs(t *testing.T) {
	const longSegment = "dependency-cache-0123456789012345678901234567890123456789012345678901"
	short := `C:\Users\user\.cache\codex-runtimes`
	longDir := short
	for len(longDir) <= 280 {
		longDir += `\` + longSegment
	}
	longFile := longDir + `\module.js`
	cases := []struct {
		name string
		path string
		want string
	}{
		{"short absolute path", short, short},
		{"short UNC path", `\\server\share\codex`, `\\server\share\codex`},
		{"short relative path", `codex-runtimes\bin`, `codex-runtimes\bin`},
		{"short rooted path", `\codex-runtimes`, `\codex-runtimes`},
		{"device path", `\\.\C:\` + strings.Repeat("a", 300), `\\.\C:\` + strings.Repeat("a", 300)},
		{"already extended path", `\\?\C:\` + strings.Repeat("a", 300), `\\?\C:\` + strings.Repeat("a", 300)},
		{"native extended path", `\??\C:\` + strings.Repeat("a", 300), `\??\C:\` + strings.Repeat("a", 300)},
		{"longest path under the limit", `C:\` + strings.Repeat("a", 244), `C:\` + strings.Repeat("a", 244)},
		{"path at the limit", `C:\` + strings.Repeat("a", 245), `\\?\C:\` + strings.Repeat("a", 245)},
		{"long absolute path", `C:\` + strings.Repeat("a", 300), `\\?\C:\` + strings.Repeat("a", 300)},
		{"long UNC path", `\\server\share\` + strings.Repeat("a", 300), `\\?\UNC\server\share\` + strings.Repeat("a", 300)},
		{"nested runtime directory beyond MAX_PATH", longDir, `\\?\` + longDir},
		{"nested runtime file beyond MAX_PATH", longFile, `\\?\` + longFile},
	}
	for _, testCase := range cases {
		if got := extendedACLPath(testCase.path); got != testCase.want {
			t.Fatalf("%s: extendedACLPath(len %d) = %q, want %q", testCase.name, len(testCase.path), got, testCase.want)
		}
	}
	if len(longFile) <= 280 || len(longDir) <= 280 {
		t.Fatalf("fixtures are %d/%d characters; the regression needs paths beyond the legacy limit", len(longDir), len(longFile))
	}
}
