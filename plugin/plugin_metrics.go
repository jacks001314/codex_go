package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	pluginAnalyticsManifestFile     = "analytics.yaml"
	maxPluginAnalyticsManifestBytes = 64 * 1024
	maxPluginMetricIdentifierLength = 64
	maxPluginMetricDimensions       = 8
)

// PluginMeasurementDefinition is the manifest declaration for one numeric
// measurement (Rust #38238).
type PluginMeasurementDefinition struct {
	EnumDimensions map[string][]string
}

// PluginMetricsOperation is a trusted plugin script operation with declared
// measurements.
type PluginMetricsOperation struct {
	OperationName string
	Measurements  map[string]PluginMeasurementDefinition
}

// ResolvedPluginMetricsOperation binds a metrics operation to a fresh trusted
// plugin command attribution.
type ResolvedPluginMetricsOperation struct {
	PluginID  string
	Operation PluginMetricsOperation
}

type pluginAnalyticsManifest struct {
	Version    int                                   `yaml:"version"`
	Operations map[string]pluginMetricsOperationDecl `yaml:"operations"`
}

type pluginMetricsOperationDecl struct {
	Path         string                              `yaml:"path"`
	Measurements map[string]pluginMetricsMeasureDecl `yaml:"measurements"`
}

type pluginMetricsMeasureDecl struct {
	Dimensions map[string][]string `yaml:"dimensions"`
}

func loadPluginMetricsOperations(root string) map[string]PluginMetricsOperation {
	root = filepath.Clean(root)
	manifestPath := filepath.Join(root, pluginAnalyticsManifestFile)
	resolved, err := filepath.EvalSymlinks(manifestPath)
	if err != nil || resolved != manifestPath {
		return nil
	}
	info, err := os.Stat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPluginAnalyticsManifestBytes {
		return nil
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil || len(data) > maxPluginAnalyticsManifestBytes {
		return nil
	}
	var manifest pluginAnalyticsManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	return validatePluginAnalyticsManifest(manifest, root)
}

func validatePluginAnalyticsManifest(manifest pluginAnalyticsManifest, root string) map[string]PluginMetricsOperation {
	if manifest.Version != 1 || len(manifest.Operations) == 0 {
		return nil
	}
	operationsByPath := make(map[string]PluginMetricsOperation, len(manifest.Operations))
	for operationName, operation := range manifest.Operations {
		if !validPluginMetricIdentifier(operationName) || len(operation.Measurements) == 0 {
			return nil
		}
		normalizedPath := validatePluginMetricsOperationPath(root, operation.Path)
		if normalizedPath == "" {
			return nil
		}
		measurements := make(map[string]PluginMeasurementDefinition, len(operation.Measurements))
		for measurementName, measurement := range operation.Measurements {
			if !validPluginMetricIdentifier(measurementName) || len(measurement.Dimensions) > maxPluginMetricDimensions {
				return nil
			}
			enumDimensions := make(map[string][]string, len(measurement.Dimensions))
			for dimensionName, values := range measurement.Dimensions {
				if !validPluginMetricIdentifier(dimensionName) || len(values) == 0 {
					return nil
				}
				unique := make(map[string]bool, len(values))
				for _, value := range values {
					if !validPluginMetricIdentifier(value) || unique[value] {
						return nil
					}
					unique[value] = true
				}
				sorted := append([]string(nil), values...)
				sort.Strings(sorted)
				enumDimensions[dimensionName] = sorted
			}
			measurements[measurementName] = PluginMeasurementDefinition{EnumDimensions: enumDimensions}
		}
		operationValue := PluginMetricsOperation{OperationName: operationName, Measurements: measurements}
		if _, exists := operationsByPath[normalizedPath]; exists {
			return nil
		}
		operationsByPath[normalizedPath] = operationValue
	}
	return operationsByPath
}

func validatePluginMetricsOperationPath(root string, path string) string {
	normalized := strings.TrimPrefix(strings.TrimSpace(path), "./")
	if !IsSafePluginRelativePath(normalized) {
		return ""
	}
	scriptPath := filepath.Join(root, filepath.FromSlash(normalized))
	resolved, err := filepath.EvalSymlinks(scriptPath)
	if err != nil {
		return ""
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || filepath.ToSlash(relative) != normalized {
		return ""
	}
	return normalized
}

func validPluginMetricIdentifier(value string) bool {
	if value == "" || len(value) > maxPluginMetricIdentifierLength || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9') && character != '_' {
			return false
		}
	}
	return true
}

// ResolveMetricsOperation resolves one exact command to one trusted
// manifest-declared metrics operation.
func (r TrustedPluginRoots) ResolveMetricsOperation(command []string, cwd string) *ResolvedPluginMetricsOperation {
	attribution := r.Resolve(command, cwd)
	if attribution == nil {
		return nil
	}
	var match *ResolvedPluginMetricsOperation
	for _, root := range r.roots {
		if root.pluginID != attribution.PluginID {
			continue
		}
		operation, ok := root.metricsOperationsByPath[attribution.ScriptPath]
		if !ok {
			continue
		}
		if match != nil {
			return nil
		}
		match = &ResolvedPluginMetricsOperation{PluginID: attribution.PluginID, Operation: operation}
	}
	return match
}

func (o ResolvedPluginMetricsOperation) String() string {
	if o.PluginID == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s", o.PluginID, o.Operation.OperationName)
}
