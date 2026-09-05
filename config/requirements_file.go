package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"codex_go/sandbox"
)

func LoadRequirementsFile(path string) (*ConfigRequirements, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return ParseRequirementsTOML(data)
}

func ParseRequirementsTOML(data []byte) (*ConfigRequirements, error) {
	values, err := parseRequirementsTOMLValues(data)
	if err != nil {
		return nil, err
	}
	return configRequirementsFromValidatedMap(values)
}

func parseRequirementsTOMLValues(data []byte) (map[string]any, error) {
	values := map[string]any{}
	if err := toml.Unmarshal(stripUTF8BOM(data), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func configRequirementsFromValidatedMap(values map[string]any) (*ConfigRequirements, error) {
	if raw, ok := values["experimental_network"]; ok {
		networkValues, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid type for experimental_network: expected table")
		}
		if err := validateNetworkRequirementsTOML(networkValues); err != nil {
			return nil, err
		}
	}
	return configRequirementsFromMap(values)
}

func browserUseRequirementsFromMap(values map[string]any) *BrowserUseRequirements {
	var out BrowserUseRequirements
	if value, ok := boolAnyKey(values, "allow_webmcp", "allowWebmcp"); ok {
		out.AllowWebmcp = &value
	}
	if value, ok := boolAnyKey(values, "allow_history_access", "allowHistoryAccess"); ok {
		out.AllowHistoryAccess = &value
	}
	if value, ok := boolAnyKey(values, "disable_auto_review", "disableAutoReview"); ok {
		out.DisableAutoReview = &value
	}
	if value, ok := boolAnyKey(values, "allow_global_persistent_approval", "allowGlobalPersistentApproval"); ok {
		out.AllowGlobalPersistentApproval = &value
	}
	if nested, ok := mapAnyKey(values, "default_origin_policy", "defaultOriginPolicy"); ok {
		out.DefaultOriginPolicy = browserUseOriginPolicyFromMap(nested)
	}
	if nested, ok := mapAnyKey(values, "origins"); ok {
		out.Origins = browserUseOriginPoliciesFromMap(nested)
	}
	return &out
}

func browserUseOriginPoliciesFromMap(values map[string]any) map[string]BrowserUseOriginPolicy {
	if values == nil {
		return nil
	}
	out := make(map[string]BrowserUseOriginPolicy, len(values))
	for name, nested := range values {
		if table, ok := nested.(map[string]any); ok {
			out[name] = *browserUseOriginPolicyFromMap(table)
		}
	}
	return out
}

func browserUseOriginPolicyFromMap(values map[string]any) *BrowserUseOriginPolicy {
	var out BrowserUseOriginPolicy
	if value, ok := allowDenyAnyKey(values, "access"); ok {
		out.Access = &value
	}
	if value, ok := allowDenyAnyKey(values, "downloads"); ok {
		out.Downloads = &value
	}
	if value, ok := allowDenyAnyKey(values, "uploads"); ok {
		out.Uploads = &value
	}
	if value, ok := allowDenyAnyKey(values, "full_cdp_access", "fullCdpAccess"); ok {
		out.FullCDPAccess = &value
	}
	if value, ok := allowDenyAnyKey(values, "auto_review", "autoReview"); ok {
		out.AutoReview = &value
	}
	if value, ok := boolAnyKey(values, "persistent_approval", "persistentApproval"); ok {
		out.PersistentApproval = &value
	}
	if value, ok := stringAnyKey(values, "access_approval_lifetime", "accessApprovalLifetime"); ok {
		lifetime := BrowserUseAccessApprovalLifetime(value)
		out.AccessApprovalLifetime = &lifetime
	}
	return &out
}

func allowDenyAnyKey(values map[string]any, keys ...string) (AllowDenyRequirement, bool) {
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			value, ok := raw.(string)
			if !ok {
				return "", false
			}
			return AllowDenyRequirement(value), true
		}
	}
	return "", false
}

func inAppBrowserRequirementsFromMap(values map[string]any) *InAppBrowserRequirements {
	var out InAppBrowserRequirements
	if value, ok := boolAnyKey(values, "allow_external_browser_settings_import", "allowExternalBrowserSettingsImport"); ok {
		out.AllowExternalBrowserSettingsImport = &value
	}
	return &out
}

