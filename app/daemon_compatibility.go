package app

// Rust parity: codex-rs/tui/src/daemon_startup.rs `compatibility_warning`
// (#55442). An interactive launch that may reuse the shared local daemon
// compares the feature toggles it was explicitly given with the daemon's
// reported configuration: legacy managed layers and managed requirements
// override the launch flags, removed features are inert, and a feature the
// daemon does not report at all is treated as disabled.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/features"
	codextea "codex_go/tui/tea"
)

const daemonCompatibilityTimeout = 10 * time.Second

// daemonCompatibilityIssue mirrors Rust `CompatibilityError`: the reason shown
// to the user plus, when the mismatch can be resolved by restarting the shared
// server, the feature settings that restart would persist.
type daemonCompatibilityIssue struct {
	Reason          string
	RestartFeatures map[string]bool
}

// Error matches the Rust display form so fatal startup output stays identical.
func (i *daemonCompatibilityIssue) Error() string {
	if i == nil {
		return ""
	}
	return fmt.Sprintf("Cannot use the shared background server: %s.\n%s", i.Reason, daemonFailureHint)
}

// daemonCompatibilityReadback is the daemon state the check compares against.
type daemonCompatibilityReadback struct {
	Layers       []config.Layer
	Requirements *config.ConfigRequirements
	Catalog      []codextea.ExperimentalFeatureEntry
}

var (
	errDaemonCompatibilityConnect      = errors.New("could not connect to check daemon feature settings")
	errDaemonCompatibilityConfig       = errors.New("could not read daemon configuration")
	errDaemonCompatibilityRequirements = errors.New("could not check daemon configuration requirements")
)

// daemonCompatibilityRead is the readback seam. includeDetails is false when
// the launch has no explicit feature toggles: Rust still connects so an
// unreachable daemon is reported, but asks the server nothing else.
var daemonCompatibilityRead = readDaemonCompatibility

// daemonCompatibilityRequestedFeatures mirrors the extraction in Rust
// `compatibility_warning`: the launch's `-c`/`--enable`/`--disable` overrides
// form one layer, a `features` table assignment replaces earlier dotted keys,
// removed features are skipped, and known aliases canonicalize.
func daemonCompatibilityRequestedFeatures(root *cli.RootOptions) map[string]bool {
	overrides, err := config.ParseOverrides(daemonRawLaunchOverrides(root))
	if err != nil {
		return nil
	}
	layer := config.BuildCLIOverridesLayer(overrides)
	table, ok := layer["features"].(map[string]any)
	if !ok {
		return nil
	}
	requested := make(map[string]bool, len(table))
	for name, value := range table {
		enabled, ok := value.(bool)
		if !ok {
			continue
		}
		if canonical, ok := features.CanonicalKey(name); ok {
			if features.StageFor(canonical) == features.StageRemoved {
				continue
			}
			requested[canonical] = enabled
			continue
		}
		requested[name] = enabled
	}
	return requested
}

// daemonFeatureMismatches applies Rust's filtering order and returns the
// mismatch reason for the first (sorted) feature plus every feature a restart
// would have to change.
func daemonFeatureMismatches(requested map[string]bool, readback *daemonCompatibilityReadback) (string, map[string]bool) {
	if len(requested) == 0 {
		return "", nil
	}
	pending := make(map[string]bool, len(requested))
	for name, enabled := range requested {
		pending[name] = enabled
	}
	if readback != nil {
		for _, layer := range readback.Layers {
			if layer.DisabledReason != nil {
				continue
			}
			switch layer.Name.Type {
			case config.LayerSourceLegacyManagedConfigFromFile, config.LayerSourceLegacyManagedConfigFromMDM:
			default:
				continue
			}
			for name := range daemonLayerFeatures(layer) {
				delete(pending, name)
			}
		}
		if readback.Requirements != nil {
			for name := range readback.Requirements.FeatureRequirements {
				delete(pending, name)
			}
		}
	}
	if len(pending) == 0 {
		return "", nil
	}
	catalog := map[string]codextea.ExperimentalFeatureEntry{}
	if readback != nil {
		for _, entry := range readback.Catalog {
			if _, ok := catalog[entry.Name]; !ok {
				catalog[entry.Name] = entry
			}
		}
	}
	mismatched := map[string]bool{}
	for name, enabled := range pending {
		entry, ok := catalog[name]
		if !ok {
			if enabled {
				mismatched[name] = enabled
			}
			continue
		}
		if entry.Stage == string(features.FeatureStageRemoved) {
			continue
		}
		if entry.Enabled != enabled {
			mismatched[name] = enabled
		}
	}
	if len(mismatched) == 0 {
		return "", nil
	}
	names := make([]string, 0, len(mismatched))
	for name := range mismatched {
		names = append(names, name)
	}
	sort.Strings(names)
	first := names[0]
	state := "disabled"
	if mismatched[first] {
		state = "enabled"
	}
	return fmt.Sprintf("This session requires %s to be %s", first, state), mismatched
}

