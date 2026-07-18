package config

import (
	featureflags "codex_go/features"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Values map[string]any
}

type ForcedLoginMethod string

const (
	ForcedLoginMethodAPI     ForcedLoginMethod = "api"
	ForcedLoginMethodChatGPT ForcedLoginMethod = "chatgpt"
)

type CurrentTimeSource string

const (
	CurrentTimeSourceSystem   CurrentTimeSource = "system"
	CurrentTimeSourceExternal CurrentTimeSource = "external"
)

type CurrentTimeReminderDeliveryMode string

const (
	CurrentTimeReminderAnyInference          CurrentTimeReminderDeliveryMode = "any_inference"
	CurrentTimeReminderAfterUserOrToolOutput CurrentTimeReminderDeliveryMode = "after_user_or_tool_output"
)

type CurrentTimeReminderConfig struct {
	Enabled                 bool
	ReminderIntervalSeconds uint64
	ClockSource             CurrentTimeSource
	DeliveryMode            CurrentTimeReminderDeliveryMode
	SleepTool               bool
}

const DefaultChatGPTBaseURL = "https://chatgpt.com/backend-api/"

type LoadOptions struct {
	Profile              string
	CWD                  string
	IncludeManagedConfig bool
	ManagedConfigPath    string
}

type EffectiveOptions struct {
	Profile              string
	CWD                  string
	RawOverrides         []string
	EnableFeatures       []string
	DisableFeatures      []string
	StrictConfig         bool
	IncludeManagedConfig bool
	ManagedConfigPath    string
}

func Load(codexHome string) (*Config, error) {
	return LoadWithOptions(codexHome, nil)
}

func LoadWithOptions(codexHome string, opts *LoadOptions) (*Config, error) {
	values, err := loadConfigFile(ConfigPath(codexHome))
	if err != nil {
		return nil, err
	}
	profile := ""
	cwd := ""
	if opts != nil {
		profile = strings.TrimSpace(opts.Profile)
		cwd = strings.TrimSpace(opts.CWD)
	}
	if cwd != "" && projectConfigEnabled(values, cwd) {
		for _, path := range ProjectConfigPaths(cwd) {
			projectValues, exists, err := loadConfigFileIfExists(path)
			if err != nil {
				return nil, err
			}
			if exists {
				resolveProjectRelativeConfigValues(projectValues, filepath.Dir(path))
				sanitizeProjectConfigValues(projectValues)
				mergeConfigMaps(values, projectValues)
			}
		}
	}
	if profile != "" {
		if err := applyProfileLayer(codexHome, values, profile); err != nil {
			return nil, err
		}
	}
	if opts != nil && opts.IncludeManagedConfig {
		managedValues, exists, err := loadConfigFileIfExists(managedConfigPath(codexHome, opts.ManagedConfigPath))
		if err != nil {
			return nil, err
		}
		if exists {
			mergeConfigMaps(values, managedValues)
		}
	}
	return &Config{Values: values}, nil
}

func LoadEffective(codexHome string, rawOverrides, enableFeatures, disableFeatures []string, cwd ...string) (*Config, error) {
	workingDir := ""
	if len(cwd) > 0 {
		workingDir = cwd[0]
	}
	return LoadEffectiveWithOptions(codexHome, &EffectiveOptions{
		RawOverrides:    rawOverrides,
		EnableFeatures:  enableFeatures,
		DisableFeatures: disableFeatures,
		CWD:             workingDir,
	})
}

