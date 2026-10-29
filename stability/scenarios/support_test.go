//go:build stability

package scenarios

import (
	"os"
	"path/filepath"

	"codex_go/stability/harness/metrics"
)

func budgetsPath() string {
	if path := os.Getenv("CODEX_STABILITY_BUDGETS"); path != "" {
		return path
	}
	return filepath.Join("..", "budgets.json")
}

func reportDir() string {
	if path := os.Getenv("CODEX_STABILITY_REPORT_DIR"); path != "" {
		return path
	}
	return filepath.Join("..", "reports")
}

// maxSampleValue returns the largest value the picker extracts from samples,
// or zero for an empty series.
func maxSampleValue(samples []metrics.Sample, pick func(metrics.Sample) uint64) uint64 {
	var peak uint64
	for _, sample := range samples {
		if value := pick(sample); value > peak {
			peak = value
		}
	}
	return peak
}
