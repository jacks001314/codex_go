package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/features"
	codextea "codex_go/tui/tea"
)

// TestDaemonCompatibilityRequestedFeaturesLikeRust covers Rust #55442's
// extraction: dotted keys and a `features` table merge into one layer (a table
// assignment replaces earlier dotted keys), removed features are skipped, and
// aliases canonicalize.
func TestDaemonCompatibilityRequestedFeaturesLikeRust(t *testing.T) {
	cases := []struct {
		name string
		root *cli.RootOptions
		want map[string]bool
	}{
		{
			name: "enable flag",
			root: &cli.RootOptions{EnableFeatures: []string{"api_key_model_discovery"}},
			want: map[string]bool{"api_key_model_discovery": true},
		},
		{
			name: "disable via config override",
			root: &cli.RootOptions{ConfigOverrides: []string{"features.code_mode_host=false"}},
			want: map[string]bool{"code_mode_host": false},
		},
		{
			name: "legacy alias canonicalizes",
			root: &cli.RootOptions{ConfigOverrides: []string{"features.connectors=true"}},
			want: map[string]bool{"apps": true},
		},
		{
			name: "removed feature is inert",
			root: &cli.RootOptions{ConfigOverrides: []string{"features.remote_models=true"}},
			want: map[string]bool{},
		},
		{
			name: "unknown feature stays literal",
			root: &cli.RootOptions{ConfigOverrides: []string{"features.zz_missing=true"}},
			want: map[string]bool{"zz_missing": true},
		},
		{
			name: "non boolean toggle is ignored",
			root: &cli.RootOptions{ConfigOverrides: []string{`features.apps="yes"`}},
			want: map[string]bool{},
		},
		{
			name: "table replaces earlier dotted keys",
			root: &cli.RootOptions{ConfigOverrides: []string{
				"features.zz_overridden=true",
				"features={api_key_model_discovery=false,code_mode_host=false,instant_interrupt=true}",
				"features.code_mode_host=true",
			}},
			want: map[string]bool{
				"api_key_model_discovery": false,
				"code_mode_host":          true,
				"instant_interrupt":       true,
			},
		},
	}
	for _, testCase := range cases {
		got := daemonCompatibilityRequestedFeatures(testCase.root)
		if !FeatureOverridesEqualForTest(got, testCase.want) {
			t.Fatalf("%s: requested = %#v, want %#v", testCase.name, got, testCase.want)
		}
	}
}

func daemonCompatibilityTestCatalog(apiKeyModelDiscoveryEnabled bool) []codextea.ExperimentalFeatureEntry {
	return []codextea.ExperimentalFeatureEntry{
		{Name: "api_key_model_discovery", Enabled: apiKeyModelDiscoveryEnabled, Stage: string(features.FeatureStageBeta)},
		{Name: "code_mode_host", Enabled: false, Stage: string(features.FeatureStageBeta)},
		{Name: "instant_interrupt", Enabled: true, Stage: string(features.FeatureStageBeta)},
		{Name: "remote_compaction_v2", Enabled: false, Stage: string(features.FeatureStageStable)},
		{Name: "shell_tool", Enabled: false, Stage: string(features.FeatureStageRemoved)},
	}
}