func LoadEffectiveWithOptions(codexHome string, opts *EffectiveOptions) (*Config, error) {
	loadOpts := &LoadOptions{}
	if opts != nil {
		loadOpts.Profile = opts.Profile
		loadOpts.CWD = opts.CWD
		loadOpts.IncludeManagedConfig = opts.IncludeManagedConfig
		loadOpts.ManagedConfigPath = opts.ManagedConfigPath
	}
	cfg, err := LoadWithOptions(codexHome, loadOpts)
	if err != nil {
		return nil, err
	}
	rawOverrides := []string(nil)
	enableFeatures := []string(nil)
	disableFeatures := []string(nil)
	if opts != nil {
		rawOverrides = opts.RawOverrides
		enableFeatures = opts.EnableFeatures
		disableFeatures = opts.DisableFeatures
	}
	overrides, err := ParseOverrides(rawOverrides)
	if err != nil {
		return nil, err
	}
	for _, feature := range enableFeatures {
		overrides = append(overrides, Override{
			Path:  "features." + feature,
			Value: true,
		})
	}
	for _, feature := range disableFeatures {
		overrides = append(overrides, Override{
			Path:  "features." + feature,
			Value: false,
		})
	}
	ApplyOverrides(cfg.Values, overrides)
	if opts != nil && opts.StrictConfig {
		if err := validateKnownTopLevelConfigFields(cfg.Values); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

var knownTopLevelConfigFields = map[string]struct{}{
	"analytics":                            {},
	"agents":                               {},
	"apps":                                 {},
	"apps_mcp_product_sku":                 {},
	"approval_policy":                      {},
	"approvals_reviewer":                   {},
	"chatgpt_base_url":                     {},
	"cli_auth_credentials_store":           {},
	"compact_prompt":                       {},
	"desktop":                              {},
	"developer_instructions":               {},
	"experimental_realtime_ws_base_url":    {},
	"experimental_use_unified_exec_tool":   {},
	"features":                             {},
	"forced_chatgpt_workspace_id":          {},
	"forced_login_method":                  {},
	"hooks":                                {},
	"instructions":                         {},
	"mcp_servers":                          {},
	"model":                                {},
	"model_auto_compact_token_limit":       {},
	"model_auto_compact_token_limit_scope": {},
	"model_context_window":                 {},
	"model_instructions_file":              {},
	"model_provider":                       {},
	"model_providers":                      {},
	"model_reasoning_effort":               {},
	"model_reasoning_summary":              {},
	"model_verbosity":                      {},
	"notices":                              {},
	"notify":                               {},
	"openai_base_url":                      {},
	"otel":                                 {},
	"personality":                          {},
	"profile":                              {},
	"profiles":                             {},
	"responsesapi_client_metadata":         {},
	"review_model":                         {},
	"requirements":                         {},
	"sandbox_mode":                         {},
	"sandbox_workspace_write":              {},
	"service_tier":                         {},
	"shell_environment_policy":             {},
	"tools":                                {},
	"trusted_projects":                     {},
	"tui":                                  {},
	"tool_output_token_limit":              {},
	"web_search":                           {},
	"windows":                              {},
	"windows_sandbox":                      {},
}

func validateKnownTopLevelConfigFields(values map[string]any) error {
	if values == nil {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := knownTopLevelConfigFields[key]; !ok {
			return fmt.Errorf("unknown configuration field `%s`", key)
		}
	}
	if err := validateKnownFeatureFields(values["features"]); err != nil {
		return err
	}
	if err := validateKnownMCPServerFields(values["mcp_servers"]); err != nil {
		return err
	}
	if err := validateKnownAgentsFields(values["agents"]); err != nil {
		return err
	}
	return nil
}

func validateKnownAgentsFields(value any) error {
	agents, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	knownSettings := map[string]bool{
		"enabled": true, "max_concurrent_threads_per_session": true, "max_threads": true,
		"max_depth": true, "default_subagent_model": true,
		"default_subagent_reasoning_effort": true, "job_max_runtime_seconds": true,
		"interrupt_message": true,
	}
	knownRoleFields := map[string]bool{"description": true, "config_file": true, "nickname_candidates": true}
	for key, raw := range agents {
		if knownSettings[key] {
			continue
		}
		role, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("unknown configuration field `agents.%s`", key)
		}
		for field := range role {
			if !knownRoleFields[field] {
				return fmt.Errorf("unknown configuration field `agents.%s.%s`", key, field)
			}
		}
	}
	return nil
}

func validateKnownFeatureFields(value any) error {
	features, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for key := range features {
		if !knownStrictFeatureFields[key] {
			return fmt.Errorf("unknown configuration field `features.%s`", key)
		}
	}
	if tokenBudget, ok := features["token_budget"].(map[string]any); ok {
		known := map[string]bool{"enabled": true, "reminder_threshold_tokens": true, "reminder_message_template": true, "guidance_message": true, "auto_compact_fallback_prompt": true, "auto_compact_fallback_buffer_tokens": true}
		for key := range tokenBudget {
			if !known[key] {
				return fmt.Errorf("unknown configuration field `features.token_budget.%s`", key)
			}
		}
	}
	return nil
}

func validateKnownMCPServerFields(value any) error {
	servers, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for server, raw := range servers {
		fields, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for key := range fields {
			if !knownStrictMCPServerFields[key] {
				return fmt.Errorf("unknown configuration field `mcp_servers.%s.%s`", server, key)
			}
		}
	}
	return nil
}

var knownStrictMCPServerFields = map[string]bool{"command": true, "args": true, "env": true, "env_vars": true, "cwd": true, "url": true, "bearer_token_env_var": true, "http_headers": true, "env_http_headers": true, "oauth_client_id": true, "oauth_resource": true, "scopes": true, "enabled": true, "disabled_reason": true, "required": true, "environment_id": true}

var knownStrictFeatureFields = map[string]bool{
	"shell_tool": true, "secret_auth_storage": true, "unified_exec": true, "shell_zsh_fork": true, "unified_exec_zsh_fork": true, "shell_snapshot": true, "deferred_executor": true, "code_mode": true, "code_mode_host": true, "code_mode_only": true, "standalone_web_search": true, "runtime_metrics": true, "memories": true, "external_agent_memory_import": true, "local_thread_store_compression": true, "chronicle": true, "apply_patch_streaming_events": true, "exec_permission_approvals": true, "hooks": true, "request_permissions_tool": true, "use_legacy_landlock": true, "enable_request_compression": true, "network_proxy": true, "respect_system_proxy": true, "multi_agent": true, "multi_agent_v2": true, "enable_fanout": true, "apps": true, "enable_mcp_apps": true, "non_prefixed_mcp_tool_names": true, "tool_suggest": true, "plugins": true, "in_app_browser": true, "browser_use": true, "browser_use_full_cdp_access": true, "browser_use_external": true, "computer_use": true, "remote_plugin": true, "plugin_sharing": true, "image_generation": true, "imagegenext": true, "item_ids": true, "concurrent_reasoning_summaries": true, "skill_mcp_dependency_install": true, "skill_search": true, "mentions_v2": true, "default_mode_request_user_input": true, "terminal_visualization_instructions": true, "guardian_approval": true, "goals": true, "token_budget": true, "rollout_budget": true, "current_time_reminder": true, "tool_call_mcp_elicitation": true, "auth_elicitation": true, "personality": true, "artifact": true, "fast_mode": true, "realtime_conversation": true, "prevent_idle_sleep": true, "remote_compaction_v2": true, "use_agent_identity": true, "workspace_dependencies": true,
}

func ProjectConfigPath(cwd string) string {
	return filepath.Join(strings.TrimSpace(cwd), ".codex", "config.toml")
}

func ProjectConfigPaths(cwd string) []string {
	folders := ProjectDotCodexFolders(cwd)
	paths := make([]string, 0, len(folders))
	for _, folder := range folders {
		paths = append(paths, filepath.Join(folder, "config.toml"))
	}
	return paths
}

func ProjectDotCodexFolders(cwd string) []string {
	var folders []string
	root := activeProjectRoot(cwd)
	for _, dir := range projectAncestorDirsWithinRoot(cwd, root) {
		folder := filepath.Join(dir, ".codex")
		if dirExists(folder) {
			folders = append(folders, folder)
		}
	}
	for i, j := 0, len(folders)-1; i < j; i, j = i+1, j-1 {
		folders[i], folders[j] = folders[j], folders[i]
	}
	return folders
}

func ActiveProjectRoot(cwd string) string {
	return activeProjectRoot(cwd)
}

func ProjectHooksDotCodexFolder(cwd string, dotCodexFolder string) string {
	dotCodexFolder = strings.TrimSpace(dotCodexFolder)
	if dotCodexFolder == "" {
		return ""
	}
	checkoutRoot := activeProjectRoot(cwd)
	repoRoot := rootCheckoutForLinkedWorktree(checkoutRoot)
	if checkoutRoot == "" || repoRoot == "" || canonicalProjectPath(checkoutRoot) == canonicalProjectPath(repoRoot) {
		return dotCodexFolder
	}
	layerDir := filepath.Dir(dotCodexFolder)
	relative, err := filepath.Rel(checkoutRoot, layerDir)
	if err != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		return dotCodexFolder
	}
	if relative == "." {
		return filepath.Join(repoRoot, ".codex")
	}
	return filepath.Join(repoRoot, relative, ".codex")
}

