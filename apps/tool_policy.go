package apps

import "strings"

// This file ports Rust connectors::app_tool_policy: the effective enablement and
// approval policy for one Codex Apps tool. core/src/mcp_tool_call.rs uses it for
// the codex_apps server to block tools the app configuration disables ("MCP tool
// call blocked by app configuration") and to pick the tool's approval mode.

// AppToolPolicy is Rust's AppToolPolicy.
type AppToolPolicy struct {
	Enabled  bool
	Approval AppToolApproval
}

// DefaultAppToolPolicy mirrors Rust's AppToolPolicy::default (enabled, Auto).
func DefaultAppToolPolicy() AppToolPolicy {
	return AppToolPolicy{Enabled: true, Approval: AppToolApprovalAuto}
}

// AppToolPolicyInput is Rust's AppToolPolicyInput.
type AppToolPolicyInput struct {
	ConnectorID     string
	LinkID          string
	ToolName        string
	ToolTitle       string
	DestructiveHint *bool
	OpenWorldHint   *bool
}

// AppToolPolicyEvaluator resolves app tool policy against one immutable config
// snapshot (Rust AppToolPolicyEvaluator). Callers should build one per exposure
// or call batch.
type AppToolPolicyEvaluator struct {
	config *AppsConfig
}

// NewAppToolPolicyEvaluator returns an evaluator over the merged apps config.
func NewAppToolPolicyEvaluator(config *AppsConfig) *AppToolPolicyEvaluator {
	return &AppToolPolicyEvaluator{config: config}
}

// Policy returns the effective enablement and approval mode for one tool.
func (e *AppToolPolicyEvaluator) Policy(input AppToolPolicyInput) AppToolPolicy {
	if e == nil || e.config == nil {
		return DefaultAppToolPolicy()
	}
	return appToolPolicyFromConfig(e.config, input)
}

// AppEnabled returns the effective enablement for one connector (Rust
// AppToolPolicyEvaluator::app_enabled).
func (e *AppToolPolicyEvaluator) AppEnabled(connectorID string) bool {
	if e == nil || e.config == nil {
		return true
	}
	return AppIsEnabled(e.config, connectorID)
}

// ApplyAppEnabledState applies app policy without overriding source state for
// unconfigured apps (Rust AppToolPolicyEvaluator::apply_app_enabled_state).
func (e *AppToolPolicyEvaluator) ApplyAppEnabledState(entries []AppEntry) []AppEntry {
	if e == nil || e.config == nil {
		return entries
	}
	for i := range entries {
		if e.config.Default == nil {
			if _, ok := e.config.Apps[strings.TrimSpace(entries[i].ID)]; !ok {
				continue
			}
		}
		enabled := e.AppEnabled(entries[i].ID)
		entries[i].IsEnabled = enabled
		entries[i].Enabled = enabled
		entries[i].EnabledExplicit = true
	}
	return entries
}

// AppIsEnabled ports Rust app_is_enabled: the per-app `enabled` wins, then the
// `_default` `enabled`, both defaulting to true.
func AppIsEnabled(config *AppsConfig, connectorID string) bool {
	if config == nil {
		return true
	}
	defaultEnabled := true
	if config.Default != nil {
		defaultEnabled = config.Default.Enabled
	}
	connectorID = strings.TrimSpace(connectorID)
	if connectorID == "" {
		return defaultEnabled
	}
	app, ok := config.Apps[connectorID]
	if !ok {
		return defaultEnabled
	}
	// Rust's AppConfig::enabled defaults to true when the entry omits it.
	if app.Enabled == nil {
		return true
	}
	return *app.Enabled
}

func appToolPolicyFromConfig(config *AppsConfig, input AppToolPolicyInput) AppToolPolicy {
	app, hasApp := config.Apps[strings.TrimSpace(input.ConnectorID)]
	toolConfig, hasToolConfig := appToolConfigFor(app, hasApp, input)
	approval := appToolApprovalFor(config, app, hasApp, toolConfig, hasToolConfig, input)
	enabled := appToolEnabledFor(config, app, hasApp, toolConfig, hasToolConfig, input)
	return AppToolPolicy{Enabled: enabled, Approval: approval}
}

