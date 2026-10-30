package app

// Rust parity: codex-rs/tui/src/app/new_session.rs +
// managed_new_thread_defaults.rs (#44693). When the TUI starts a fresh thread it
// reads the server's effective config and requirements, then overlays the
// managed new-thread defaults for model / reasoning effort / service tier that
// the user did not explicitly choose for this launch.

import (
	"context"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/config"
	modelpkg "codex_go/model"
	codextui "codex_go/tui"
)

func (c *remoteAppServerTUIClient) remoteManagedThreadStartParams(ctx context.Context, root *cli.RootOptions, state *codextui.State) (appserver.ThreadStartParams, error) {
	params, err := remoteThreadStartParams(root, state)
	if err != nil {
		return params, err
	}
	// Rust #56090/#56516: the first thread of this process sends only explicit
	// launch choices for permissions, so the app server resolves any saved
	// client approval policy/reviewer from its own configuration. Explicit
	// profile launches keep their resolved-configuration behavior.
	if state != nil && state.StartupThreadPending && !remoteTUILaunchSelectedProfile(root) {
		overrides := launchConfigOverrides(root)
		applyStartupApprovalSelection(&params, resolveStartupApprovalSelection(launchTypedApprovalPolicy(root), overrides))
		applyStartupApprovalsReviewer(&params, overrides)
	}
	cwd := strings.TrimSpace(params.CWD)
	cliKVOverrides := remoteCLIConfigOverrideKeys(root)
	harnessModelSet := root != nil && strings.TrimSpace(root.Shared.Model) != ""
	// Rust #50013 (`StartupLaunchChoices::configure`): a launch without an
	// explicit profile forwards only the model, provider and reasoning effort the
	// invocation selected. The resolved client configuration stays out of the
	// request so the app server owns its own new-thread defaults.
	launchChoices := startupLaunchChoicesFromLaunch(root)
	defaults, layers, effective, ok, readErr := c.remoteNewThreadModelDefaults(ctx, cwd)
	if readErr != nil {
		return params, readErr
	}
	if !ok {
		// Rust #50913 (`bootstrap_server_owned_start`): when the server does not
		// support `config/read` the implicit client settings are cleared instead
		// of being restored.
		launchChoices.configure(&params)
		return params, nil
	}
	if launchChoices.selectedProfile {
		// An explicit profile launch keeps the resolved-configuration path
		// (Rust #50013: `selected_profile` short-circuits `configure`).
		if launchChoices.hasProvider {
			params.ModelProvider = launchChoices.provider
		}
		// Rust #43177: the server's config/read values seed model and reasoning
		// effort for launches that did not choose them explicitly.
		applyServerEffectiveLaunchDefaults(
			&params,
			effective,
			layers,
			cliKVOverrides,
			harnessModelSet,
			c.serverCatalogDefaultModel(ctx),
		)
	} else {
		launchChoices.configure(&params)
	}
	applyManagedDefaultsToThreadStartParams(&params, state, defaults, layers,
		cliKVOverrides,
		harnessModelSet,
		params.ServiceTierSet,
		launchChoices.selectedProfile)
	return params, nil
}

// remoteTUILaunchSelectedProfile reports whether this launch selected an
// explicit configuration profile. Rust #50913
// (`StartupLaunchChoices::from_launch` + `uses_server_owned_fresh_bootstrap`):
// an explicit profile launch keeps the resolved-configuration path instead of
// the server-owned fresh bootstrap.
func remoteTUILaunchSelectedProfile(root *cli.RootOptions) bool {
	if root == nil {
		return false
	}
	return strings.TrimSpace(root.Shared.Profile) != ""
}

// applyServerEffectiveLaunchDefaults overlays the server's effective config
// onto a thread/start request for settings the user did not choose explicitly
// (Rust #43177). Explicit CLI `--model`/profile/generic overrides win; when the
// server reports no configured model the server catalog's default is used.
// This is the resolved-configuration path: an explicit profile launch (Rust
// #50013) resolves model and effort from the merged configuration. Launches
// without a profile use startupLaunchChoices instead, so their resolved defaults
// never reach the request as explicit overrides.
func applyServerEffectiveLaunchDefaults(
	params *appserver.ThreadStartParams,
	effective map[string]any,
	layers []config.Layer,
	cliKVOverrides []string,
	harnessModelSet bool,
	catalogDefault func() (string, bool),
) {
	if params == nil {
		return
	}
	if !harnessModelSet && !config.HasLaunchSetting(layers, cliKVOverrides, "model") {
		model := ""
		if effective != nil {
			if value, ok := effective["model"].(string); ok {
				model = strings.TrimSpace(value)
			}
		}
		if model == "" && catalogDefault != nil {
			if fallback, ok := catalogDefault(); ok {
				model = strings.TrimSpace(fallback)
			}
		}
		if model != "" {
			params.Model = model
		}
	}
	if !config.HasLaunchSetting(layers, cliKVOverrides, "model_reasoning_effort") {
		effort := ""
		if effective != nil {
			if value, ok := effective["model_reasoning_effort"].(string); ok {
				effort = strings.TrimSpace(value)
			}
		}
		if effort != "" {
			if params.Config == nil {
				params.Config = map[string]any{}
			}
			params.Config["model_reasoning_effort"] = effort
		}
	}
}