func (c *Config) FeatureSettings() map[string]bool {
	settings, _ := c.FeatureSettingsWithLegacyUsages()
	return settings
}

func (c *Config) IncludeSkillInstructions() bool {
	if c == nil || c.Values == nil {
		return true
	}
	skills, ok := c.Values["skills"].(map[string]any)
	if !ok {
		return true
	}
	include, ok := skills["include_instructions"].(bool)
	if !ok {
		return true
	}
	return include
}

func (c *Config) SkillShadowSelectionEnabled() bool {
	if featureflags.Enabled(c.FeatureSettings(), "skill_search") {
		return true
	}
	if c == nil || c.Values == nil {
		return false
	}
	skills, ok := c.Values["skills"].(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := skills["shadow_selection_enabled"].(bool)
	return ok && enabled
}

func (c *Config) SkillSelectionEnabled() bool {
	if c == nil || c.Values == nil {
		return false
	}
	skills, ok := c.Values["skills"].(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := skills["selection_enabled"].(bool)
	return ok && enabled
}

func (c *Config) OrchestratorSkillsEnabled() bool {
	if c == nil || c.Values == nil {
		return true
	}
	orchestrator, ok := c.Values["orchestrator"].(map[string]any)
	if !ok {
		return true
	}
	skills, ok := orchestrator["skills"].(map[string]any)
	if !ok {
		return true
	}
	enabled, ok := skills["enabled"].(bool)
	if !ok {
		return true
	}
	return enabled
}

func (c *Config) ToolOutputTokenLimit() *int {
	if c == nil || c.Values == nil {
		return nil
	}
	value, ok := nonNegativeIntFromConfigValue(c.Values["tool_output_token_limit"])
	if !ok {
		return nil
	}
	return &value
}

func (c *Config) FeatureSettingsWithLegacyUsages() (map[string]bool, []featureflags.LegacyFeatureUsage) {
	if c == nil || c.Values == nil {
		return map[string]bool{}, nil
	}
	values := map[string]any{}
	if featuresValue, ok := c.Values["features"].(map[string]any); ok {
		for key, value := range featuresValue {
			values[key] = value
		}
	}
	if enabled, ok := c.Values["experimental_use_unified_exec_tool"].(bool); ok {
		values["experimental_use_unified_exec_tool"] = enabled
	}
	return featureflags.ResolveSettings(values)
}

func (c *Config) LegacyFeatureUsages() []featureflags.LegacyFeatureUsage {
	_, usages := c.FeatureSettingsWithLegacyUsages()
	return usages
}

func (c *Config) ForcedChatGPTWorkspaceIDs() []string {
	if c == nil {
		return nil
	}
	return stringListFromConfigValue(c.Values["forced_chatgpt_workspace_id"])
}

func (c *Config) ForcedLoginMethod() ForcedLoginMethod {
	if c == nil {
		return ""
	}
	switch strings.TrimSpace(strings.ToLower(stringFromConfigValue(c.Values["forced_login_method"]))) {
	case string(ForcedLoginMethodAPI):
		return ForcedLoginMethodAPI
	case string(ForcedLoginMethodChatGPT):
		return ForcedLoginMethodChatGPT
	default:
		return ""
	}
}

func (c *Config) ResponsesAPIClientMetadata() map[string]string {
	if c == nil {
		return nil
	}
	return stringMapFromConfigValue(c.Values["responsesapi_client_metadata"])
}

func (c *Config) CLIAuthCredentialsStoreMode() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(stringFromConfigValue(c.Values["cli_auth_credentials_store"]))
}

func (c *Config) SecretAuthStorageEnabled() bool {
	if c == nil {
		return false
	}
	value, ok := c.Values["features"].(map[string]any)
	if !ok {
		return runtime.GOOS == "windows"
	}
	enabled, ok := value["secret_auth_storage"].(bool)
	if ok {
		return enabled
	}
	return runtime.GOOS == "windows"
}

func (c *Config) RespectSystemProxyEnabled() bool {
	if c == nil {
		return false
	}
	value, ok := c.Values["features"].(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := value["respect_system_proxy"].(bool)
	return ok && enabled
}

func (c *Config) CurrentTimeReminder() *CurrentTimeReminderConfig {
	if c == nil {
		return nil
	}
	featuresValue, ok := c.Values["features"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := featuresValue["current_time_reminder"]
	if !ok {
		return nil
	}
	cfg := &CurrentTimeReminderConfig{
		ReminderIntervalSeconds: 1,
		ClockSource:             CurrentTimeSourceSystem,
		DeliveryMode:            CurrentTimeReminderAnyInference,
	}
	switch value := raw.(type) {
	case bool:
		cfg.Enabled = value
	case map[string]any:
		if enabled, ok := value["enabled"].(bool); ok {
			cfg.Enabled = enabled
		}
		if rawInterval, ok := value["reminder_interval_seconds"]; ok {
			cfg.ReminderIntervalSeconds = uint64FromConfigValue(rawInterval)
		}
		if source := currentTimeSourceFromConfig(value["clock_source"]); source != "" {
			cfg.ClockSource = source
		}
		if mode := currentTimeDeliveryModeFromConfig(value["delivery_mode"]); mode != "" {
			cfg.DeliveryMode = mode
		}
		if sleepTool, ok := value["sleep_tool"].(bool); ok {
			cfg.SleepTool = sleepTool
		}
	default:
		return nil
	}
	return cfg
}

func (c *Config) ChatGPTBaseURL() string {
	if c == nil {
		return DefaultChatGPTBaseURL
	}
	value := stringFromConfigValue(c.Values["chatgpt_base_url"])
	if value == "" {
		return DefaultChatGPTBaseURL
	}
	return value
}

func stringFromConfigValue(value any) string {
	if raw, ok := value.(string); ok {
		return strings.TrimSpace(raw)
	}
	return ""
}

func stringListFromConfigValue(value any) []string {
	switch v := value.(type) {
	case string:
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return []string{trimmed}
		}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if value, ok := item.(string); ok {
				value = strings.TrimSpace(value)
				if value != "" {
					out = append(out, value)
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	case []string:
		out := make([]string, 0, len(v))
		for _, value := range v {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func uint64FromConfigValue(value any) uint64 {
	switch v := value.(type) {
	case int:
		if v > 0 {
			return uint64(v)
		}
	case int64:
		if v > 0 {
			return uint64(v)
		}
	case uint64:
		return v
	case float64:
		if v > 0 {
			return uint64(v)
		}
	case string:
		if parsed, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
			return parsed
		}
	}
	return 0
}

func nonNegativeIntFromConfigValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, v >= 0
	case int64:
		if v < 0 || uint64(v) > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(v), true
	case uint64:
		if v > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(v), true
	case float64:
		if v < 0 || v != float64(int64(v)) || v > float64(^uint(0)>>1) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

func currentTimeSourceFromConfig(value any) CurrentTimeSource {
	switch strings.TrimSpace(strings.ToLower(stringFromConfigValue(value))) {
	case string(CurrentTimeSourceSystem):
		return CurrentTimeSourceSystem
	case string(CurrentTimeSourceExternal):
		return CurrentTimeSourceExternal
	default:
		return ""
	}
}

func currentTimeDeliveryModeFromConfig(value any) CurrentTimeReminderDeliveryMode {
	switch strings.TrimSpace(strings.ToLower(stringFromConfigValue(value))) {
	case string(CurrentTimeReminderAnyInference), "any-inference", "anyinference":
		return CurrentTimeReminderAnyInference
	case string(CurrentTimeReminderAfterUserOrToolOutput), "after-user-or-tool-output", "afteruserortooloutput":
		return CurrentTimeReminderAfterUserOrToolOutput
	default:
		return ""
	}
}

func loadConfigFile(path string) (map[string]any, error) {
	values, _, err := loadConfigFileIfExists(path)
	return values, err
}

func loadConfigFileIfExists(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var values map[string]any
	if err := toml.Unmarshal(data, &values); err != nil {
		return nil, false, err
	}
	if values == nil {
		values = map[string]any{}
	}
	resolveConfigFileRelativeValues(values, filepath.Dir(path))
	return values, true, nil
}

func managedConfigPath(codexHome string, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(codexHome, "managed_config.toml")
	}
	return filepath.Join(string(filepath.Separator), "etc", "codex", "managed_config.toml")
}

func projectConfigEnabled(userValues map[string]any, cwd string) bool {
	trustLevels := projectTrustLevels(userValues)
	if len(trustLevels) == 0 {
		return false
	}
	trustTarget := activeProjectTrustTarget(cwd)
	for _, dir := range projectAncestorDirsWithinRoot(cwd, trustTarget) {
		if level, ok := trustLevels[canonicalProjectPath(dir)]; ok {
			return strings.EqualFold(level, "trusted")
		}
	}
	return strings.EqualFold(trustLevels[canonicalProjectPath(trustTarget)], "trusted")
}

func projectTrustLevels(values map[string]any) map[string]string {
	projects, ok := values["projects"].(map[string]any)
	if !ok || len(projects) == 0 {
		return nil
	}
	out := map[string]string{}
	for rawPath, rawProject := range projects {
		project, ok := rawProject.(map[string]any)
		if !ok {
			continue
		}
		trustLevel, ok := project["trust_level"].(string)
		if !ok {
			continue
		}
		path := canonicalProjectPath(rawPath)
		if path != "" {
			out[path] = strings.TrimSpace(trustLevel)
		}
	}
	return out
}

func projectAncestorDirs(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	start, err := filepath.Abs(cwd)
	if err != nil {
		start = filepath.Clean(cwd)
	}
	if info, statErr := os.Stat(start); statErr == nil && !info.IsDir() {
		start = filepath.Dir(start)
	}
	var dirs []string
	for dir := start; dir != ""; {
		dirs = append(dirs, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dirs
}

func projectAncestorDirsWithinRoot(cwd string, root string) []string {
	dirs := projectAncestorDirs(cwd)
	root = canonicalProjectPath(root)
	if root == "" {
		return dirs
	}
	var out []string
	for _, dir := range dirs {
		out = append(out, dir)
		if canonicalProjectPath(dir) == root {
			break
		}
	}
	return out
}

func activeProjectTrustTarget(cwd string) string {
	if root := nearestGitRoot(cwd); root != "" {
		if checkout := rootCheckoutForLinkedWorktree(root); checkout != "" {
			return checkout
		}
		return root
	}
	return activeProjectRoot(cwd)
}

func activeProjectRoot(cwd string) string {
	dirs := projectAncestorDirs(cwd)
	for _, dir := range dirs {
		if projectRootMarkerExists(dir) {
			return dir
		}
	}
	if len(dirs) > 0 {
		return dirs[0]
	}
	return strings.TrimSpace(cwd)
}

func nearestGitRoot(cwd string) string {
	for _, dir := range projectAncestorDirs(cwd) {
		if pathExists(filepath.Join(dir, ".git")) {
			return dir
		}
	}
	return ""
}

func projectRootMarkerExists(dir string) bool {
	for _, marker := range []string{".git", ".hg", ".svn"} {
		if pathExists(filepath.Join(dir, marker)) {
			return true
		}
	}
	return false
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func rootCheckoutForLinkedWorktree(checkoutRoot string) string {
	checkoutRoot = strings.TrimSpace(checkoutRoot)
	if checkoutRoot == "" {
		return ""
	}
	gitFile := filepath.Join(checkoutRoot, ".git")
	info, err := os.Stat(gitFile)
	if err != nil || info.IsDir() {
		return checkoutRoot
	}
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return checkoutRoot
	}
	target := parseGitDirPointer(string(data))
	if target == "" {
		return checkoutRoot
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(checkoutRoot, target)
	}
	target = filepath.Clean(target)
	slashTarget := filepath.ToSlash(target)
	before, _, found := strings.Cut(slashTarget, "/.git/worktrees/")
	if found && before != "" {
		return filepath.Clean(filepath.FromSlash(before))
	}
	return checkoutRoot
}

func parseGitDirPointer(contents string) string {
	for _, line := range strings.Split(strings.ReplaceAll(contents, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "gitdir:") {
			return strings.TrimSpace(line[len("gitdir:"):])
		}
	}
	return ""
}

func canonicalProjectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func sanitizeProjectConfigValues(values map[string]any) {
	if values == nil {
		return
	}
	for _, key := range []string{
		"openai_base_url",
		"chatgpt_base_url",
		"apps_mcp_product_sku",
		"model_provider",
		"model_providers",
		"notify",
		"profile",
		"profiles",
		"experimental_realtime_ws_base_url",
		"otel",
	} {
		delete(values, key)
	}
	if features, ok := values["features"].(map[string]any); ok {
		delete(features, "respect_system_proxy")
		if len(features) == 0 {
			delete(values, "features")
		}
	}
}

func resolveProjectRelativeConfigValues(values map[string]any, dotCodexDir string) {
	if values == nil || strings.TrimSpace(dotCodexDir) == "" {
		return
	}
	for _, key := range []string{"model_instructions_file"} {
		value, ok := values[key].(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || filepath.IsAbs(value) {
			continue
		}
		values[key] = filepath.Join(dotCodexDir, value)
	}
}

func resolveConfigFileRelativeValues(values map[string]any, baseDir string) {
	if values == nil || strings.TrimSpace(baseDir) == "" {
		return
	}
	resolveModelProviderAuthCWDValues(values, baseDir)
	profiles, ok := values["profiles"].(map[string]any)
	if !ok {
		return
	}
	for _, rawProfile := range profiles {
		profileValues, ok := rawProfile.(map[string]any)
		if ok {
			resolveModelProviderAuthCWDValues(profileValues, baseDir)
		}
	}
}

func resolveModelProviderAuthCWDValues(values map[string]any, baseDir string) {
	rawProviders, ok := values["model_providers"].(map[string]any)
	if !ok {
		return
	}
	for _, rawProvider := range rawProviders {
		providerValues, ok := rawProvider.(map[string]any)
		if !ok {
			continue
		}
		authValues, ok := providerValues["auth"].(map[string]any)
		if !ok {
			continue
		}
		cwd, ok := authValues["cwd"].(string)
		if !ok || strings.TrimSpace(cwd) == "" {
			authValues["cwd"] = filepath.Clean(baseDir)
			continue
		}
		authValues["cwd"] = resolveConfigFileRelativePath(cwd, baseDir)
	}
}

func resolveConfigFileRelativePath(value string, baseDir string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return filepath.Clean(baseDir)
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}

func applyProfileLayer(codexHome string, values map[string]any, profile string) error {
	profilePath, err := ResolveProfileConfigPath(codexHome, profile)
	if err != nil {
		return err
	}
	profileValues, exists, err := loadConfigFileIfExists(profilePath)
	if err != nil {
		return err
	}
	if exists {
		mergeConfigMaps(values, profileValues)
		return nil
	}
	if legacyProfile, ok, err := legacyProfileValues(values, profile); ok || err != nil {
		if err != nil {
			return err
		}
		mergeConfigMaps(values, legacyProfile)
		return nil
	}
	return fmt.Errorf("profile %q not found", profile)
}

func legacyProfileValues(values map[string]any, profile string) (map[string]any, bool, error) {
	profiles, ok := values["profiles"].(map[string]any)
	if !ok {
		return nil, false, nil
	}
	raw, ok := profiles[profile]
	if !ok {
		return nil, false, nil
	}
	profileValues, ok := raw.(map[string]any)
	if !ok {
		return nil, true, fmt.Errorf("profile %q is not a table", profile)
	}
	return profileValues, true, nil
}

func mergeConfigMaps(dst map[string]any, src map[string]any) {
	for key, value := range src {
		srcMap, srcIsMap := value.(map[string]any)
		dstMap, dstIsMap := dst[key].(map[string]any)
		if srcIsMap && dstIsMap {
			mergeConfigMaps(dstMap, srcMap)
			continue
		}
		dst[key] = cloneConfigValue(value)
	}
}

func cloneConfigValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, nested := range v {
			out[key] = cloneConfigValue(nested)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneConfigValue(v[i])
		}
		return out
	default:
		return value
	}
}

func stringMapFromConfigValue(value any) map[string]string {
	values, ok := value.(map[string]any)
	if !ok || len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, raw := range values {
		text, ok := raw.(string)
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(text) == "" {
			continue
		}
		out[key] = text
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func splitDottedPath(path string) []string {
	if path == "" {
		return nil
	}
	parts := splitDottedPathRespectingQuotes(path)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = unquoteDottedPathPart(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func unquoteDottedPathPart(part string) string {
	if len(part) >= 2 && part[0] == '"' && part[len(part)-1] == '"' {
		if value, err := strconv.Unquote(part); err == nil {
			return value
		}
	}
	return strings.Trim(part, "\"'")
}

func splitDottedPathRespectingQuotes(path string) []string {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, ch := range path {
		switch {
		case escaped:
			current.WriteRune(ch)
			escaped = false
		case ch == '\\' && quote != 0:
			current.WriteRune(ch)
			escaped = true
		case quote != 0:
			current.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'':
			quote = ch
			current.WriteRune(ch)
		case ch == '.':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}
	parts = append(parts, current.String())
	return parts
}
