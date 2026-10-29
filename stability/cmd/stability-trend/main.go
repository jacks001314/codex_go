// Command stability-trend fits a line through the metrics of recent stability
// reports and fails when a series grows faster than the budget allows.
//
// It is the W10 trend job: nightly runs upload reports, and this command turns
// "seven green artifacts" into "no metric is drifting".
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir := flag.String("dir", envDefault("CODEX_STABILITY_REPORT_DIR", filepath.Join("stability", "reports")), "directory holding report JSON files")
	last := flag.Int("last", 7, "reports per scenario name included in the fit")
	minPoints := flag.Int("min-points", 3, "minimum points before a series can alert")
	growth := flag.Float64("growth-per-hour", 0.01, "relative growth per hour that counts as a regression")
	jsonOut := flag.Bool("json", false, "print the analysis as JSON")
	flag.Parse()

	result, err := Analyze(Options{
		Dir:           *dir,
		Last:          *last,
		MinPoints:     *minPoints,
		GrowthPerHour: *growth,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "stability-trend: %v\n", err)
		os.Exit(2)
	}
	if *jsonOut {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintf(os.Stderr, "stability-trend: %v\n", err)
			os.Exit(2)
		}
	} else {
		var builder strings.Builder
		result.Print(&builder)
		fmt.Print(builder.String())
	}
	if !result.OK {
		os.Exit(1)
	}
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