// daemonLayerFeatures reads the `features` table a config layer supplies.
func daemonLayerFeatures(layer config.Layer) map[string]any {
	values, ok := layer.Config.(map[string]any)
	if !ok {
		return nil
	}
	table, _ := values["features"].(map[string]any)
	return table
}

// daemonCompatibilityCheck mirrors Rust `compatibility_warning`: it returns a
// startup warning when an optional attachment may fall back to embedded mode,
// and an issue when the launch must recover (or fail) instead.
func daemonCompatibilityCheck(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, allowEmbeddedFallback bool) (string, *daemonCompatibilityIssue) {
	requested := daemonCompatibilityRequestedFeatures(root)
	readback, err := daemonCompatibilityRead(backgroundContextIfNil(ctx), endpoint, interactiveSessionPickerCWD(root), len(requested) > 0)
	if err != nil {
		if allowEmbeddedFallback {
			return fmt.Sprintf("Running without the shared background server: %s.", err.Error()), nil
		}
		return "", &daemonCompatibilityIssue{Reason: err.Error()}
	}
	reason, mismatched := daemonFeatureMismatches(requested, readback)
	if reason == "" {
		return "", nil
	}
	if allowEmbeddedFallback && len(mismatched) == 0 {
		return fmt.Sprintf("Running without the shared background server: %s.", reason), nil
	}
	return "", &daemonCompatibilityIssue{Reason: reason, RestartFeatures: mismatched}
}

// readDaemonCompatibility connects to the daemon and collects the state the
// feature comparison needs. Every failure keeps Rust's exact reason text.
func readDaemonCompatibility(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, cwd string, includeDetails bool) (*daemonCompatibilityReadback, error) {
	if endpoint == nil {
		return nil, errDaemonCompatibilityConnect
	}
	readCtx, cancel := context.WithTimeout(backgroundContextIfNil(ctx), daemonCompatibilityTimeout)
	defer cancel()
	client, err := openRemoteSessionClient(readCtx, endpoint)
	if err != nil {
		return nil, errDaemonCompatibilityConnect
	}
	defer client.close()
	if !includeDetails {
		return &daemonCompatibilityReadback{}, nil
	}
	params := config.ConfigReadParams{IncludeLayers: true}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		params.CWD = &cwd
	}
	var read config.ConfigReadResponse
	if err := remoteSessionRequest(readCtx, client, appserver.MethodConfigRead, params, &read); err != nil {
		return nil, errDaemonCompatibilityConfig
	}
	readback := &daemonCompatibilityReadback{Layers: read.Layers}
	var requirements config.ConfigRequirementsReadResponse
	err = remoteSessionRequest(readCtx, client, appserver.MethodConfigRequirementsRead, nil, &requirements)
	if err != nil && !daemonRequirementsReadUnsupported(err) {
		return nil, errDaemonCompatibilityRequirements
	}
	if err == nil {
		readback.Requirements = requirements.Requirements
	}
	catalog, err := fetchExperimentalFeatures(readCtx, func(requestCtx context.Context, method appserver.Method, params any, target any) error {
		return remoteSessionRequest(requestCtx, client, method, params, target)
	}, "")
	if err != nil {
		return nil, err
	}
	readback.Catalog = catalog
	return readback, nil
}

// daemonRequirementsReadUnsupported mirrors Rust's tolerated
// configRequirements/read failures: servers that predate the request treat the
// method as missing.
func daemonRequirementsReadUnsupported(err error) bool {
	var rpcErr *remoteRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	if rpcErr.Code == appserver.JSONRPCMethodNotFoundErrorCode {
		return true
	}
	if rpcErr.Code != appserver.JSONRPCInvalidRequestErrorCode {
		return false
	}
	if !strings.Contains(rpcErr.Message, "configRequirements/read") {
		return false
	}
	return strings.Contains(rpcErr.Message, "unknown variant") || strings.Contains(rpcErr.Message, "unknown method")
}