// serverCatalogDefaultModel resolves the server model catalog's default model
// (Rust #43177: `is_default`, else the first catalog entry).
func (c *remoteAppServerTUIClient) serverCatalogDefaultModel(ctx context.Context) func() (string, bool) {
	return func() (string, bool) {
		if c == nil {
			return "", false
		}
		var listed modelpkg.ModelListResponse
		if err := remoteSessionRequest(ctx, c, appserver.MethodModelList, modelpkg.ModelListParams{}, &listed); err != nil {
			return "", false
		}
		presets := listed.Data
		if len(presets) == 0 {
			presets = listed.Models
		}
		for index := range presets {
			if presets[index].IsDefault {
				return strings.TrimSpace(presets[index].Model), true
			}
		}
		if len(presets) > 0 {
			return strings.TrimSpace(presets[0].Model), true
		}
		return "", false
	}
}

// applyManagedDefaultsToThreadStartParams overlays the managed new-thread
// defaults onto an already-built thread/start request (Rust
// apply_managed_new_thread_defaults + Config). It is shared by the interactive
// new-thread path and background-task creation.
func applyManagedDefaultsToThreadStartParams(
	params *appserver.ThreadStartParams,
	state *codextui.State,
	defaults *codextui.ManagedNewThreadDefaults,
	layers []config.Layer,
	cliKVOverrides []string,
	harnessModelSet bool,
	harnessServiceTierSet bool,
	seedModelFromState bool,
) {
	if params == nil || defaults == nil {
		return
	}
	target := codextui.ManagedNewThreadDefaultsTarget{Model: strings.TrimSpace(params.Model)}
	if params.Config != nil {
		if effort, isString := params.Config["model_reasoning_effort"].(string); isString {
			target.ReasoningEffort = strings.TrimSpace(effort)
		}
		if tier, isString := params.Config["service_tier"].(string); isString {
			target.ServiceTier = strings.TrimSpace(tier)
		}
	}
	if params.ServiceTier != nil {
		target.ServiceTier = strings.TrimSpace(*params.ServiceTier)
	} else if target.ServiceTier == "" && state != nil {
		target.ServiceTier = strings.TrimSpace(state.ServiceTier)
	}
	if target.Model == "" && state != nil && seedModelFromState {
		target.Model = strings.TrimSpace(state.Model)
	}
	codextui.ApplyManagedNewThreadDefaults(
		&target,
		defaults,
		layers,
		cliKVOverrides,
		harnessModelSet,
		harnessServiceTierSet,
	)
	if model := strings.TrimSpace(target.Model); model != "" {
		params.Model = model
	}
	if effort := strings.TrimSpace(target.ReasoningEffort); effort != "" {
		if params.Config == nil {
			params.Config = map[string]any{}
		}
		params.Config["model_reasoning_effort"] = effort
	}
	if tier := strings.TrimSpace(target.ServiceTier); tier != "" {
		value := tier
		params.ServiceTier = &value
		params.ServiceTierSet = true
	}
}

