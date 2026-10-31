package app

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"codex_go/features"
)

// featuresListRow matches Rust cli/src/main.rs FeaturesSubcommand::List output:
// the name and stage columns are padded to the longest entry (byte length) and
// separated by exactly two spaces.
var featuresListRow = regexp.MustCompile(`^([^ ]+)( +)(under development|experimental|stable|deprecated|removed)( +)(true|false)$`)

// TestFeaturesListUsesDynamicColumnWidthsLikeRust pins the dynamic widths the
// Rust printer derives from FEATURES instead of fixed columns.
func TestFeaturesListUsesDynamicColumnWidthsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"features", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("features list returned error: %v", err)
	}

	wantNameWidth := 0
	wantStageWidth := 0
	for _, spec := range features.Sorted() {
		wantNameWidth = max(wantNameWidth, len(spec.Key))
		wantStageWidth = max(wantStageWidth, len(spec.Stage))
	}

	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != len(features.Sorted()) {
		t.Fatalf("features list printed %d rows, want %d", len(lines), len(features.Sorted()))
	}
	for _, line := range lines {
		match := featuresListRow.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("line %q is not padded with two spaces between columns", line)
		}
		nameWidth := len(match[1]) + len(match[2]) - 2
		stageWidth := len(match[3]) + len(match[4]) - 2
		if nameWidth != wantNameWidth {
			t.Fatalf("line %q uses a %d-wide name column, want %d", line, nameWidth, wantNameWidth)
		}
		if stageWidth != wantStageWidth {
			t.Fatalf("line %q uses a %d-wide stage column, want %d", line, stageWidth, wantStageWidth)
		}
	}
}
