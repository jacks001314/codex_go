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
	sessions         int64
	archivedSessions int64
}

// codexHomeScanCancel is a cancellation check for the home-size scan; a nil or
// false-returning check never cancels.
type codexHomeScanCancel func() bool

// scanCodexHomeSizes sums regular-file lengths under the sessions and
// archived_sessions directories of codexHome without reading file contents or
// following symlinks (Rust #43790). Missing session directories are skipped.
// Scanning is abandoned (returning os.ErrInvalid) once cancel reports true.
func scanCodexHomeSizes(codexHome string, cancel codexHomeScanCancel) (codexHomeSizes, error) {
	var sizes codexHomeSizes
	if strings.TrimSpace(codexHome) == "" {
		return sizes, os.ErrInvalid
	}
	if cancel != nil && cancel() {
		return sizes, os.ErrInvalid
	}
	sessions := filepath.Join(codexHome, rollout.SessionsSubdir)
	archived := filepath.Join(codexHome, rollout.ArchivedSessionsSubdir)
	pending := make([]string, 0, 2)
	for _, root := range []string{sessions, archived} {
		if cancel != nil && cancel() {
			return sizes, os.ErrInvalid
		}
		// Inspect the roots without following symlinks, just like entries below them.
		info, err := os.Lstat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return sizes, err
		}
		if info.IsDir() {
			pending = append(pending, root)
		}
	}
	for len(pending) > 0 {
		if cancel != nil && cancel() {
			return sizes, os.ErrInvalid
		}
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
// sessions / archived_sessions subdirectories (Rust #43790).
func recordCodexHomeMetrics(metrics *state.TaskMetrics, codexHome string, cancel codexHomeScanCancel) {
	if metrics == nil {
		return
	}
	sizes, err := scanCodexHomeSizes(codexHome, cancel)
	if err != nil {
		return
	}
	for _, metric := range []struct {
		label string
		bytes int64
	}{
		{rollout.SessionsSubdir, sizes.sessions},
		{rollout.ArchivedSessionsSubdir, sizes.archivedSessions},
	} {
		metrics.HistogramWithBounds(codexHomeSizeBytesMetric, int(metric.bytes), codexHomeSizeBytesBoundaries, map[string]string{"directory": metric.label})
	}
}
