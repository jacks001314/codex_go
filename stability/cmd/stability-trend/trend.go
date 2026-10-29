package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex_go/stability/harness/report"
)

// Options controls one trend analysis.
type Options struct {
	// Dir holds the JSON reports written by the stability scenarios.
	Dir string
	// Last is how many reports per scenario name take part in the fit.
	Last int
	// MinPoints is the smallest series that can produce an alert.
	MinPoints int
	// GrowthPerHour is the relative growth per hour that counts as a
	// regression (0.01 means 1% per hour).
	GrowthPerHour float64
	// MinTotalGrowth suppresses alerts on series that barely moved, so
	// small counts (six goroutines becoming seven) do not trip the gate.
	MinTotalGrowth float64
	// MinSpanHours is the shortest series that can alert. Short soak runs a
	// few minutes apart produce huge extrapolated slopes from GC noise.
	MinSpanHours float64
}

// Point is one metric value from one report.
type Point struct {
	At     time.Time `json:"at"`
	Value  float64   `json:"value"`
	Report string    `json:"report"`
	Commit string    `json:"commit,omitempty"`
}

// Series is one metric tracked across reports of the same scenario.
type Series struct {
	Metric           string  `json:"metric"`
	Points           []Point `json:"points"`
	SpanHours        float64 `json:"span_hours"`
	SlopePerHour     float64 `json:"slope_per_hour"`
	RelGrowthPerHour float64 `json:"rel_growth_per_hour"`
	TotalGrowth      float64 `json:"total_growth"`
	Alert            bool    `json:"alert"`
	Reason           string  `json:"reason,omitempty"`
}

// Scenario groups the series of one report name.
type Scenario struct {
	Name   string   `json:"name"`
	Runs   int      `json:"runs"`
	Failed []string `json:"failed,omitempty"`
	Series []Series `json:"series,omitempty"`
}

// Result is the whole analysis.
type Result struct {
	Dir       string     `json:"dir"`
	Reports   int        `json:"reports"`
	Scenarios []Scenario `json:"scenarios"`
	Alerts    []string   `json:"alerts,omitempty"`
	OK        bool       `json:"ok"`
}

