package memories

// Rust parity: codex-rs/state/src/runtime/memories.rs `consolidation_progress`
// and codex-rs/memories/write (#43827). Rust keeps the largest distinct-thread
// count from a successful consolidation across pruning and clears it on an
// explicit memory reset; Go persists the same value in the v2 root because the
// Go state DB migrations are pinned to a certified inventory.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const consolidationProgressFilename = "consolidation_progress.json"

type consolidationProgress struct {
	MaxThreadCount uint32 `json:"max_thread_count"`
}

func consolidationProgressPath(codexHome string) string {
	return filepath.Join(V2Root(codexHome), consolidationProgressFilename)
}

// MaxConsolidatedThreadCount returns the largest number of distinct source
// threads included in a successful v2 consolidation, or 0 when none has been
// recorded (Rust MemoryStore::max_consolidated_thread_count, #43827).
func MaxConsolidatedThreadCount(codexHome string) uint32 {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return 0
	}
	data, err := os.ReadFile(consolidationProgressPath(codexHome))
	if err != nil {
		return 0
	}
	var progress consolidationProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return 0
	}
	return progress.MaxThreadCount
}

// RecordConsolidatedThreadCount persists the largest successful consolidation
// count, never lowering an existing value (Rust #43827).
func RecordConsolidatedThreadCount(codexHome string, count uint32) error {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" || count <= MaxConsolidatedThreadCount(codexHome) {
		return nil
	}
	root := V2Root(codexHome)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(consolidationProgress{MaxThreadCount: count})
	if err != nil {
		return err
	}
	return os.WriteFile(consolidationProgressPath(codexHome), data, 0o600)
}