func ConfigRequirementsFromMap(values map[string]any) *ConfigRequirements {
	requirements, _ := configRequirementsFromMap(values)
	return requirements
}

func configRequirementsFromMap(values map[string]any) (*ConfigRequirements, error) {
	if len(values) == 0 {
		return nil, nil
	}
	remoteConfigs, err := parseRemoteSandboxConfigs(values)
	if err != nil {
		return nil, err
	}
	return configRequirementsFromMapWithResolver(values, remoteConfigs, nil)
}

// configRequirementsFromMapWithResolver is the shared core of
// configRequirementsFromMap and the deterministic hostname-injectable variant
// used by the parity differential (Rust apply_remote_sandbox_config takes the
// hostname explicitly; a nil resolver falls back to os.Hostname()).
func configRequirementsFromMapWithResolver(values map[string]any, remoteConfigs []RemoteSandboxConfig, resolver HostnameResolver) (*ConfigRequirements, error) {
	if len(values) == 0 {
		return nil, nil
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
	applyRemoteSandboxConfig(&out, remoteConfigs, resolver)
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
	if value, ok := stringAnyKey(values, "additional_developer_instructions", "additionalDeveloperInstructions"); ok {
		out.AdditionalDeveloperInstructions = &value
	}
	if nested, ok := mapAnyKey(values, "permissions"); ok {
		out.Permissions = cloneMap(nested)
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
	if value, ok := boolAnyKey(values, "allow_browser_and_computer_use", "allowBrowserAndComputerUse"); ok {
		out.AllowBrowserAndComputerUse = &value
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
	// Rust 2994f545a7 (#37132): local requirements.toml allowlists for login
	// methods and ChatGPT workspaces; these fields are ignored when they
	// appear in cloud-provided requirements.
	if values, ok := stringListAnyKey(values, "allowed_login_methods", "allowedLoginMethods"); ok {
		out.AllowedLoginMethods = make([]ForcedLoginMethod, 0, len(values))
		for _, value := range values {
			method := ForcedLoginMethod(strings.ToLower(strings.TrimSpace(value)))
			if method == ForcedLoginMethodAPI || method == ForcedLoginMethodChatGPT {
				out.AllowedLoginMethods = append(out.AllowedLoginMethods, method)
			}
		}
	}
	if values, ok := stringListAnyKey(values, "allowed_chatgpt_workspaces", "allowedChatGPTWorkspaces"); ok {
		out.AllowedChatGPTWorkspaces = append([]string(nil), values...)
	}
	// Rust 0f21cb3413 (#39043): exact managed authentication backend settings.
	// These are local-only requirements and are dropped from cloud layers in
	// applyCloudConfigBundle.
	if value, ok := stringAnyKey(values, "cli_auth_credentials_store", "cliAuthCredentialsStore"); ok {
		mode := AuthCredentialsStoreMode(strings.ToLower(strings.TrimSpace(value)))
		switch mode {
		case AuthCredentialsStoreFile, AuthCredentialsStoreKeyring, AuthCredentialsStoreAuto, AuthCredentialsStoreEphemeral:
			out.CliAuthCredentialsStore = &mode
		default:
			return nil, fmt.Errorf("invalid cli_auth_credentials_store mode %q: expected file, keyring, auto, or ephemeral", value)
		}
	}
	if value, ok := stringAnyKey(values, "chatgpt_base_url", "chatgptBaseUrl"); ok && strings.TrimSpace(value) != "" {
		baseURL := strings.TrimSpace(value)
		out.ChatgptBaseURL = &baseURL
	}
	if nested, ok := mapAnyKey(values, "browser_use", "browserUse"); ok {
		out.BrowserUse = browserUseRequirementsFromMap(nested)
	}
	if nested, ok := mapAnyKey(values, "in_app_browser", "inAppBrowser"); ok {
		out.InAppBrowser = inAppBrowserRequirementsFromMap(nested)
	}
	if nested, ok := mapAnyKey(values, "auto_review", "autoReview"); ok {
		out.AutoReview = autoReviewRequirementsFromMap(nested)
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
	if nested, ok := mapAnyKey(values, "application"); ok {
		parsed, err := applicationRequirementsFromMap(nested)
		if err != nil {
			return nil, err
		}
		out.Application = parsed
	}
	if nested, ok := mapAnyKey(values, "models"); ok {
		out.Models = modelsRequirementsFromMap(nested)
	}
	if nested, ok := mapAnyKey(values, "mcp_servers", "mcpServers"); ok {
		parsed, err := mcpServerRequirementsFromMap(nested)
		if err != nil {
			return nil, err
		}
		out.MCPServers = parsed
	}
	if nested, ok := mapAnyKey(values, "plugins"); ok {
		parsed, err := pluginRequirementsFromMap(nested)
		if err != nil {
			return nil, err
		}
		out.Plugins = parsed
	}
	if configRequirementsEmpty(&out) {
		return nil, nil
	}
	return &out, nil
}

func autoReviewRequirementsFromMap(values map[string]any) *AutoReviewRequirements {
	if len(values) == 0 {
		return nil
	}
	out := AutoReviewRequirements{}
	if values, ok := stringListAnyKey(values, "required_on_models", "requiredOnModels"); ok {
		out.RequiredOnModels = append([]string(nil), values...)
	}
	if values, ok := stringListAnyKey(values, "ignore_rules", "ignoreRules"); ok {
		out.IgnoreRules = append([]string(nil), values...)
	}
	if len(out.RequiredOnModels) == 0 && len(out.IgnoreRules) == 0 {
		return nil
	}
	return &out
}

func mcpServerRequirementsFromMap(values map[string]any) (map[string]MCPServerRequirement, error) {
	out := make(map[string]MCPServerRequirement, len(values))
	for name, raw := range values {
		table, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid MCP requirement %q: expected table", name)
		}
		requirement, err := mcpServerRequirementFromMap(table)
		if err != nil {
			return nil, fmt.Errorf("invalid MCP requirement %q: %w", name, err)
		}
		out[name] = requirement
	}
	return out, nil
}

func pluginRequirementsFromMap(values map[string]any) (map[string]PluginRequirements, error) {
	out := make(map[string]PluginRequirements, len(values))
	for pluginID, raw := range values {
		table, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid plugin requirement %q: expected table", pluginID)
		}
		var requirement PluginRequirements
		if servers, configured := mapAnyKey(table, "mcp_servers", "mcpServers"); configured {
			parsed, err := mcpServerRequirementsFromMap(servers)
			if err != nil {
				return nil, fmt.Errorf("invalid plugin requirement %q: %w", pluginID, err)
			}
			requirement.MCPServers = &parsed
		}
		out[pluginID] = requirement
	}
	return out, nil
}

func mcpServerRequirementFromMap(values map[string]any) (MCPServerRequirement, error) {
	identity, ok := mapAnyKey(values, "identity")
	if !ok || len(values) != 1 {
		return MCPServerRequirement{}, fmt.Errorf("identity table is required")
	}
	if command, ok := stringAnyKey(identity, "command"); ok {
		requirement := MCPServerRequirement{Identity: &MCPServerIdentity{Command: &command}}
		return requirement, requirement.Validate()
	}
	if rawURL, ok := stringAnyKey(identity, "url"); ok {
		requirement := MCPServerRequirement{Identity: &MCPServerIdentity{URL: &rawURL}}
		return requirement, requirement.Validate()
	}
	if command, ok := mapAnyKey(identity, "command"); ok {
		executable, _ := stringAnyKey(command, "executable")
		args, err := mcpServerValueMatchersFromAny(command["args"])
		if err != nil {
			return MCPServerRequirement{}, err
		}
		requirement := MCPServerRequirement{Command: &MCPServerCommandMatcher{Executable: executable, Args: args}}
		return requirement, requirement.Validate()
	}
	if urlMatcher, ok := mapAnyKey(identity, "url"); ok {
		matcher, err := mcpServerValueMatcherFromMap(urlMatcher)
		if err != nil {
			return MCPServerRequirement{}, err
		}
		requirement := MCPServerRequirement{URL: &matcher}
		return requirement, requirement.Validate()
	}
	return MCPServerRequirement{}, fmt.Errorf("identity requires command or url")
}

func mcpServerValueMatchersFromAny(raw any) ([]MCPServerValueMatcher, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("command args must be an array")
	}
	out := make([]MCPServerValueMatcher, 0, len(items))
	for index, rawItem := range items {
		table, ok := rawItem.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("command arg matcher %d must be a table", index)
		}
		matcher, err := mcpServerValueMatcherFromMap(table)
		if err != nil {
			return nil, fmt.Errorf("command arg matcher %d: %w", index, err)
		}
		out = append(out, matcher)
	}
	return out, nil
}