// appToolConfigFor ports Rust's tool lookup: the tool name first, then the tool
// title (the hosted catalog can key a tool by its display title).
func appToolConfigFor(app AppConfig, hasApp bool, input AppToolPolicyInput) (AppToolConfig, bool) {
	if !hasApp || len(app.Tools) == 0 {
		return AppToolConfig{}, false
	}
	if tool, ok := app.Tools[strings.TrimSpace(input.ToolName)]; ok {
		return tool, true
	}
	if title := strings.TrimSpace(input.ToolTitle); title != "" {
		if tool, ok := app.Tools[title]; ok {
			return tool, true
		}
	}
	return AppToolConfig{}, false
}

// appToolApprovalFor ports Rust's approval precedence: per-tool override, then
// the connected account's default, then the app default, then the apps default.
func appToolApprovalFor(
	config *AppsConfig,
	app AppConfig,
	hasApp bool,
	toolConfig AppToolConfig,
	hasToolConfig bool,
	input AppToolPolicyInput,
) AppToolApproval {
	if hasToolConfig && toolConfig.ApprovalMode != nil {
		return normalizeAppToolApproval(*toolConfig.ApprovalMode)
	}
	if hasApp && app.Links != nil {
		if link, ok := app.Links.Links[strings.TrimSpace(input.LinkID)]; ok && link.DefaultToolsApprovalMode != nil {
			return normalizeAppToolApproval(*link.DefaultToolsApprovalMode)
		}
	}
	if hasApp && app.DefaultToolsApprovalMode != nil {
		return normalizeAppToolApproval(*app.DefaultToolsApprovalMode)
	}
	if config.Default != nil && strings.TrimSpace(input.ConnectorID) != "" && config.Default.DefaultToolsApprovalMode != nil {
		return normalizeAppToolApproval(*config.Default.DefaultToolsApprovalMode)
	}
	return AppToolApprovalAuto
}

// appToolEnabledFor ports Rust's enablement precedence: connector enablement,
// then the per-tool `enabled`, then the app's `default_tools_enabled`, then the
// destructive/open-world hint policy (both hints default to true).
func appToolEnabledFor(
	config *AppsConfig,
	app AppConfig,
	hasApp bool,
	toolConfig AppToolConfig,
	hasToolConfig bool,
	input AppToolPolicyInput,
) bool {
	if !AppIsEnabled(config, input.ConnectorID) {
		return false
	}
	if hasToolConfig && toolConfig.Enabled != nil {
		return *toolConfig.Enabled
	}
	if hasApp && app.DefaultToolsEnabled != nil {
		return *app.DefaultToolsEnabled
	}
	destructiveEnabled := true
	if hasApp && app.DestructiveEnabled != nil {
		destructiveEnabled = *app.DestructiveEnabled
	} else if config.Default != nil {
		destructiveEnabled = config.Default.DestructiveEnabled
	}
	openWorldEnabled := true
	if hasApp && app.OpenWorldEnabled != nil {
		openWorldEnabled = *app.OpenWorldEnabled
	} else if config.Default != nil {
		openWorldEnabled = config.Default.OpenWorldEnabled
	}
	destructiveHint := true
	if input.DestructiveHint != nil {
		destructiveHint = *input.DestructiveHint
	}
	openWorldHint := true
	if input.OpenWorldHint != nil {
		openWorldHint = *input.OpenWorldHint
	}
	return (destructiveEnabled || !destructiveHint) && (openWorldEnabled || !openWorldHint)
}

// normalizeAppToolApproval maps an unset mode to Rust's default (Auto).
func normalizeAppToolApproval(mode AppToolApproval) AppToolApproval {
	if strings.TrimSpace(string(mode)) == "" {
		return AppToolApprovalAuto
	}
	return mode
}
