package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"codex_go/internal/sandbox"
)

func LoadRequirementsFile(path string) (*ConfigRequirements, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var values map[string]any
	if err := toml.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	if raw, ok := values["experimental_network"]; ok {
		networkValues, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid type for experimental_network: expected table")
		}
		if err := validateNetworkRequirementsTOML(networkValues); err != nil {
			return nil, err
		}
	}
	return ConfigRequirementsFromMap(values), nil
}

func ConfigRequirementsFromMap(values map[string]any) *ConfigRequirements {
	if len(values) == 0 {
		return nil
	}
	var out ConfigRequirements
	if values, ok := stringListAnyKey(values, "allowed_approval_policies", "allowedApprovalPolicies"); ok {
		out.AllowedApprovalPolicies = make([]sandbox.AskForApproval, 0, len(values))
		for _, value := range values {
			out.AllowedApprovalPolicies = append(out.AllowedApprovalPolicies, sandbox.AskForApproval(value))
		}
	}
	if values, ok := stringListAnyKey(values, "allowed_approvals_reviewers", "allowedApprovalsReviewers"); ok {
		out.AllowedApprovalsReviewers = make([]ApprovalsReviewer, 0, len(values))
		for _, value := range values {
			out.AllowedApprovalsReviewers = append(out.AllowedApprovalsReviewers, ApprovalsReviewer(value))
		}
	}
	if values, ok := stringListAnyKey(values, "allowed_sandbox_modes", "allowedSandboxModes"); ok {
		out.AllowedSandboxModes = make([]sandbox.SandboxMode, 0, len(values))
		for _, value := range values {
			out.AllowedSandboxModes = append(out.AllowedSandboxModes, sandbox.SandboxMode(value))
		}
	}
	if values, ok := stringListAnyKey(values, "allowed_windows_sandbox_implementations", "allowedWindowsSandboxImplementations"); ok {
		out.AllowedWindowsSandboxImplementations = make([]WindowsSandboxSetupMode, 0, len(values))
		for _, value := range values {
			out.AllowedWindowsSandboxImplementations = append(out.AllowedWindowsSandboxImplementations, WindowsSandboxSetupMode(value))
		}
	}
	if values, ok := boolMapAnyKey(values, "allowed_permission_profiles", "allowedPermissionProfiles"); ok {
		out.AllowedPermissionProfiles = values
	}
	if value, ok := stringAnyKey(values, "default_permissions", "defaultPermissions"); ok {
		out.DefaultPermissions = &value
	}
	if values, ok := stringListAnyKey(values, "allowed_web_search_modes", "allowedWebSearchModes"); ok {
		out.AllowedWebSearchModes = make([]WebSearchMode, 0, len(values))
		for _, value := range values {
			out.AllowedWebSearchModes = append(out.AllowedWebSearchModes, WebSearchMode(value))
		}
	}
	if value, ok := boolAnyKey(values, "allow_managed_hooks_only", "allowManagedHooksOnly"); ok {
		out.AllowManagedHooksOnly = &value
	}
	if value, ok := boolAnyKey(values, "allow_appshots", "allowAppshots"); ok {
		out.AllowAppshots = &value
	}
	if value, ok := boolAnyKey(values, "allow_remote_control", "allowRemoteControl"); ok {
		out.AllowRemoteControl = &value
	}
	if nested, ok := mapAnyKey(values, "computer_use", "computerUse"); ok {
		out.ComputerUse = computerUseRequirementsFromMap(nested)
	}
	if values, ok := boolMapAnyKey(values, "feature_requirements", "featureRequirements", "features"); ok {
		out.FeatureRequirements = values
	}
	if nested, ok := mapAnyKey(values, "hooks"); ok {
		out.Hooks = managedHooksRequirementsFromMap(nested)
	}
	if value, ok := stringAnyKey(values, "enforce_residency", "enforceResidency"); ok {
		residency := ResidencyRequirement(value)
		out.EnforceResidency = &residency
	}
	if nested, ok := mapAnyKey(values, "experimental_network"); ok {
		out.Network = networkRequirementsFromMap(nested)
	}
	if nested, ok := mapAnyKey(values, "models"); ok {
		out.Models = modelsRequirementsFromMap(nested)
	}
	if configRequirementsEmpty(&out) {
		return nil
	}
	return &out
}

func computerUseRequirementsFromMap(values map[string]any) *ComputerUseRequirements {
	var out ComputerUseRequirements
	if value, ok := boolAnyKey(values, "allow_locked_computer_use", "allowLockedComputerUse"); ok {
		out.AllowLockedComputerUse = &value
	}
	return &out
}