// Analyze reads every report under options.Dir and fits a line through each
// metric series. It never mutates the report directory.
func Analyze(options Options) (*Result, error) {
	if options.Last <= 0 {
		options.Last = 7
	}
	if options.MinPoints <= 0 {
		options.MinPoints = 3
	}
	if options.GrowthPerHour <= 0 {
		options.GrowthPerHour = 0.01
	}
	if options.MinTotalGrowth <= 0 {
		options.MinTotalGrowth = 0.05
	}
	if options.MinSpanHours <= 0 {
		options.MinSpanHours = 1
	}

	paths, err := filepath.Glob(filepath.Join(options.Dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	byName := map[string][]report.Report{}
	fileByName := map[string]string{}
	result := &Result{Dir: options.Dir}
	for _, path := range paths {
		run, err := readReport(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		byName[run.Name] = append(byName[run.Name], run)
		fileByName[reportKey(run)] = filepath.Base(path)
		result.Reports++
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		runs := byName[name]
		sort.SliceStable(runs, func(i, j int) bool { return runs[i].StartedAt.Before(runs[j].StartedAt) })
		if len(runs) > options.Last {
			runs = runs[len(runs)-options.Last:]
		}
		scenario := Scenario{Name: name, Runs: len(runs)}
		for _, run := range runs {
			if !run.Passed {
				scenario.Failed = append(scenario.Failed, fileByName[reportKey(run)])
			}
		}
		if len(scenario.Failed) > 0 {
			result.Alerts = append(result.Alerts, fmt.Sprintf("%s: %d recent run(s) failed: %s",
				name, len(scenario.Failed), strings.Join(scenario.Failed, ", ")))
		}

		for _, metric := range metricNames(runs) {
			points := collectPoints(runs, metric, fileByName)
			if len(points) < options.MinPoints {
				continue
			}
			series := fit(metric, points)
			if series.SpanHours >= options.MinSpanHours &&
				series.RelGrowthPerHour > options.GrowthPerHour &&
				math.Abs(series.TotalGrowth) > options.MinTotalGrowth {
				series.Alert = true
				series.Reason = fmt.Sprintf("grows %.2f%%/h (budget %.2f%%/h, total %.1f%%)",
					series.RelGrowthPerHour*100, options.GrowthPerHour*100, series.TotalGrowth*100)
				result.Alerts = append(result.Alerts, name+": "+metric+" "+series.Reason)
			}
			scenario.Series = append(scenario.Series, series)
		}
		sort.SliceStable(scenario.Series, func(i, j int) bool { return scenario.Series[i].Metric < scenario.Series[j].Metric })
		result.Scenarios = append(result.Scenarios, scenario)
	}
	result.OK = len(result.Alerts) == 0
	return result, nil
}

func readReport(path string) (report.Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report.Report{}, err
	}
	var run report.Report
	if err := json.Unmarshal(data, &run); err != nil {
		return report.Report{}, err
	}
	return run, nil
}

// reportKey identifies a report file by start time and name; report.Write uses
// the same pair for the file name, which keeps failure messages pointing at
// the artifact a human can open.
func reportKey(run report.Report) string {
	return run.StartedAt.Format(time.RFC3339Nano) + "|" + run.Name
}

// metricNames returns the union of explicit metrics and metrics derived from
// samples/disk snapshots, so reports written before Metrics existed still
// participate in trends.
func metricNames(runs []report.Report) []string {
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, run := range runs {
		for name := range run.Metrics {
			add(name)
		}
		for name := range derivedMetrics(run) {
			add(name)
		}
	}
	sort.Strings(names)
	return names
}

func collectPoints(runs []report.Report, metric string, fileByName map[string]string) []Point {
	points := make([]Point, 0, len(runs))
	for _, run := range runs {
		value, ok := run.Metrics[metric]
		if !ok {
			derived := derivedMetrics(run)
			value, ok = derived[metric]
		}
		if !ok {
			continue
		}
		points = append(points, Point{
			At:     run.StartedAt,
			Value:  value,
			Report: fileByName[reportKey(run)],
			Commit: run.Commit,
		})
	}
	return points
}

// derivedMetrics extracts the resource series that every scenario already
// records, so trend analysis works without extra bookkeeping.
func derivedMetrics(run report.Report) map[string]float64 {
	values := map[string]float64{}
	if len(run.Samples) > 0 {
		var heapPeak, rssPeak uint64
		for _, sample := range run.Samples {
			if sample.HeapAllocBytes > heapPeak {
				heapPeak = sample.HeapAllocBytes
			}
			if sample.RSSBytes > rssPeak {
				rssPeak = sample.RSSBytes
			}
		}
		last := run.Samples[len(run.Samples)-1]
		values["samples.heap_peak_bytes"] = float64(heapPeak)
		values["samples.rss_peak_bytes"] = float64(rssPeak)
		values["samples.goroutines_final"] = float64(last.Goroutines)
		values["samples.open_fds_final"] = float64(last.OpenFDs)
	}
	if len(run.Disk) > 0 {
		last := run.Disk[len(run.Disk)-1]
		values["disk.total_bytes"] = float64(last.TotalBytes)
		values["disk.wal_bytes"] = float64(last.WALBytes)
		values["disk.temp_files"] = float64(last.TempFiles)
	}
	return values
}

// fit runs an ordinary least-squares line through the points.
func fit(metric string, points []Point) Series {
	series := Series{Metric: metric, Points: points}
	series.SpanHours = points[len(points)-1].At.Sub(points[0].At).Hours()
	first := points[0].Value
	last := points[len(points)-1].Value
	if first != 0 {
		series.TotalGrowth = (last - first) / math.Abs(first)
	}
	if len(points) < 2 {
		return series
	}
	origin := points[0].At
	var sumX, sumY, sumXY, sumXX float64
	for _, point := range points {
		x := point.At.Sub(origin).Hours()
		y := point.Value
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	n := float64(len(points))
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return series
	}
	series.SlopePerHour = (n*sumXY - sumX*sumY) / denominator
	if first != 0 {
		series.RelGrowthPerHour = series.SlopePerHour / math.Abs(first)
	}
	return series
}

// Print writes a human-readable summary.
func (r *Result) Print(writer *strings.Builder) {
	fmt.Fprintf(writer, "stability-trend dir=%s reports=%d\n", r.Dir, r.Reports)
	for _, scenario := range r.Scenarios {
		fmt.Fprintf(writer, "\n%s (%d run(s))\n", scenario.Name, scenario.Runs)
		if len(scenario.Series) == 0 {
			fmt.Fprintf(writer, "  no metric series with enough points\n")
			continue
		}
		fmt.Fprintf(writer, "  %-36s %6s %14s %14s %10s\n", "metric", "points", "first", "last", "rel/h")
		for _, series := range scenario.Series {
			first := series.Points[0].Value
			last := series.Points[len(series.Points)-1].Value
			flag := ""
			if series.Alert {
				flag = "  ALERT"
			}
			fmt.Fprintf(writer, "  %-36s %6d %14.4g %14.4g %9.2f%%%s\n",
				series.Metric, len(series.Points), first, last, series.RelGrowthPerHour*100, flag)
		}
	}
	if len(r.Alerts) > 0 {
		fmt.Fprintf(writer, "\nalerts:\n")
		for _, alert := range r.Alerts {
			fmt.Fprintf(writer, "  - %s\n", alert)
		}
	} else {
		fmt.Fprintf(writer, "\nno regressions detected\n")
	}
}
