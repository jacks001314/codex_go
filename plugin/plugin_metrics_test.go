package plugin

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidatePluginAnalyticsManifestMatchesRust(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scripts", "measure.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("print(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := pluginAnalyticsManifest{
		Version: 1,
		Operations: map[string]pluginMetricsOperationDecl{
			"run_measure": {
				Path: "scripts/measure.py",
				Measurements: map[string]pluginMetricsMeasureDecl{
					"tokens": {Dimensions: map[string][]string{"speed": {"fast", "slow"}}},
				},
			},
		},
	}
	operations := validatePluginAnalyticsManifest(manifest, root)
	if len(operations) != 1 || operations["scripts/measure.py"].OperationName != "run_measure" {
		t.Fatalf("operations = %#v", operations)
	}
	measurement := operations["scripts/measure.py"].Measurements["tokens"]
	if !reflect.DeepEqual(measurement.EnumDimensions["speed"], []string{"fast", "slow"}) {
		t.Fatalf("dimensions = %#v", measurement)
	}
}

func TestPluginMetricsSidecarParsesAndValidatesOutput(t *testing.T) {
	resolved := ResolvedPluginMetricsOperation{
		PluginID: "plugin-1",
		Operation: PluginMetricsOperation{
			OperationName: "run_measure",
			Measurements: map[string]PluginMeasurementDefinition{
				"tokens": {EnumDimensions: map[string][]string{"speed": {"fast", "slow"}}},
			},
		},
	}
	sidecar := NewPluginMetricsSidecar(resolved)
	if sidecar == nil {
		t.Fatal("NewPluginMetricsSidecar() = nil")
	}
	env := map[string]string{}
	sidecar.InstallOutputEnv(env)
	if env[PluginMetricsOutputEnvVar] == "" {
		t.Fatal("output env not installed")
	}
	if err := os.WriteFile(sidecar.OutputPath(), []byte(`{"version":1,"measurements":[{"name":"tokens","value":12.5,"dimensions":{"speed":"fast"}},{"name":"tokens","value":3,"dimensions":{"speed":"invalid"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	batch := sidecar.Finish(0)
	if batch == nil || batch.PluginID != "plugin-1" || len(batch.Rows) != 1 || batch.Rows[0].NumberValue != 12.5 {
		t.Fatalf("batch = %#v", batch)
	}
	if _, err := os.Stat(sidecar.OutputPath()); !os.IsNotExist(err) {
		t.Fatalf("sidecar output was not cleaned up")
	}
}

func TestValidatePluginMetricsOperationPathRejectsEscapesAndUnsafePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "ok.py"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := validatePluginMetricsOperationPath(root, "scripts/ok.py"); got != "scripts/ok.py" {
		t.Fatalf("safe path = %q", got)
	}
	for _, path := range []string{"../ok.py", "scripts/../ok.py", "scripts/../../etc/passwd", "\\absolute"} {
		if got := validatePluginMetricsOperationPath(root, path); got != "" {
			t.Fatalf("unsafe path %q resolved to %q", path, got)
		}
	}
}

func TestResolveMetricsOperationBindsExactTrustedCommand(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scripts", "measure.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := TrustedPluginRoots{roots: []trustedPluginRoot{{
		pluginID: "plugin-1",
		root:     root,
		metricsOperationsByPath: map[string]PluginMetricsOperation{
			"scripts/measure.py": {OperationName: "run_measure", Measurements: map[string]PluginMeasurementDefinition{}},
		},
	}}}
	resolved := roots.ResolveMetricsOperation([]string{"python3", filepath.Join(root, "scripts", "measure.py")}, root)
	if resolved == nil || resolved.PluginID != "plugin-1" || resolved.Operation.OperationName != "run_measure" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestValidPluginMetricIdentifier(t *testing.T) {
	for _, value := range []string{"a", "abc_def1"} {
		if !validPluginMetricIdentifier(value) {
			t.Fatalf("%q should be valid", value)
		}
	}
	for _, value := range []string{"", "A", "1abc", "a-b", "a.b", "a" + string(make([]byte, 65))} {
		if validPluginMetricIdentifier(value) {
			t.Fatalf("%q should be invalid", value)
		}
	}
}

// TestResolveMetricsOperationBindsToMatchedTrustedRootVersion covers Rust
// #46528: a measurement declaration must come from the plugin version whose
// root matched the script, not from another version of the same plugin.
func TestResolveMetricsOperationBindsToMatchedTrustedRootVersion(t *testing.T) {
	home := t.TempDir()
	older := filepath.Join(home, "plugins", "cache", trustedRemoteMarketplaceName, "sample", "1.0.0")
	newer := filepath.Join(home, "plugins", "cache", trustedRemoteMarketplaceName, "sample", "2.0.0")
	olderScript := filepath.Join(older, "scripts", "measure.py")
	newerScript := filepath.Join(newer, "scripts", "measure.py")
	for _, script := range []string{olderScript, newerScript} {
		if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(script, []byte("ok"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id, err := ParsePluginId("sample@" + trustedRemoteMarketplaceName)
	if err != nil {
		t.Fatal(err)
	}
	roots := TrustedPluginRoots{roots: []trustedPluginRoot{
		{
			pluginID: id.Key(), version: "1.0.0", root: older,
			metricsOperationsByPath: map[string]PluginMetricsOperation{
				"scripts/measure.py": {OperationName: "run_measure", Measurements: map[string]PluginMeasurementDefinition{}},
			},
		},
		{
			pluginID: id.Key(), version: "2.0.0", root: newer,
			metricsOperationsByPath: map[string]PluginMetricsOperation{},
		},
	}}

	matched := roots.ResolveMetricsOperation([]string{"python3", olderScript}, older)
	if matched == nil || matched.Operation.OperationName != "run_measure" {
		t.Fatalf("older version declaration = %#v, want run_measure", matched)
	}
	if got := roots.ResolveMetricsOperation([]string{"python3", newerScript}, newer); got != nil {
		t.Fatalf("newer version inherited another version's declaration: %#v", got)
	}
	// Attribution still spans versions with matching contents.
	if attribution := roots.Resolve([]string{"python3", newerScript}, newer); attribution == nil || attribution.PluginID != id.Key() {
		t.Fatalf("attribution = %#v, want the plugin identity", attribution)
	}
}
