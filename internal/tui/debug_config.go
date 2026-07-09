package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"codex_go/internal/config"
	"codex_go/internal/sandbox"
)

// Rust parity: codex-rs/tui/src/debug_config.rs.

const DebugConfigCommand = "/debug-config"

type DebugConfigLine struct {
	Section string
	Text    string
}

type DebugConfigSessionNetworkProxy struct {
	HTTPAddr     string
	SOCKSAddr    string
	SOCKSEnabled bool
}

func DebugConfigSection(section string, lines []string) []DebugConfigLine {
	out := make([]DebugConfigLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, DebugConfigLine{Section: section, Text: line})
	}
	return out
}

func NewDebugConfigOutput(read *config.ConfigReadResponse, requirements *config.ConfigRequirements, proxy *DebugConfigSessionNetworkProxy) []string {
	lines := RenderDebugConfigLines(read, requirements, nil)
	if proxy != nil {
		lines = append(lines,
			"",
			"Session network proxy:",
			"  - all_proxy: "+SessionAllProxyURL(proxy.HTTPAddr, proxy.SOCKSAddr, proxy.SOCKSEnabled),
		)
	}
	return lines
}

func RenderDebugConfigLines(read *config.ConfigReadResponse, requirements *config.ConfigRequirements, sandboxModeAllowed func(sandbox.SandboxMode) bool) []string {
	lines := []string{DebugConfigCommand, ""}
	lines = append(lines, "Config layer stack (lowest precedence first):")
	layers := configLayersForDebug(read)
	if len(layers) == 0 {
		lines = append(lines, "  <none>")
	} else {
		for i, layer := range layers {
			status := "enabled"
			if layer.DisabledReason != nil {
				status = "disabled"
			}
			lines = append(lines, fmt.Sprintf("  %d. %s (%s)", i+1, FormatConfigLayerSource(layer.Name), status))
			lines = append(lines, renderNonFileLayerDetails(layer)...)
			if reason := strings.TrimSpace(stringPtrValueDebugConfig(layer.DisabledReason)); reason != "" {
				lines = append(lines, "     reason: "+reason)
			}
		}
	}

	lines = append(lines, "", "Requirements:")
	requirementLines := renderRequirementLines(requirements, sandboxModeAllowed)
	if len(requirementLines) == 0 {
		lines = append(lines, "  <none>")
	} else {
		lines = append(lines, requirementLines...)
	}
	return lines
}

func FormatConfigLayerSource(source config.LayerSource) string {
	switch source.Type {
	case config.LayerSourceMDM:
		return fmt.Sprintf("MDM (%s:%s)", source.Domain, source.Key)
	case config.LayerSourceSystem:
		return fmt.Sprintf("system (%s)", source.File)
	case config.LayerSourceEnterpriseManaged:
		return fmt.Sprintf("enterprise-managed (%s, %s)", source.Name, source.ID)
	case config.LayerSourceUser:
		return fmt.Sprintf("user (%s)", source.File)
	case config.LayerSourceProject:
		return fmt.Sprintf("project (%s/config.toml)", source.DotCodexFolder)
	case config.LayerSourceSessionFlags:
		return "session-flags"
	case config.LayerSourceLegacyManagedConfigFromFile:
		return fmt.Sprintf("legacy managed_config.toml (%s)", source.File)
	case config.LayerSourceLegacyManagedConfigFromMDM:
		return "legacy managed_config.toml (MDM)"
	default:
		if source.Type != "" {
			return string(source.Type)
		}
		return "<unknown>"
	}
}

func SessionAllProxyURL(httpAddr string, socksAddr string, socksEnabled bool) string {
	if socksEnabled {
		return "socks5h://" + strings.TrimSpace(socksAddr)
	}
	return "http://" + strings.TrimSpace(httpAddr)
}

func configLayersForDebug(read *config.ConfigReadResponse) []config.Layer {
	if read == nil || len(read.Layers) == 0 {
		return nil
	}
	layers := append([]config.Layer(nil), read.Layers...)
	return layers
}

func renderNonFileLayerDetails(layer config.Layer) []string {
	switch layer.Name.Type {
	case config.LayerSourceSessionFlags:
		return renderSessionFlagDetails(layer.Config)
	case config.LayerSourceMDM, config.LayerSourceEnterpriseManaged, config.LayerSourceLegacyManagedConfigFromMDM:
		return renderNonFileLayerValue(layer)
	default:
		return nil
	}
}

func renderSessionFlagDetails(value any) []string {
	var pairs []debugConfigPair
	flattenDebugConfigValues(value, "", &pairs)
	if len(pairs) == 0 {
		return []string{"     - <none>"}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })
	lines := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		lines = append(lines, fmt.Sprintf("     - %s = %s", pair.Key, pair.Value))
	}
	return lines
}