func managedHooksRequirementsFromMap(values map[string]any) *ManagedHooksRequirements {
	var out ManagedHooksRequirements
	if value, ok := stringAnyKey(values, "managed_dir", "managedDir"); ok {
		out.ManagedDir = &value
	}
	if value, ok := stringAnyKey(values, "windows_managed_dir", "windowsManagedDir"); ok {
		out.WindowsManagedDir = &value
	}
	out.PreToolUse = hookGroupsAnyKey(values, "PreToolUse", "pre_tool_use", "preToolUse")
	out.PermissionRequest = hookGroupsAnyKey(values, "PermissionRequest", "permission_request", "permissionRequest")
	out.PostToolUse = hookGroupsAnyKey(values, "PostToolUse", "post_tool_use", "postToolUse")
	out.PreCompact = hookGroupsAnyKey(values, "PreCompact", "pre_compact", "preCompact")
	out.PostCompact = hookGroupsAnyKey(values, "PostCompact", "post_compact", "postCompact")
	out.SessionStart = hookGroupsAnyKey(values, "SessionStart", "session_start", "sessionStart")
	out.UserPromptSubmit = hookGroupsAnyKey(values, "UserPromptSubmit", "user_prompt_submit", "userPromptSubmit")
	out.SubagentStart = hookGroupsAnyKey(values, "SubagentStart", "subagent_start", "subagentStart")
	out.SubagentStop = hookGroupsAnyKey(values, "SubagentStop", "subagent_stop", "subagentStop")
	out.Stop = hookGroupsAnyKey(values, "Stop", "stop")
	return &out
}

func networkRequirementsFromMap(values map[string]any) *NetworkRequirements {
	var out NetworkRequirements
	if value, ok := boolAnyKey(values, "enabled"); ok {
		out.Enabled = &value
	}
	if value, ok := uint16AnyKey(values, "http_port", "httpPort"); ok {
		out.HTTPPort = &value
	}
	if value, ok := uint16AnyKey(values, "socks_port", "socksPort"); ok {
		out.SOCKSPort = &value
	}
	if value, ok := boolAnyKey(values, "allow_upstream_proxy", "allowUpstreamProxy"); ok {
		out.AllowUpstreamProxy = &value
	}
	if value, ok := boolAnyKey(values, "dangerously_allow_non_loopback_proxy", "dangerouslyAllowNonLoopbackProxy"); ok {
		out.DangerouslyAllowNonLoopbackProxy = &value
	}
	if value, ok := boolAnyKey(values, "dangerously_allow_all_unix_sockets", "dangerouslyAllowAllUnixSockets"); ok {
		out.DangerouslyAllowAllUnixSockets = &value
	}
	if value, ok := networkPermissionMapAnyKey(values, "domains"); ok {
		out.Domains = value
	}
	if value, ok := boolAnyKey(values, "managed_allowed_domains_only", "managedAllowedDomainsOnly"); ok {
		out.ManagedAllowedDomainsOnly = &value
	}
	if value, ok := stringListAnyKey(values, "allowed_domains", "allowedDomains"); ok {
		out.AllowedDomains = value
	}
	if value, ok := stringListAnyKey(values, "denied_domains", "deniedDomains"); ok {
		out.DeniedDomains = value
	}
	if value, ok := networkPermissionMapAnyKey(values, "unix_sockets", "unixSockets"); ok {
		out.UnixSockets = value
	}
	if value, ok := stringListAnyKey(values, "allow_unix_sockets", "allowUnixSockets"); ok {
		out.AllowUnixSockets = value
	}
	if value, ok := boolAnyKey(values, "allow_local_binding", "allowLocalBinding"); ok {
		out.AllowLocalBinding = &value
	}
	normalizeLegacyNetworkRequirements(&out)
	return &out
}