func mcpServerValueMatcherFromMap(values map[string]any) (MCPServerValueMatcher, error) {
	kind, _ := stringAnyKey(values, "match")
	matcher := MCPServerValueMatcher{Match: kind}
	switch kind {
	case "exact", "prefix":
		matcher.Value, _ = stringAnyKey(values, "value")
		if len(values) != 2 {
			return MCPServerValueMatcher{}, fmt.Errorf("%s matcher requires only match and value", kind)
		}
	case "regex":
		matcher.Expression, _ = stringAnyKey(values, "expression")
		if len(values) != 2 {
			return MCPServerValueMatcher{}, fmt.Errorf("regex matcher requires only match and expression")
		}
	default:
		return MCPServerValueMatcher{}, fmt.Errorf("unsupported matcher %q", kind)
	}
	return matcher, matcher.Validate()
}

func computerUseRequirementsFromMap(values map[string]any) *ComputerUseRequirements {
	var out ComputerUseRequirements
	if value, ok := boolAnyKey(values, "allow_locked_computer_use", "allowLockedComputerUse"); ok {
		out.AllowLockedComputerUse = &value
	}
	if value, ok := boolAnyKey(values, "allow_persistent_approval", "allowPersistentApproval"); ok {
		out.AllowPersistentApproval = &value
	}
	if value, ok := allowDenyAnyKey(values, "default_app_access", "defaultAppAccess"); ok {
		out.DefaultAppAccess = &value
	}
	if nested, ok := mapAnyKey(values, "macos"); ok {
		out.Macos = computerUseMacosRequirementsFromMap(nested)
	}
	if nested, ok := mapAnyKey(values, "windows"); ok {
		out.Windows = computerUseWindowsRequirementsFromMap(nested)
	}
	return &out
}