// TestDaemonFeatureMismatchesLikeRust mirrors Rust's
// daemon_feature_compatibility_respects_required_and_optional_attachment matrix:
// managed layers and requirements win over launch flags, removed features are
// inert, an unreported feature counts as disabled, and the restart set is what a
// restart would persist.
func TestDaemonFeatureMismatchesLikeRust(t *testing.T) {
	legacyLayer := config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceLegacyManagedConfigFromFile},
		Config: map[string]any{"features": map[string]any{"api_key_model_discovery": true}},
	}
	plainLayer := config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceLegacyManagedConfigFromFile},
		Config: map[string]any{},
	}
	managedRequirements := &config.ConfigRequirements{FeatureRequirements: map[string]bool{"api_key_model_discovery": true}}

	cases := []struct {
		name      string
		requested map[string]bool
		readback  *daemonCompatibilityReadback
		want      string
		restart   map[string]bool
	}{
		{
			name: "matching",
			// `remote_compaction_v2` is dropped during extraction (the Go registry
			// marks it removed), and a catalog entry whose stage is removed is
			// inert here, just as in Rust.
			requested: map[string]bool{
				"instant_interrupt": true,
				"shell_tool":        true,
				"zz_missing":        false,
			},
			readback: &daemonCompatibilityReadback{Layers: []config.Layer{plainLayer}, Catalog: daemonCompatibilityTestCatalog(true)},
		},
		{
			name:      "conflicting client",
			requested: map[string]bool{"api_key_model_discovery": false},
			readback:  &daemonCompatibilityReadback{Layers: []config.Layer{plainLayer}, Catalog: daemonCompatibilityTestCatalog(true)},
			want:      "This session requires api_key_model_discovery to be disabled",
			restart:   map[string]bool{"api_key_model_discovery": false},
		},
		{
			name:      "explicit enable",
			requested: map[string]bool{"api_key_model_discovery": true},
			readback:  &daemonCompatibilityReadback{Layers: []config.Layer{plainLayer}, Catalog: daemonCompatibilityTestCatalog(false)},
			want:      "This session requires api_key_model_discovery to be enabled",
			restart:   map[string]bool{"api_key_model_discovery": true},
		},
		{
			name: "mixed flags",
			requested: map[string]bool{
				"api_key_model_discovery": false,
				"code_mode_host":          true,
				"instant_interrupt":       true,
			},
			readback: &daemonCompatibilityReadback{Layers: []config.Layer{plainLayer}, Catalog: daemonCompatibilityTestCatalog(true)},
			want:     "This session requires api_key_model_discovery to be disabled",
			restart: map[string]bool{
				"api_key_model_discovery": false,
				"code_mode_host":          true,
			},
		},
		{
			name:      "missing enabled",
			requested: map[string]bool{"zz_missing": true},
			readback:  &daemonCompatibilityReadback{Layers: []config.Layer{plainLayer}, Catalog: daemonCompatibilityTestCatalog(true)},
			want:      "This session requires zz_missing to be enabled",
			restart:   map[string]bool{"zz_missing": true},
		},
		{
			name:      "managed requirements override the launch",
			requested: map[string]bool{"api_key_model_discovery": false},
			readback:  &daemonCompatibilityReadback{Layers: []config.Layer{plainLayer}, Requirements: managedRequirements, Catalog: daemonCompatibilityTestCatalog(true)},
		},
		{
			name:      "legacy managed layer overrides the launch",
			requested: map[string]bool{"api_key_model_discovery": false},
			readback:  &daemonCompatibilityReadback{Layers: []config.Layer{legacyLayer}, Catalog: daemonCompatibilityTestCatalog(true)},
		},
		{
			name:      "disabled layer does not override the launch",
			requested: map[string]bool{"api_key_model_discovery": false},
			readback: func() *daemonCompatibilityReadback {
				disabled := "disabled by policy"
				layer := legacyLayer
				layer.DisabledReason = &disabled
				return &daemonCompatibilityReadback{Layers: []config.Layer{layer}, Catalog: daemonCompatibilityTestCatalog(true)}
			}(),
			want:    "This session requires api_key_model_discovery to be disabled",
			restart: map[string]bool{"api_key_model_discovery": false},
		},
	}
	for _, testCase := range cases {
		reason, restart := daemonFeatureMismatches(testCase.requested, testCase.readback)
		if reason != testCase.want {
			t.Fatalf("%s: reason = %q, want %q", testCase.name, reason, testCase.want)
		}
		if !FeatureOverridesEqualForTest(restart, testCase.restart) {
			t.Fatalf("%s: restart = %#v, want %#v", testCase.name, restart, testCase.restart)
		}
	}
}