func validateNetworkRequirementsTOML(values map[string]any) error {
	if _, hasDomains := values["domains"]; hasDomains {
		if _, hasAllowed := values["allowed_domains"]; hasAllowed {
			return fmt.Errorf("`experimental_network.domains` cannot be combined with legacy `allowed_domains` or `denied_domains`")
		}
		if _, hasDenied := values["denied_domains"]; hasDenied {
			return fmt.Errorf("`experimental_network.domains` cannot be combined with legacy `allowed_domains` or `denied_domains`")
		}
	}
	if _, hasUnixSockets := values["unix_sockets"]; hasUnixSockets {
		if _, hasLegacy := values["allow_unix_sockets"]; hasLegacy {
			return fmt.Errorf("`experimental_network.unix_sockets` cannot be combined with legacy `allow_unix_sockets`")
		}
	}

	for _, key := range []string{
		"enabled",
		"allow_upstream_proxy",
		"dangerously_allow_non_loopback_proxy",
		"dangerously_allow_all_unix_sockets",
		"managed_allowed_domains_only",
		"allow_local_binding",
	} {
		if raw, ok := values[key]; ok {
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("invalid type for experimental_network.%s: expected boolean", key)
			}
		}
	}
	for _, key := range []string{"http_port", "socks_port"} {
		if raw, ok := values[key]; ok {
			if _, ok := strictTOMLUint16(raw); !ok {
				return fmt.Errorf("invalid value for experimental_network.%s: expected an integer from 0 through 65535", key)
			}
		}
	}
	for _, key := range []string{"allowed_domains", "denied_domains", "allow_unix_sockets"} {
		if raw, ok := values[key]; ok {
			if !strictTOMLStringList(raw) {
				return fmt.Errorf("invalid type for experimental_network.%s: expected an array of strings", key)
			}
		}
	}
	for _, key := range []string{"domains", "unix_sockets"} {
		if raw, ok := values[key]; ok {
			if err := validateNetworkPermissionTable(key, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateNetworkPermissionTable(key string, raw any) error {
	values, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid type for experimental_network.%s: expected table", key)
	}
	for pattern, rawPermission := range values {
		permission, ok := rawPermission.(string)
		if !ok || (permission != string(NetworkAllow) && permission != string(NetworkDeny)) {
			return fmt.Errorf("invalid value for experimental_network.%s.%s: expected `allow` or `deny`", key, pattern)
		}
	}
	return nil
}

func strictTOMLUint16(value any) (uint16, bool) {
	switch value := value.(type) {
	case int64:
		if value >= 0 && value <= 65535 {
			return uint16(value), true
		}
	case uint64:
		if value <= 65535 {
			return uint16(value), true
		}
	}
	return 0, false
}

func strictTOMLStringList(value any) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

func normalizeLegacyNetworkRequirements(out *NetworkRequirements) {
	if out == nil {
		return
	}
	if out.Domains == nil && (out.AllowedDomains != nil || out.DeniedDomains != nil) {
		entries := make(map[string]NetworkPermission, len(out.AllowedDomains)+len(out.DeniedDomains))
		for _, pattern := range out.AllowedDomains {
			entries[pattern] = NetworkAllow
		}
		for _, pattern := range out.DeniedDomains {
			entries[pattern] = NetworkDeny
		}
		if len(entries) > 0 {
			out.Domains = entries
		}
	}
	out.AllowedDomains = nil
	out.DeniedDomains = nil
	if out.UnixSockets == nil && out.AllowUnixSockets != nil {
		entries := make(map[string]NetworkPermission, len(out.AllowUnixSockets))
		for _, path := range out.AllowUnixSockets {
			entries[path] = NetworkAllow
		}
		if len(entries) > 0 {
			out.UnixSockets = entries
		}
	}
	out.AllowUnixSockets = nil
}

func modelsRequirementsFromMap(values map[string]any) *ModelsRequirements {
	var out ModelsRequirements
	if nested, ok := mapAnyKey(values, "new_thread", "newThread"); ok {
		models := &NewThreadModelDefaults{}
		if value, ok := stringAnyKey(nested, "model"); ok {
			models.Model = &value
		}
		if value, ok := stringAnyKey(nested, "model_reasoning_effort", "modelReasoningEffort"); ok {
			models.ModelReasoningEffort = &value
		}
		if value, ok := stringAnyKey(nested, "service_tier", "serviceTier"); ok {
			models.ServiceTier = &value
		}
		out.NewThread = models
	}
	return &out
}

func hookGroupsAnyKey(values map[string]any, keys ...string) []ConfiguredHookGroup {
	raw, ok := anyKey(values, keys...)
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]ConfiguredHookGroup, 0, len(items))
	for _, item := range items {
		groupMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		group := ConfiguredHookGroup{}
		if value, ok := stringAnyKey(groupMap, "matcher"); ok {
			group.Matcher = &value
		}
		if hooks, ok := hookHandlersAnyKey(groupMap, "hooks"); ok {
			group.Hooks = hooks
		}
		out = append(out, group)
	}
	return out
}

