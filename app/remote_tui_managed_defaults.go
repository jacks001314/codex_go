package app

// Rust parity: codex-rs/tui/src/app/new_session.rs +
// managed_new_thread_defaults.rs (#44693). When the TUI starts a fresh thread it
// reads the server's effective config and requirements, then overlays the
// managed new-thread defaults for model / reasoning effort / service tier that
// the user did not explicitly choose for this launch.

import (
	"context"
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
	cwd := strings.TrimSpace(params.CWD)
	defaults, layers, effective, ok := c.remoteNewThreadModelDefaults(ctx, cwd)
	if !ok {
		return params, nil
	}
	// Rust #43177: the server's config/read values seed model and reasoning
	// effort for launches that did not choose them explicitly.
	applyServerEffectiveLaunchDefaults(
		&params,
		effective,
		layers,
		remoteCLIConfigOverrideKeys(root),
		root != nil && strings.TrimSpace(root.Shared.Model) != "",
		c.serverCatalogDefaultModel(ctx),
	)
	applyManagedDefaultsToThreadStartParams(&params, state, defaults, layers,
		remoteCLIConfigOverrideKeys(root),
		root != nil && strings.TrimSpace(root.Shared.Model) != "",
		params.ServiceTierSet)
	return params, nil
}

// applyServerEffectiveLaunchDefaults overlays the server's effective config
// onto a thread/start request for settings the user did not choose explicitly
// (Rust #43177). Explicit CLI `--model`/profile/generic overrides win; when the
// server reports no configured model the server catalog's default is used.
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
	if target.Model == "" && state != nil {
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
func (c *remoteAppServerTUIClient) remoteNewThreadModelDefaults(ctx context.Context, cwd string) (*codextui.ManagedNewThreadDefaults, []config.Layer, map[string]any, bool) {
	params := config.ConfigReadParams{IncludeLayers: true}
	if trimmed := strings.TrimSpace(cwd); trimmed != "" {
		params.CWD = &trimmed
	}
	var read config.ConfigReadResponse
	if err := c.remoteConfigRequest(ctx, appserver.MethodConfigRead, params, &read); err != nil {
		return nil, nil, nil, false
	}
	var requirements config.ConfigRequirementsReadResponse
	if err := c.remoteConfigRequest(ctx, appserver.MethodConfigRequirementsRead, map[string]any{}, &requirements); err != nil {
		return nil, read.Layers, read.Config, true
	}
	defaults := newThreadModelDefaultsFromRequirements(requirements.Requirements)
	return defaults, read.Layers, read.Config, true
}

// newThreadModelDefaultsFromRequirements converts the wire requirements into
// the TUI's managed defaults (nil when no model defaults are configured).
// localNewThreadModelDefaults reads the same managed defaults and layers from
// the local config service for the embedded TUI bootstrap path.
func localNewThreadModelDefaults(codexHome string) (*codextui.ManagedNewThreadDefaults, []config.Layer, bool) {
	service := config.NewConfigService(strings.TrimSpace(codexHome))
	read, err := service.Read(&config.ConfigReadParams{IncludeLayers: true})
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