// TestDaemonCompatibilityCheckFallbackLikeRust covers the warning/issue split:
// an optional attachment falls back to embedded mode only when a restart could
// not resolve the problem.
func TestDaemonCompatibilityCheckFallbackLikeRust(t *testing.T) {
	originalRead := daemonCompatibilityRead
	t.Cleanup(func() { daemonCompatibilityRead = originalRead })

	root := &cli.RootOptions{ConfigOverrides: []string{"features.api_key_model_discovery=false"}}
	endpoint := appserverdaemon.NewUnixSocketEndpoint("/tmp/codex.sock")

	daemonCompatibilityRead = func(context.Context, *appserverdaemon.RemoteAppServerEndpoint, string, bool) (*daemonCompatibilityReadback, error) {
		return nil, errDaemonCompatibilityConnect
	}
	warning, issue := daemonCompatibilityCheck(context.Background(), root, endpoint, true)
	if issue != nil || warning != "Running without the shared background server: could not connect to check daemon feature settings." {
		t.Fatalf("fallback warning = %q, issue = %#v", warning, issue)
	}
	warning, issue = daemonCompatibilityCheck(context.Background(), root, endpoint, false)
	if warning != "" || issue == nil || issue.Error() != "Cannot use the shared background server: could not connect to check daemon feature settings.\n"+daemonFailureHint {
		t.Fatalf("required-daemon warning = %q, issue = %#v", warning, issue)
	}

	// A restartable mismatch is never silently downgraded, even when the launch
	// could otherwise fall back.
	daemonCompatibilityRead = func(context.Context, *appserverdaemon.RemoteAppServerEndpoint, string, bool) (*daemonCompatibilityReadback, error) {
		return &daemonCompatibilityReadback{Layers: []config.Layer{{Name: config.LayerSource{Type: config.LayerSourceUser}, Config: map[string]any{}}}, Catalog: daemonCompatibilityTestCatalog(true)}, nil
	}
	warning, issue = daemonCompatibilityCheck(context.Background(), root, endpoint, true)
	if warning != "" || issue == nil || issue.Reason != "This session requires api_key_model_discovery to be disabled" {
		t.Fatalf("mismatch warning = %q, issue = %#v", warning, issue)
	}
	if !FeatureOverridesEqualForTest(issue.RestartFeatures, map[string]bool{"api_key_model_discovery": false}) {
		t.Fatalf("mismatch restart features = %#v", issue.RestartFeatures)
	}

	// The feature catalog write path failing is not restartable, so an optional
	// attachment still falls back.
	daemonCompatibilityRead = func(context.Context, *appserverdaemon.RemoteAppServerEndpoint, string, bool) (*daemonCompatibilityReadback, error) {
		return nil, errors.New("Experimental feature request failed")
	}
	warning, issue = daemonCompatibilityCheck(context.Background(), root, endpoint, true)
	if issue != nil || warning != "Running without the shared background server: Experimental feature request failed." {
		t.Fatalf("catalog failure warning = %q, issue = %#v", warning, issue)
	}
	warning, issue = daemonCompatibilityCheck(context.Background(), root, endpoint, false)
	if warning != "" || issue == nil || issue.RestartFeatures != nil {
		t.Fatalf("catalog failure warning = %q, issue = %#v", warning, issue)
	}

	daemonCompatibilityRead = func(context.Context, *appserverdaemon.RemoteAppServerEndpoint, string, bool) (*daemonCompatibilityReadback, error) {
		return &daemonCompatibilityReadback{Catalog: daemonCompatibilityTestCatalog(false)}, nil
	}
	if warning, issue := daemonCompatibilityCheck(context.Background(), root, endpoint, false); warning != "" || issue != nil {
		t.Fatalf("matching launch = %q, %#v", warning, issue)
	}
}