func renderNonFileLayerValue(layer config.Layer) []string {
	label := nonFileLayerValueLabel(layer.Name)
	value := formatDebugConfigValue(layer.Config)
	if strings.TrimSpace(value) == "" {
		return []string{fmt.Sprintf("     %s: <empty>", label)}
	}
	if strings.Contains(value, "\n") {
		out := []string{fmt.Sprintf("     %s:", label)}
		for _, line := range strings.Split(strings.TrimRight(value, "\r\n"), "\n") {
			out = append(out, "       "+strings.TrimRight(line, "\r"))
		}
		return out
	}
	return []string{fmt.Sprintf("     %s: %s", label, value)}
}

func nonFileLayerValueLabel(source config.LayerSource) string {
	switch source.Type {
	case config.LayerSourceMDM, config.LayerSourceLegacyManagedConfigFromMDM:
		return "MDM value"
	case config.LayerSourceEnterpriseManaged:
		return "Enterprise-managed config value"
	default:
		return "Layer value"
	}
}

type debugConfigPair struct {
	Key   string
	Value string
}

func flattenDebugConfigValues(value any, prefix string, out *[]debugConfigPair) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flattenDebugConfigValues(typed[key], next, out)
		}
	case map[string]string:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			*out = append(*out, debugConfigPair{Key: next, Value: formatDebugConfigValue(typed[key])})
		}
	default:
		key := prefix
		if key == "" {
			key = "<value>"
		}
		*out = append(*out, debugConfigPair{Key: key, Value: formatDebugConfigValue(value)})
	}
}

func renderRequirementLines(requirements *config.ConfigRequirements, sandboxModeAllowed func(sandbox.SandboxMode) bool) []string {
	if requirements == nil {
		return nil
	}
	lines := []string{}
	if requirements.AllowedApprovalPolicies != nil {
		lines = append(lines, requirementLine("allowed_approval_policies", joinOrEmptyDebugConfig(approvalPoliciesToStrings(requirements.AllowedApprovalPolicies)), "config requirements"))
	}
	if requirements.AllowedApprovalsReviewers != nil {
		lines = append(lines, requirementLine("allowed_approvals_reviewers", joinOrEmptyDebugConfig(approvalsReviewersToStrings(requirements.AllowedApprovalsReviewers)), "config requirements"))
	}
	if requirements.AllowedSandboxModes != nil {
		modes := make([]string, 0, len(requirements.AllowedSandboxModes))
		for _, mode := range requirements.AllowedSandboxModes {
			if sandboxModeAllowed != nil && !sandboxModeAllowed(mode) {
				continue
			}
			modes = append(modes, string(mode))
		}
		lines = append(lines, requirementLine("allowed_sandbox_modes", joinOrEmptyDebugConfig(modes), "config requirements"))
	}
	if requirements.AllowedWebSearchModes != nil {
		lines = append(lines, requirementLine("allowed_web_search_modes", joinOrEmptyDebugConfig(normalizeAllowedWebSearchModes(requirements.AllowedWebSearchModes)), "config requirements"))
	}
	if requirements.AllowManagedHooksOnly != nil {
		lines = append(lines, requirementLine("allow_managed_hooks_only", fmt.Sprint(*requirements.AllowManagedHooksOnly), "config requirements"))
	}
	if requirements.AllowAppshots != nil {
		lines = append(lines, requirementLine("allow_appshots", fmt.Sprint(*requirements.AllowAppshots), "config requirements"))
	}
	if requirements.AllowRemoteControl != nil {
		lines = append(lines, requirementLine("allow_remote_control", fmt.Sprint(*requirements.AllowRemoteControl), "config requirements"))
	}
	if len(requirements.FeatureRequirements) > 0 {
		lines = append(lines, requirementLine("features", joinOrEmptyDebugConfig(boolMapEntries(requirements.FeatureRequirements)), "config requirements"))
	}
	if requirements.Hooks != nil {
		lines = append(lines, requirementLine("hooks", FormatManagedHooksRequirements(requirements.Hooks), "config requirements"))
	}
	if requirements.EnforceResidency != nil {
		lines = append(lines, requirementLine("enforce_residency", string(*requirements.EnforceResidency), "config requirements"))
	}
	if requirements.Network != nil {
		lines = append(lines, requirementLine("experimental_network", FormatNetworkRequirements(requirements.Network), "config requirements"))
	}
	return lines
}

