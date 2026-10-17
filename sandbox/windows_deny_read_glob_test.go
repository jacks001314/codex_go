package sandbox

import "testing"

func intPtrForTest(value int) *int { return &value }

func TestWindowsDenyReadGlobScanForPatternLikeRust(t *testing.T) {
	tests := []struct {
		name            string
		pattern         string
		configuredDepth *int
		wantRoot        string
		wantSuffix      string
		wantMaxDepth    *int
	}{
		{
			name:         "recursive glob keeps the literal prefix",
			pattern:      `/tmp/work/**/*.env`,
			wantRoot:     "/tmp/work",
			wantSuffix:   "**/*.env",
			wantMaxDepth: nil,
		},
		{
			name:         "non-recursive glob bounds the suffix depth",
			pattern:      `/tmp/work/*.env`,
			wantRoot:     "/tmp/work",
			wantSuffix:   "*.env",
			wantMaxDepth: intPtrForTest(1),
		},
		{
			name:         "windows drive prefix keeps its trailing separator",
			pattern:      `C:\repo\**\*.env`,
			wantRoot:     `C:\repo`,
			wantSuffix:   `**\*.env`,
			wantMaxDepth: nil,
		},
		{
			name:         "drive root keeps the root separator",
			pattern:      `C:\*.env`,
			wantRoot:     `C:\`,
			wantSuffix:   "*.env",
			wantMaxDepth: intPtrForTest(1),
		},
		{
			name:         "pattern without a separator scans the current directory",
			pattern:      `*.env`,
			wantRoot:     ".",
			wantSuffix:   "*.env",
			wantMaxDepth: intPtrForTest(1),
		},
		{
			name:            "configured depth bounds a recursive glob",
			pattern:         `/tmp/**/*.env`,
			configuredDepth: intPtrForTest(4),
			wantRoot:        "/tmp",
			wantSuffix:      "**/*.env",
			wantMaxDepth:    intPtrForTest(4),
		},
		{
			name:            "configured depth caps a literal suffix",
			pattern:         `/tmp/a/b/*.env`,
			configuredDepth: intPtrForTest(1),
			wantRoot:        "/tmp/a/b",
			wantSuffix:      "*.env",
			wantMaxDepth:    intPtrForTest(1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scan := WindowsDenyReadGlobScanForPattern(test.pattern, test.configuredDepth)
			if scan.Root != test.wantRoot {
				t.Fatalf("Root = %q, want %q", scan.Root, test.wantRoot)
			}
			if scan.PatternSuffix != test.wantSuffix {
				t.Fatalf("PatternSuffix = %q, want %q", scan.PatternSuffix, test.wantSuffix)
			}
			switch {
			case test.wantMaxDepth == nil && scan.MaxDepth != nil:
				t.Fatalf("MaxDepth = %v, want nil", *scan.MaxDepth)
			case test.wantMaxDepth != nil && scan.MaxDepth == nil:
				t.Fatalf("MaxDepth = nil, want %d", *test.wantMaxDepth)
			case test.wantMaxDepth != nil && *scan.MaxDepth != *test.wantMaxDepth:
				t.Fatalf("MaxDepth = %d, want %d", *scan.MaxDepth, *test.wantMaxDepth)
			}
		})
	}
}
