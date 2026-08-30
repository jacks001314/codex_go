package appserver

import (
	"os"
	"path/filepath"
	"strings"

	"codex_go/rollout"
	"codex_go/state"
)

const codexHomeSizeBytesMetric = "codex.app_server.codex_home.size_bytes"

// codexHomeSizeBytesBoundaries mirrors Rust codex_home_metrics.rs bucket sizes
// (1 MiB through 1 TiB; larger homes use the overflow bucket).
var codexHomeSizeBytesBoundaries = []float64{
	1_048_576,
	10_485_760,
	104_857_600,
	1_073_741_824,
	10_737_418_240,
	107_374_182_400,
	1_099_511_627_776,
}

type codexHomeSizes struct {
	codexHome        int64
	sessions         int64
	archivedSessions int64
}

// scanCodexHomeSizes sums regular-file lengths under codexHome without reading
// file contents or following symlinks (Rust #41360 directory_sizes).
func scanCodexHomeSizes(codexHome string) (codexHomeSizes, error) {
	var sizes codexHomeSizes
	if strings.TrimSpace(codexHome) == "" {
		return sizes, os.ErrInvalid
	}
	sessions := filepath.Join(codexHome, rollout.SessionsSubdir)
	archived := filepath.Join(codexHome, rollout.ArchivedSessionsSubdir)
	pending := []string{codexHome}
	for len(pending) > 0 {
		dir := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		entries, err := os.ReadDir(dir)
		if err != nil {
			return sizes, err
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				pending = append(pending, path)
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			bytes := info.Size()
			sizes.codexHome += bytes
			if strings.HasPrefix(path, sessions) {
				sizes.sessions += bytes
			} else if strings.HasPrefix(path, archived) {
				sizes.archivedSessions += bytes
			}
		}
	}
	return sizes, nil
}

// recordCodexHomeMetrics scans codexHome and records the size histogram for the
// whole home and the sessions / archived_sessions subdirectories (Rust #41360).
func recordCodexHomeMetrics(metrics *state.TaskMetrics, codexHome string) {
	if metrics == nil {
		return
	}
	sizes, err := scanCodexHomeSizes(codexHome)
	if err != nil {
		return
	}
	for _, metric := range []struct {
		label string
		bytes int64
	}{
		{"codex_home", sizes.codexHome},
		{rollout.SessionsSubdir, sizes.sessions},
		{rollout.ArchivedSessionsSubdir, sizes.archivedSessions},
	} {
		metrics.HistogramWithBounds(codexHomeSizeBytesMetric, int(metric.bytes), codexHomeSizeBytesBoundaries, map[string]string{"directory": metric.label})
	}
}