func hookHandlersAnyKey(values map[string]any, keys ...string) ([]ConfiguredHookHandler, bool) {
	raw, ok := anyKey(values, keys...)
	if !ok {
		return nil, false
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]ConfiguredHookHandler, 0, len(items))
	for _, item := range items {
		handlerMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		handler := ConfiguredHookHandler{}
		if value, ok := stringAnyKey(handlerMap, "type"); ok {
			handler.Type = value
		}
		if value, ok := stringAnyKey(handlerMap, "command"); ok {
			handler.Command = value
		}
		if value, ok := stringAnyKey(handlerMap, "command_windows", "commandWindows"); ok {
			handler.CommandWindows = &value
		}
		if value, ok := uint64AnyKey(handlerMap, "timeout_sec", "timeoutSec"); ok {
			handler.TimeoutSec = &value
		}
		if value, ok := boolAnyKey(handlerMap, "async"); ok {
			handler.Async = value
		}
		if value, ok := stringAnyKey(handlerMap, "status_message", "statusMessage"); ok {
			handler.StatusMessage = &value
		}
		out = append(out, handler)
	}
	return out, true
}

func configRequirementsEmpty(value *ConfigRequirements) bool {
	return value == nil ||
		(value.AllowedApprovalPolicies == nil &&
			value.AllowedApprovalsReviewers == nil &&
			value.AllowedSandboxModes == nil &&
			value.AllowedWindowsSandboxImplementations == nil &&
			value.AllowedPermissionProfiles == nil &&
			value.DefaultPermissions == nil &&
			value.AllowedWebSearchModes == nil &&
			value.AllowManagedHooksOnly == nil &&
			value.AllowAppshots == nil &&
			value.AllowRemoteControl == nil &&
			value.ComputerUse == nil &&
			value.FeatureRequirements == nil &&
			value.Hooks == nil &&
			value.EnforceResidency == nil &&
			value.Network == nil &&
			value.Models == nil)
}

func mapAnyKey(values map[string]any, keys ...string) (map[string]any, bool) {
	raw, ok := anyKey(values, keys...)
	if !ok {
		return nil, false
	}
	nested, ok := raw.(map[string]any)
	return nested, ok
}

func stringAnyKey(values map[string]any, keys ...string) (string, bool) {
	raw, ok := anyKey(values, keys...)
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func boolAnyKey(values map[string]any, keys ...string) (bool, bool) {
	raw, ok := anyKey(values, keys...)
	if !ok {
		return false, false
	}
	switch value := raw.(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err == nil {
			return parsed, true
		}
	}
	return false, false
}

func uint16AnyKey(values map[string]any, keys ...string) (uint16, bool) {
	parsed, ok := uint64AnyKey(values, keys...)
	if !ok || parsed > 65535 {
		return 0, false
	}
	return uint16(parsed), true
}

func uint64AnyKey(values map[string]any, keys ...string) (uint64, bool) {
	raw, ok := anyKey(values, keys...)
	if !ok {
		return 0, false
	}
	switch value := raw.(type) {
	case int64:
		if value >= 0 {
			return uint64(value), true
		}
	case int:
		if value >= 0 {
			return uint64(value), true
		}
	case uint64:
		return value, true
	case string:
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func stringListAnyKey(values map[string]any, keys ...string) ([]string, bool) {
	raw, ok := anyKey(values, keys...)
	if !ok {
		return nil, false
	}
	out := stringListFromConfigValue(raw)
	if out == nil {
		return []string{}, true
	}
	return out, true
}

func boolMapAnyKey(values map[string]any, keys ...string) (map[string]bool, bool) {
	raw, ok := anyKey(values, keys...)
	if !ok {
		return nil, false
	}
	nested, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]bool, len(nested))
	for key, rawValue := range nested {
		if value, ok := boolAnyValue(rawValue); ok {
			out[key] = value
		}
	}
	return out, true
}

func networkPermissionMapAnyKey(values map[string]any, keys ...string) (map[string]NetworkPermission, bool) {
	raw, ok := anyKey(values, keys...)
	if !ok {
		return nil, false
	}
	nested, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]NetworkPermission, len(nested))
	for key, rawValue := range nested {
		value, ok := rawValue.(string)
		if !ok {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(value)) {
		case string(NetworkAllow):
			out[key] = NetworkAllow
		case string(NetworkDeny):
			out[key] = NetworkDeny
		}
	}
	return out, true
}

func boolAnyValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}

func anyKey(values map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value, true
		}
	}
	return nil, false
}