func computerUseMacosRequirementsFromMap(values map[string]any) *ComputerUseMacosRequirements {
	var out ComputerUseMacosRequirements
	if nested, ok := mapAnyKey(values, "bundle_ids", "bundleIds"); ok {
		out.BundleIDs = allowDenyMapFromMap(nested)
	}
	return &out
}

func computerUseWindowsRequirementsFromMap(values map[string]any) *ComputerUseWindowsRequirements {
	var out ComputerUseWindowsRequirements
	if nested, ok := mapAnyKey(values, "aumids"); ok {
		out.Aumids = allowDenyMapFromMap(nested)
	}
	if raw, ok := values["exes"]; ok {
		items, ok := raw.([]any)
		if !ok {
			items = nil
		}
		out.Exes = computerUseWindowsExesFromList(items)
	}
	return &out
}

func computerUseWindowsExesFromList(values []any) []ComputerUseWindowsExeRequirement {
	out := make([]ComputerUseWindowsExeRequirement, 0, len(values))
	for _, raw := range values {
		table, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		var item ComputerUseWindowsExeRequirement
		item.PublisherName, _ = stringAnyKey(table, "publisher_name", "publisherName")
		item.ProductName, _ = stringAnyKey(table, "product_name", "productName")
		if value, ok := stringAnyKey(table, "binary_name", "binaryName"); ok {
			item.BinaryName = &value
		}
		if value, ok := allowDenyAnyKey(table, "access"); ok {
			item.Access = value
		}
		out = append(out, item)
	}
	return out
}

