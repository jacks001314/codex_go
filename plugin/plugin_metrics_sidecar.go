package plugin

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const PluginMetricsOutputEnvVar = "CODEX_PLUGIN_METRICS_OUTPUT"

const (
	maxPluginMetricsOutputBytes = 64 * 1024
	maxPluginMetricsOutputRows  = 100
)

// PluginMeasurementBatch is the validated measurement output for one plugin
// execution.
type PluginMeasurementBatch struct {
	PluginID    string
	ExecutionID string
	Operation   string
	Rows        []PluginMeasurementRow
}

func (s *PluginMetricsSidecar) Cleanup() {
	if s == nil {
		return
	}
	_ = s.outputFile.Close()
	_ = os.RemoveAll(s.outputDir)
}

// PluginMeasurementRow is one validated numeric measurement.
type PluginMeasurementRow struct {
	MeasurementName string
	NumberValue     float64
	Dimensions      map[string]string
}

type pluginMetricsOutputEnvelope struct {
	Version      int               `json:"version"`
	Measurements []json.RawMessage `json:"measurements"`
}

type pluginMetricsOutputMeasurement struct {
	Name       string            `json:"name"`
	Value      float64           `json:"value"`
	Dimensions map[string]string `json:"dimensions"`
}

// PluginMetricsSidecar prepares a sandbox-writable output file for a trusted
// plugin command and parses/validates it after execution (Rust #38252).
type PluginMetricsSidecar struct {
	outputFile     *os.File
	outputDir      string
	outputEnvValue string
	resolved       ResolvedPluginMetricsOperation
	executionID    string
}

func NewPluginMetricsSidecar(resolved ResolvedPluginMetricsOperation) *PluginMetricsSidecar {
	if resolved.PluginID == "" || resolved.Operation.OperationName == "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "codex-plugin-metrics-")
	if err != nil {
		return nil
	}
	file, err := os.CreateTemp(dir, "measurements-*.json")
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil
	}
	return &PluginMetricsSidecar{
		outputFile:     file,
		outputDir:      dir,
		outputEnvValue: file.Name(),
		resolved:       resolved,
		executionID:    uuid.NewString(),
	}
}

func (s *PluginMetricsSidecar) InstallOutputEnv(env map[string]string) {
	if s == nil || env == nil {
		return
	}
	env[PluginMetricsOutputEnvVar] = s.outputEnvValue
}

func StripPluginMetricsOutputEnv(env map[string]string) {
	if env == nil {
		return
	}
	for key := range env {
		if key == PluginMetricsOutputEnvVar {
			delete(env, key)
		}
	}
}

func (s *PluginMetricsSidecar) OutputPath() string {
	if s == nil {
		return ""
	}
	return s.outputEnvValue
}

func (s *PluginMetricsSidecar) OutputDir() string {
	if s == nil {
		return ""
	}
	return s.outputDir
}

func (s *PluginMetricsSidecar) Finish(exitCode int) *PluginMeasurementBatch {
	if s == nil || exitCode != 0 {
		return nil
	}
	defer s.Cleanup()
	rows := parsePluginMetricsOutput(s.outputEnvValue, s.resolved)
	if len(rows) == 0 {
		return nil
	}
	return &PluginMeasurementBatch{
		PluginID:    s.resolved.PluginID,
		ExecutionID: s.executionID,
		Operation:   s.resolved.Operation.OperationName,
		Rows:        rows,
	}
}

func parsePluginMetricsOutput(path string, resolved ResolvedPluginMetricsOperation) []PluginMeasurementRow {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPluginMetricsOutputBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxPluginMetricsOutputBytes {
		return nil
	}
	var envelope pluginMetricsOutputEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Version != 1 || len(envelope.Measurements) > maxPluginMetricsOutputRows {
		return nil
	}
	seen := map[string]bool{}
	rows := make([]PluginMeasurementRow, 0, len(envelope.Measurements))
	for _, raw := range envelope.Measurements {
		var measurement pluginMetricsOutputMeasurement
		if err := json.Unmarshal(raw, &measurement); err != nil {
			continue
		}
		definition, ok := resolved.Operation.Measurements[measurement.Name]
		if !ok || math.IsNaN(measurement.Value) || math.IsInf(measurement.Value, 0) {
			continue
		}
		if len(measurement.Dimensions) != len(definition.EnumDimensions) {
			continue
		}
		validDimensions := true
		for name, allowedValues := range definition.EnumDimensions {
			value, ok := measurement.Dimensions[name]
			if !ok || !containsString(allowedValues, value) {
				validDimensions = false
				break
			}
		}
		if !validDimensions {
			continue
		}
		dimensions := cloneStringMap(measurement.Dimensions)
		key := measurement.Name + "\x00" + formatSortedDimensions(dimensions)
		if seen[key] {
			continue
		}
		seen[key] = true
		rows = append(rows, PluginMeasurementRow{
			MeasurementName: measurement.Name,
			NumberValue:     measurement.Value,
			Dimensions:      dimensions,
		})
	}
	sort.SliceStable(rows, func(i int, j int) bool {
		if rows[i].MeasurementName != rows[j].MeasurementName {
			return rows[i].MeasurementName < rows[j].MeasurementName
		}
		return rows[i].NumberValue < rows[j].NumberValue
	})
	return rows
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func formatSortedDimensions(dimensions map[string]string) string {
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, dimensions[key]))
	}
	return strings.Join(parts, ",")
}