// TestReadDaemonCompatibilityWireLikeRust covers the real transport path Rust's
// compatibility check uses: config/read with layers, configRequirements/read
// (including the tolerated pre-requirements servers), and the paginated
// experimentalFeature/list catalog.
func TestReadDaemonCompatibilityWireLikeRust(t *testing.T) {
	cases := []struct {
		name             string
		requirementsMode string
		wantErr          error
		wantRequirements *config.ConfigRequirements
	}{
		{name: "requirements"},
		{
			name:             "old server unknown variant",
			requirementsMode: "unknown variant",
		},
		{
			name:             "old server method not found",
			requirementsMode: "method not found",
		},
		{
			name:             "requirements failure",
			requirementsMode: "server error",
			wantErr:          errDaemonCompatibilityRequirements,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var sawConfigRead, sawRequirementsRead bool
			path := startUnixWebSocketTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
				for {
					request, err := remoteTUITestReadRequest(ctx, conn)
					if err != nil {
						return
					}
					switch appserver.Method(request.Method) {
					case appserver.MethodInitialize:
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
					case appserver.MethodConfigRead:
						sawConfigRead = true
						var params config.ConfigReadParams
						_ = json.Unmarshal(request.Params, &params)
						if !params.IncludeLayers || params.CWD == nil || *params.CWD != "/work" {
							t.Errorf("config/read params = %#v", params)
						}
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{
							"config":  map[string]any{},
							"origins": map[string]any{},
							"layers": []any{map[string]any{
								"name":   map[string]any{"type": string(config.LayerSourceLegacyManagedConfigFromFile)},
								"config": map[string]any{"features": map[string]any{"api_key_model_discovery": true}},
							}},
						}})
					case appserver.MethodConfigRequirementsRead:
						sawRequirementsRead = true
						switch testCase.requirementsMode {
						case "unknown variant":
							remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": appserver.JSONRPCInvalidRequestErrorCode, "message": "Invalid request: unknown variant `configRequirements/read`"}})
						case "method not found":
							remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": appserver.JSONRPCMethodNotFoundErrorCode, "message": "method not found"}})
						case "server error":
							remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": appserver.JSONRPCInternalErrorCode, "message": "boom"}})
						default:
							remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{
								"requirements": map[string]any{"featureRequirements": map[string]any{"api_key_model_discovery": true}},
							}})
						}
					case appserver.MethodExperimentalFeatureList:
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{
							"data": []any{map[string]any{
								"name":           "api_key_model_discovery",
								"stage":          string(features.FeatureStageBeta),
								"enabled":        true,
								"defaultEnabled": false,
							}},
							"nextCursor": nil,
						}})
					default:
						t.Errorf("unexpected request %q", request.Method)
					}
				}
			})
			readback, err := readDaemonCompatibility(context.Background(), appserverdaemon.NewUnixSocketEndpoint(path), "/work", true)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
			if !sawConfigRead || !sawRequirementsRead {
				t.Fatalf("config/read = %v, requirements = %v", sawConfigRead, sawRequirementsRead)
			}
			if testCase.wantErr != nil {
				return
			}
			if len(readback.Layers) != 1 || readback.Layers[0].Name.Type != config.LayerSourceLegacyManagedConfigFromFile {
				t.Fatalf("layers = %#v", readback.Layers)
			}
			if testCase.requirementsMode == "" {
				if readback.Requirements == nil || !readback.Requirements.FeatureRequirements["api_key_model_discovery"] {
					t.Fatalf("requirements = %#v", readback.Requirements)
				}
			} else if readback.Requirements != nil {
				t.Fatalf("requirements = %#v, want none for an old server", readback.Requirements)
			}
			if len(readback.Catalog) != 1 || readback.Catalog[0].Name != "api_key_model_discovery" || !readback.Catalog[0].Enabled {
				t.Fatalf("catalog = %#v", readback.Catalog)
			}
		})
	}
}

// TestReadDaemonCompatibilityConnectFailureLikeRust covers an unreachable
// control socket and the connect-only mode used when no feature toggles exist.
func TestReadDaemonCompatibilityConnectFailureLikeRust(t *testing.T) {
	if _, err := readDaemonCompatibility(context.Background(), appserverdaemon.NewUnixSocketEndpoint("/nonexistent/codex.sock"), "", true); !errors.Is(err, errDaemonCompatibilityConnect) {
		t.Fatalf("missing socket error = %v", err)
	}

	path := startUnixWebSocketTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		for {
			request, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			if appserver.Method(request.Method) != appserver.MethodInitialize {
				t.Errorf("connect-only check sent %q", request.Method)
				return
			}
			remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
		}
	})
	if readback, err := readDaemonCompatibility(context.Background(), appserverdaemon.NewUnixSocketEndpoint(path), "", false); err != nil || readback == nil {
		t.Fatalf("connect-only readback = %#v, err = %v", readback, err)
	}
}