func allowDenyMapFromMap(values map[string]any) map[string]AllowDenyRequirement {
	if values == nil {
		return nil
	}
	out := make(map[string]AllowDenyRequirement, len(values))
	for key, raw := range values {
		if value, ok := raw.(string); ok {
			out[key] = AllowDenyRequirement(value)
		}
	}
	return out
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
	out.SessionEnd = hookGroupsAnyKey(values, "SessionEnd", "session_end", "sessionEnd")
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

func applicationRequirementsFromMap(values map[string]any) (*ApplicationRequirements, error) {
	if len(values) == 0 {
		return nil, nil
	}
	nested, ok := mapAnyKey(values, "network")
	if !ok {
		return nil, nil
	}
	network, err := applicationNetworkRequirementsFromMap(nested)
	if err != nil {
		return nil, err
	}
	return &ApplicationRequirements{Network: network}, nil
}

func applicationNetworkRequirementsFromMap(values map[string]any) (*ApplicationNetworkRequirements, error) {
	enabled := true
	if value, ok := boolAnyValue(values["enabled"]); ok {
		enabled = value
	}
	domains := map[string]NetworkPermission{}
	if raw, ok := values["domains"]; ok {
		nested, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("application.network.domains must be a table")
		}
		for domain, rawPermission := range nested {
			normalized, err := normalizeApplicationNetworkDomain(domain)
			if err != nil {
				return nil, err
			}
			permission, ok := networkPermissionFromAny(rawPermission)
			if !ok {
				return nil, fmt.Errorf("application.network.domains.%s must be \"allow\" or \"deny\"", domain)
			}
			if _, exists := domains[normalized]; exists {
				return nil, fmt.Errorf("duplicate application.network domain after normalization: %q", domain)
			}
			domains[normalized] = permission
		}
	}
	return &ApplicationNetworkRequirements{Enabled: enabled, Domains: domains}, nil
}

func normalizeApplicationNetworkDomain(domain string) (string, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimSuffix(domain, ".")
	if len(domain) > 253 {
		return "", fmt.Errorf("application.network.domains requires exact ASCII domain names without URLs, ports, or wildcards")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("application.network.domains requires exact ASCII domain names without URLs, ports, or wildcards")
		}
		for i, r := range label {
			valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
			if !valid || (i == 0 && r == '-') || (i == len(label)-1 && r == '-') {
				return "", fmt.Errorf("application.network.domains requires exact ASCII domain names without URLs, ports, or wildcards")
			}
		}
	}
	return domain, nil
}

func networkPermissionFromAny(value any) (NetworkPermission, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(NetworkAllow):
		return NetworkAllow, true
	case string(NetworkDeny):
		return NetworkDeny, true
	default:
		return "", false
	}
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
		if value, ok := stringAnyKey(handlerMap, "server"); ok {
			handler.Server = value
		}
		if value, ok := stringAnyKey(handlerMap, "tool"); ok {
			handler.Tool = value
		}
		if value, ok := mapAnyKey(handlerMap, "input"); ok {
			handler.Input = cloneMap(value)
		}
		if value, ok := uint64AnyKey(handlerMap, "timeout_sec", "timeoutSec", "timeout"); ok {
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
			value.AdditionalDeveloperInstructions == nil &&
			value.Permissions == nil &&
			value.AllowedWebSearchModes == nil &&
			value.AllowManagedHooksOnly == nil &&
			value.AllowBrowserAndComputerUse == nil &&
			value.AllowAppshots == nil &&
			value.AllowRemoteControl == nil &&
			value.ComputerUse == nil &&
			value.BrowserUse == nil &&
			value.InAppBrowser == nil &&
			value.AutoReview == nil &&
			value.FeatureRequirements == nil &&
			value.Hooks == nil &&
			value.EnforceResidency == nil &&
			value.Application == nil &&
			value.Network == nil &&
			value.Models == nil &&
			value.AllowedLoginMethods == nil &&
			value.AllowedChatGPTWorkspaces == nil &&
			value.CliAuthCredentialsStore == nil &&
			value.ChatgptBaseURL == nil &&
			value.MCPServers == nil &&
			value.Plugins == nil)
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