func FormatManagedHooksRequirements(hooks *config.ManagedHooksRequirements) string {
	if hooks == nil {
		return "<empty>"
	}
	parts := []string{}
	if value := strings.TrimSpace(stringPtrValueDebugConfig(hooks.ManagedDir)); value != "" {
		parts = append(parts, "managed_dir="+value)
	}
	if value := strings.TrimSpace(stringPtrValueDebugConfig(hooks.WindowsManagedDir)); value != "" {
		parts = append(parts, "windows_managed_dir="+value)
	}
	parts = append(parts, fmt.Sprintf("handlers=%d", managedHookHandlerCount(hooks)))
	return joinOrEmptyDebugConfig(parts)
}

func FormatNetworkRequirements(network *config.NetworkRequirements) string {
	if network == nil {
		return "<empty>"
	}
	parts := []string{}
	if network.Enabled != nil {
		parts = append(parts, fmt.Sprintf("enabled=%t", *network.Enabled))
	}
	if network.HTTPPort != nil {
		parts = append(parts, fmt.Sprintf("http_port=%d", *network.HTTPPort))
	}
	if network.SOCKSPort != nil {
		parts = append(parts, fmt.Sprintf("socks_port=%d", *network.SOCKSPort))
	}
	if network.AllowUpstreamProxy != nil {
		parts = append(parts, fmt.Sprintf("allow_upstream_proxy=%t", *network.AllowUpstreamProxy))
	}
	if network.DangerouslyAllowNonLoopbackProxy != nil {
		parts = append(parts, fmt.Sprintf("dangerously_allow_non_loopback_proxy=%t", *network.DangerouslyAllowNonLoopbackProxy))
	}
	if network.DangerouslyAllowAllUnixSockets != nil {
		parts = append(parts, fmt.Sprintf("dangerously_allow_all_unix_sockets=%t", *network.DangerouslyAllowAllUnixSockets))
	}
	if network.Domains != nil {
		parts = append(parts, "domains="+formatNetworkPermissionEntries(network.Domains))
	}
	if network.ManagedAllowedDomainsOnly != nil {
		parts = append(parts, fmt.Sprintf("managed_allowed_domains_only=%t", *network.ManagedAllowedDomainsOnly))
	}
	if network.UnixSockets != nil {
		parts = append(parts, "unix_sockets="+formatNetworkPermissionEntries(network.UnixSockets))
	}
	if network.AllowLocalBinding != nil {
		parts = append(parts, fmt.Sprintf("allow_local_binding=%t", *network.AllowLocalBinding))
	}
	return joinOrEmptyDebugConfig(parts)
}

func requirementLine(name string, value string, source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "<unspecified>"
	}
	return fmt.Sprintf("  - %s: %s (source: %s)", name, value, source)
}

func normalizeAllowedWebSearchModes(modes []config.WebSearchMode) []string {
	if len(modes) == 0 {
		return []string{string(config.WebSearchDisabled)}
	}
	out := make([]string, 0, len(modes)+1)
	hasDisabled := false
	for _, mode := range modes {
		if mode == config.WebSearchDisabled {
			hasDisabled = true
		}
		out = append(out, string(mode))
	}
	if !hasDisabled {
		out = append(out, string(config.WebSearchDisabled))
	}
	return out
}

func approvalPoliciesToStrings(values []sandbox.AskForApproval) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func approvalsReviewersToStrings(values []config.ApprovalsReviewer) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func boolMapEntries(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, fmt.Sprintf("%s=%t", key, values[key]))
	}
	return out
}

func managedHookHandlerCount(hooks *config.ManagedHooksRequirements) int {
	if hooks == nil {
		return 0
	}
	countGroups := func(groups []config.ConfiguredHookGroup) int {
		total := 0
		for _, group := range groups {
			total += len(group.Hooks)
		}
		return total
	}
	total := 0
	total += countGroups(hooks.PreToolUse)
	total += countGroups(hooks.PermissionRequest)
	total += countGroups(hooks.PostToolUse)
	total += countGroups(hooks.PreCompact)
	total += countGroups(hooks.PostCompact)
	total += countGroups(hooks.SessionStart)
	total += countGroups(hooks.UserPromptSubmit)
	total += countGroups(hooks.SubagentStart)
	total += countGroups(hooks.SubagentStop)
	total += countGroups(hooks.Stop)
	return total
}

func formatNetworkPermissionEntries(values map[string]config.NetworkPermission) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, values[key]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func joinOrEmptyDebugConfig(values []string) string {
	if len(values) == 0 {
		return "<empty>"
	}
	return strings.Join(values, ", ")
}

func formatDebugConfigValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return strconvQuoteDebugConfig(typed)
	case fmt.Stringer:
		return typed.String()
	default:
		data, err := json.Marshal(typed)
		if err == nil {
			return string(data)
		}
		return fmt.Sprint(typed)
	}
}

func strconvQuoteDebugConfig(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%q", value)
	}
	return string(data)
}

func stringPtrValueDebugConfig(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