// remoteNewThreadModelDefaults reads the destination's effective config and the
// requirements' models.newThread defaults (Rust #43177/#43261: config/read for
// the relevant working directory). It reports ok=false when config/read fails,
// leaving the launch selection untouched; a nil defaults value means the server
// configures no managed new-thread defaults.
func (c *remoteAppServerTUIClient) remoteNewThreadModelDefaults(ctx context.Context, cwd string) (*codextui.ManagedNewThreadDefaults, []config.Layer, map[string]any, bool, error) {
	params := config.ConfigReadParams{IncludeLayers: true}
	if trimmed := strings.TrimSpace(cwd); trimmed != "" {
		params.CWD = &trimmed
	}
	var read config.ConfigReadResponse
	if err := c.remoteConfigRequest(ctx, appserver.MethodConfigRead, params, &read); err != nil {
		if remoteNewSessionDefaultsUnsupported(err) {
			// Rust read_effective_config_if_supported: a server without
			// config/read still starts threads from the client defaults.
			return nil, nil, nil, false, nil
		}
		// Any other failure aborts the replacement thread before thread/start so
		// the active session and the composed message stay intact (Rust #42671).
		return nil, nil, nil, false, &remoteNewSessionDefaultsError{err: err}
	}
	var requirements config.ConfigRequirementsReadResponse
	if err := c.remoteConfigRequest(ctx, appserver.MethodConfigRequirementsRead, map[string]any{}, &requirements); err != nil {
		return nil, read.Layers, read.Config, true, nil
	}
	defaults := newThreadModelDefaultsFromRequirements(requirements.Requirements)
	return defaults, read.Layers, read.Config, true, nil
}

// remoteNewSessionDefaultsError marks a real config/read failure for a
// replacement thread so the TUI reports Rust's "Failed to read new session
// defaults" message instead of the generic thread-start error.
type remoteNewSessionDefaultsError struct {
	err error
}

func (e *remoteNewSessionDefaultsError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *remoteNewSessionDefaultsError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// remoteNewSessionDefaultsUnsupported mirrors Rust's
// read_effective_config_if_supported matcher: only "method/variant unknown"
// means the server is too old to answer config/read.
func remoteNewSessionDefaultsUnsupported(err error) bool {
	var rpcErr *remoteRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	if rpcErr.Code == appserver.JSONRPCMethodNotFoundErrorCode {
		return true
	}
	if rpcErr.Code != jsonRPCInvalidRequestCode {
		return false
	}
	message := strings.ToLower(rpcErr.Message)
	return strings.Contains(message, "config/read") &&
		(strings.Contains(message, "unknown variant") || strings.Contains(message, "unknown method"))
}

// newThreadModelDefaultsFromRequirements converts the wire requirements into
// the TUI's managed defaults (nil when no model defaults are configured).
// localNewThreadModelDefaults reads the same managed defaults and layers from
// the local config service for the embedded TUI bootstrap path.
func localNewThreadModelDefaults(root *cli.RootOptions, cwd string) (*codextui.ManagedNewThreadDefaults, []config.Layer, bool) {
	service := interactiveConfigService(root)
	params := &config.ConfigReadParams{IncludeLayers: true}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		params.CWD = &cwd
	}
	read, err := service.Read(params)
	if err != nil || read == nil {
		return nil, nil, false
	}
	requirements := service.Requirements()
	if requirements == nil {
		return nil, read.Layers, false
	}
	defaults := newThreadModelDefaultsFromRequirements(requirements.Requirements)
	if defaults == nil {
		return nil, read.Layers, false
	}
	return defaults, read.Layers, true
}

func newThreadModelDefaultsFromRequirements(requirements *config.ConfigRequirements) *codextui.ManagedNewThreadDefaults {
	if requirements == nil || requirements.Models == nil || requirements.Models.NewThread == nil {
		return nil
	}
	newThread := requirements.Models.NewThread
	defaults := &codextui.ManagedNewThreadDefaults{
		Model:           stringPtrValueOrDefault(newThread.Model),
		ReasoningEffort: stringPtrValueOrDefault(newThread.ModelReasoningEffort),
		ServiceTier:     stringPtrValueOrDefault(newThread.ServiceTier),
	}
	if defaults.Model == "" && defaults.ReasoningEffort == "" && defaults.ServiceTier == "" {
		return nil
	}
	return defaults
}

func (c *remoteAppServerTUIClient) remoteConfigRequest(ctx context.Context, method appserver.Method, params any, target any) error {
	id, err := c.sendRequest(ctx, method, params)
	if err != nil {
		return err
	}
	return c.waitResponse(ctx, id, target)
}

// remoteCLIConfigOverrideKeys returns the generic `-c key=value` override paths
// for this launch, used by the launch-setting precedence check.
func remoteCLIConfigOverrideKeys(root *cli.RootOptions) []string {
	if root == nil || len(root.ConfigOverrides) == 0 {
		return nil
	}
	overrides, err := config.ParseOverrides(root.ConfigOverrides)
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(overrides))
	for _, override := range overrides {
		if path := strings.TrimSpace(override.Path); path != "" {
			keys = append(keys, path)
		}
	}
	return keys
}

func stringPtrValueOrDefault(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
