package model

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"codex_go/features"
)

// BaseInstructions is Rust models-manager/prompt.md (BASE_INSTRUCTIONS): the
// fallback base instructions used when a model catalog provides no
// `model_messages.instructions_template` (Rust model_info_from_slug /
// BundledModelsResponse).
//
//go:embed prompt.md
var BaseInstructions string

const (
	TruncationModeBytes  = "bytes"
	TruncationModeTokens = "tokens"

	// ModelSpecialtyCyber identifies backend model-catalog specialties for
	// cybersecurity-focused models (Rust MODEL_SPECIALTY_CYBER, f141dc77f0).
	ModelSpecialtyCyber = "cyber"

	ServiceTierDefaultRequestValue = "default"

	ToolModeDirect       = "direct"
	ToolModeCodeMode     = "code_mode"
	ToolModeCodeModeOnly = "code_mode_only"

	VisibilityNone    = "none"
	VisibilityHide    = "hide"
	VisibilityList    = "list"
	VisibilityVisible = "visible"
)

const personalityPlaceholder = "{{ personality }}"

// personalitySectionHeader is the "# Personality" H1 heading whose section is
// removed when the `personality = "none"` opt-out is active (Rust #44946).
const personalitySectionHeader = "# Personality"

// stripPersonalitySection removes the "# Personality" section, from its heading
// up to the next H1 heading (or the end of the instructions).
func stripPersonalitySection(instructions string) string {
	sectionStart := -1
	sectionEnd := -1
	offset := 0
	for _, lineWithEnding := range splitInclusiveNewlines(instructions) {
		line := strings.TrimSuffix(lineWithEnding, "\n")
		line = strings.TrimSuffix(line, "\r")
		if sectionStart >= 0 {
			if isH1Heading(line) {
				sectionEnd = offset
				break
			}
		} else if line == personalitySectionHeader {
			sectionStart = offset
		}
		offset += len(lineWithEnding)
	}
	if sectionStart < 0 {
		return instructions
	}
	if sectionEnd < 0 {
		sectionEnd = len(instructions)
	}
	return instructions[:sectionStart] + instructions[sectionEnd:]
}

// splitInclusiveNewlines splits text like Rust's str::split_inclusive('\n').
func splitInclusiveNewlines(text string) []string {
	if text == "" {
		return nil
	}
	out := make([]string, 0, strings.Count(text, "\n")+1)
	for len(text) > 0 {
		if index := strings.IndexByte(text, '\n'); index >= 0 {
			out = append(out, text[:index+1])
			text = text[index+1:]
			continue
		}
		out = append(out, text)
		break
	}
	return out
}

func isH1Heading(line string) bool {
	rest, ok := strings.CutPrefix(line, "#")
	if !ok {
		return false
	}
	return rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")
}

type ModelMessages struct {
	InstructionsTemplate string                     `json:"instructions_template,omitempty"`
	PersonalityDefault   string                     `json:"-"`
	PersonalityFriendly  string                     `json:"-"`
	PersonalityPragmatic string                     `json:"-"`
	CollaborationModes   *CollaborationModeMessages `json:"collaboration_modes,omitempty"`
	MultiAgent           *MultiAgentMessages        `json:"multi_agent,omitempty"`
	TokenBudget          *ModelTokenBudgetConfig    `json:"token_budget,omitempty"`
	AutoReview           *AutoReviewMessages        `json:"auto_review,omitempty"`
	// Approvals is the catalog's `approvals` object (Rust
	// ModelMessages::approvals); missing or null uses the built-in texts.
	Approvals *ApprovalMessages `json:"approvals,omitempty"`
	// PersistentInstructions is the catalog's fixed persistent-mode developer
	// guidance (Rust ModelMessages::persistent_instructions); missing or null
	// uses the bundled default and an explicit empty string disables it.
	PersistentInstructions *string               `json:"persistent_instructions,omitempty"`
	Permissions            *PermissionMessages   `json:"permissions,omitempty"`
	ConfirmationPolicies   *ConfirmationPolicies `json:"confirmation_policies,omitempty"`
	// GuardianV2 carries the catalog's Guardian v2 model defaults (Rust
	// ModelMessages::guardian_v2). Go consumes max_tool_call_lag; the remaining
	// classifier fields belong to Rust's async scorer, which Go does not have.
	GuardianV2 *GuardianV2ModelConfig `json:"guardian_v2,omitempty"`
	Tools      *ToolMessages          `json:"tools,omitempty"`
}

type CollaborationModeMessages struct {
	Default *string `json:"default"`
	Plan    *string `json:"plan"`
}

// ApprovalMessages mirrors Rust protocol::openai_models::ApprovalMessages:
// catalog-provided approval-policy texts that replace the built-in ones.
type ApprovalMessages struct {
	OnRequest           *string `json:"on_request,omitempty"`
	OnRequestAutoReview *string `json:"on_request_auto_review,omitempty"`
	Never               *string `json:"never,omitempty"`
	UnlessTrusted       *string `json:"unless_trusted,omitempty"`
}

// PermissionMessages mirrors Rust protocol::openai_models::PermissionMessages:
// catalog-provided sandbox-mode texts that replace the built-in templates.
type PermissionMessages struct {
	DangerFullAccess *string `json:"danger_full_access,omitempty"`
	WorkspaceWrite   *string `json:"workspace_write,omitempty"`
	ReadOnly         *string `json:"read_only,omitempty"`
}

// GuardianV2ModelConfig mirrors Rust
// protocol::openai_models::GuardianV2ModelConfig: the model catalog's Guardian
// v2 defaults, overlaid by `[features.guardianv2]` configuration.
type GuardianV2ModelConfig struct {
	ClassifierInstructions         *string         `json:"classifier_instructions,omitempty"`
	ReviewThresholdBasisPoints     *int            `json:"review_threshold_basis_points,omitempty"`
	MaxToolCallLag                 *int            `json:"max_tool_call_lag,omitempty"`
	ReasoningEffort                *string         `json:"reasoning_effort,omitempty"`
	MaxActionTokens                *int            `json:"max_action_tokens,omitempty"`
	MaxClassifierInstructionTokens *int            `json:"max_classifier_instruction_tokens,omitempty"`
	Transcript                     json.RawMessage `json:"transcript,omitempty"`
}

// MultiAgentMessages mirrors Rust MultiAgentMessages: model-catalog messages
// for multi-agent roles and delegation modes (#38619).
type MultiAgentMessages struct {
	Role *MultiAgentRoleMessages `json:"role,omitempty"`
	Mode *MultiAgentModeMessages `json:"mode,omitempty"`
}

type MultiAgentRoleMessages struct {
	Root     *string `json:"root,omitempty"`
	Subagent *string `json:"subagent,omitempty"`
}

type MultiAgentModeMessages struct {
	Explicit  *string `json:"explicit,omitempty"`
	Proactive *string `json:"proactive,omitempty"`
	HintText  *string `json:"hint_text,omitempty"`
}

// ModelTokenBudgetConfig contains model-owned defaults for the context-window
// token-budget feature.
type ModelTokenBudgetConfig struct {
	ReminderThresholdTokens         int    `json:"reminder_threshold_tokens"`
	ReminderMessageTemplate         string `json:"reminder_message_template"`
	GuidanceMessage                 string `json:"guidance_message"`
	AutoCompactFallbackPrompt       string `json:"auto_compact_fallback_prompt"`
	AutoCompactFallbackBufferTokens int    `json:"auto_compact_fallback_buffer_tokens"`
}

// AutoReviewMessages mirrors Rust AutoReviewMessages (#39741): catalog-provided
// auto-review policy and outcome instructions. Pointer fields preserve explicit
// empty-string overrides.
type AutoReviewMessages struct {
	Policy         *string `json:"policy,omitempty"`
	PolicyTemplate *string `json:"policy_template,omitempty"`
	// NodeReplPolicy is the extra developer policy for `node_repl` and
	// `cua_repl` reviews (Rust AutoReviewMessages::node_repl_policy).
	NodeReplPolicy        *string `json:"node_repl_policy,omitempty"`
	RejectionInstructions *string `json:"rejection_instructions,omitempty"`
	TimeoutInstructions   *string `json:"timeout_instructions,omitempty"`
}

// ConfirmationPolicies mirrors Rust protocol::openai_models::ConfirmationPolicies
// (#41072): model-owned replacement Markdown for the Browser Use and native
// Computer Use confirmation-policy documents, forwarded unchanged to actor MCP
// tools. Pointer fields preserve explicit empty-string overrides.
type ConfirmationPolicies struct {
	BrowserUse  *string `json:"browser_use,omitempty"`
	ComputerUse *string `json:"computer_use,omitempty"`
}

// ToolMessages mirrors Rust protocol::openai_models::ToolMessages (#41461):
// model-owned descriptions for built-in tools.
type ToolMessages struct {
	SendUserMessageAsync *ToolMessage `json:"send_user_message_async,omitempty"`
	// MultiAgent carries the Multi-Agent V2 tools' catalog overrides (Rust
	// #46505).
	MultiAgent *MultiAgentToolMessages `json:"multi_agent,omitempty"`
}

// ToolMessage mirrors Rust protocol::openai_models::ToolMessage (#41461):
// a built-in tool's model-owned message metadata. A missing description uses the
// built-in description; an explicit empty string leaves the description empty.
type ToolMessage struct {
	Description *string `json:"description,omitempty"`
	// Parameters is a complete JSON Schema encoded as a string, consumed by the
	// Multi-Agent V2 tools only (Rust #46505). Missing, null, invalid or
	// unsupported structures, or a root without `type: "object"` retain the
	// harness parameters; overrides must declare harness-encrypted properties so
	// their annotations can be retained.
	Parameters *string `json:"parameters,omitempty"`
}

// MultiAgentToolMessages mirrors Rust protocol::openai_models::
// MultiAgentToolMessages: model-owned descriptions and parameters for the
// Multi-Agent V2 tools, independent of their namespace.
type MultiAgentToolMessages struct {
	SpawnAgent     *ToolMessage `json:"spawn_agent,omitempty"`
	SendMessage    *ToolMessage `json:"send_message,omitempty"`
	FollowupTask   *ToolMessage `json:"followup_task,omitempty"`
	WaitAgent      *ToolMessage `json:"wait_agent,omitempty"`
	InterruptAgent *ToolMessage `json:"interrupt_agent,omitempty"`
	ListAgents     *ToolMessage `json:"list_agents,omitempty"`
}

// MultiAgentToolMessage selects a V2 tool's catalog messages by tool name,
// independently of its runtime namespace (Rust ModelMessages::multi_agent_tool).
func (m *ModelMessages) MultiAgentToolMessage(toolName string) *ToolMessage {
	if m == nil || m.Tools == nil || m.Tools.MultiAgent == nil {
		return nil
	}
	tools := m.Tools.MultiAgent
	switch strings.TrimSpace(toolName) {
	case "spawn_agent":
		return tools.SpawnAgent
	case "send_message":
		return tools.SendMessage
	case "followup_task":
		return tools.FollowupTask
	case "wait_agent":
		return tools.WaitAgent
	case "interrupt_agent":
		return tools.InterruptAgent
	case "list_agents":
		return tools.ListAgents
	}
	return nil
}

// MultiAgentToolDescriptionOverride returns a V2 tool's catalog description
// override, or nil to keep the bundled description.
func (m *ModelMessages) MultiAgentToolDescriptionOverride(toolName string) *string {
	tool := m.MultiAgentToolMessage(toolName)
	if tool == nil {
		return nil
	}
	return tool.Description
}

// MultiAgentToolParametersOverride returns a V2 tool's complete parameter schema
// (JSON encoded), or nil to keep the harness parameters. Parsing belongs to the
// tool consumer.
func (m *ModelMessages) MultiAgentToolParametersOverride(toolName string) *string {
	tool := m.MultiAgentToolMessage(toolName)
	if tool == nil {
		return nil
	}
	return tool.Parameters
}

func (m *ModelMessages) UnmarshalJSON(data []byte) error {
	var raw struct {
		InstructionsTemplate   string                     `json:"instructions_template"`
		InstructionsVariables  map[string]string          `json:"instructions_variables"`
		CollaborationModes     *CollaborationModeMessages `json:"collaboration_modes"`
		MultiAgent             *MultiAgentMessages        `json:"multi_agent"`
		TokenBudget            *ModelTokenBudgetConfig    `json:"token_budget"`
		AutoReview             *AutoReviewMessages        `json:"auto_review"`
		Approvals              *ApprovalMessages          `json:"approvals"`
		PersistentInstructions *string                    `json:"persistent_instructions"`
		Permissions            *PermissionMessages        `json:"permissions"`
		ConfirmationPolicies   *ConfirmationPolicies      `json:"confirmation_policies"`
		GuardianV2             *GuardianV2ModelConfig     `json:"guardian_v2"`
		Tools                  *ToolMessages              `json:"tools"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.InstructionsTemplate = raw.InstructionsTemplate
	m.CollaborationModes = raw.CollaborationModes
	m.MultiAgent = raw.MultiAgent
	m.TokenBudget = raw.TokenBudget
	m.AutoReview = raw.AutoReview
	m.Approvals = raw.Approvals
	m.PersistentInstructions = raw.PersistentInstructions
	m.Permissions = raw.Permissions
	m.ConfirmationPolicies = raw.ConfirmationPolicies
	m.GuardianV2 = raw.GuardianV2
	m.Tools = raw.Tools
	if raw.InstructionsVariables != nil {
		m.PersonalityDefault = raw.InstructionsVariables["personality_default"]
		m.PersonalityFriendly = raw.InstructionsVariables["personality_friendly"]
		m.PersonalityPragmatic = raw.InstructionsVariables["personality_pragmatic"]
	}
	return nil
}

func (m *ModelMessages) SupportsPersonality() bool {
	if m == nil || !strings.Contains(m.InstructionsTemplate, personalityPlaceholder) {
		return false
	}
	return m.PersonalityFriendly != "" && m.PersonalityPragmatic != ""
}

func (m *ModelMessages) PersonalityMessage(personality string) (string, bool) {
	if m == nil {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(personality)) {
	case "none":
		return "", true
	case "friendly":
		if m.PersonalityFriendly != "" {
			return m.PersonalityFriendly, true
		}
	case "pragmatic":
		if m.PersonalityPragmatic != "" {
			return m.PersonalityPragmatic, true
		}
	case "":
		return m.PersonalityDefault, true
	default:
		return m.PersonalityDefault, true
	}
	return "", false
}

func (m *ModelInfo) SupportsPersonality() bool {
	// Rust #44946 retires Friendly/Pragmatic personality selection: generated
	// model presets always report `supports_personality = false`.
	return false
}

func (m *ModelInfo) PersonalityMessage(personality string) (string, bool) {
	if m == nil || m.ModelMessages == nil {
		return "", false
	}
	return m.ModelMessages.PersonalityMessage(personality)
}

func (m *ModelInfo) ModelInstructions(personality string) string {
	if m == nil {
		return BaseInstructions
	}
	if m.ModelMessages != nil && strings.TrimSpace(m.ModelMessages.InstructionsTemplate) != "" {
		// Rust #44946: the instruction template is literal text; legacy
		// personality variables are ignored, so a leftover `{{ personality }}`
		// placeholder is retained verbatim.
		return m.ModelMessages.InstructionsTemplate
	}
	return m.BaseInstructions
}

// resolvedContextWindow returns the model's effective context window, preferring
// the configured context_window over max_context_window (Rust
// ModelInfo::resolved_context_window).
func (m *ModelInfo) resolvedContextWindow() (int64, bool) {
	if m == nil {
		return 0, false
	}
	if m.ContextWindow > 0 {
		return m.ContextWindow, true
	}
	if m.MaxContextWindow > 0 {
		return m.MaxContextWindow, true
	}
	return 0, false
}

// usableContextWindow returns the context available to inference after reserving
// the model's configured headroom (Rust ModelInfo::usable_context_window,
// #41162). It distinguishes reserved-headroom capacity from the resolved context
// window and the auto-compaction limit.
func (m *ModelInfo) usableContextWindow() (int64, bool) {
	window, ok := m.resolvedContextWindow()
	if !ok {
		return 0, false
	}
	percent := m.EffectiveContextWindowPercent
	if percent <= 0 {
		percent = 95
	}
	return window * int64(percent) / 100, true
}

// UsableContextWindow is the exported form of usableContextWindow for callers
// outside the catalog (Rust ModelInfo::usable_context_window consumers such as
// the usage-tag diagnostics).
func (m *ModelInfo) UsableContextWindow() (int64, bool) {
	return m.usableContextWindow()
}

type TruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

// CyberAccessProgram is a caller-specific explicit access program advertised by
// model discovery (Rust core `turn_input::CyberAccessProgram`, snake_case on the
// catalog wire).
type CyberAccessProgram string

const (
	CyberAccessProgramStandard     CyberAccessProgram = "standard"
	CyberAccessProgramDaybreakBlue CyberAccessProgram = "daybreak_blue"
	CyberAccessProgramDaybreakRed  CyberAccessProgram = "daybreak_red"
)

// ModelAccessPrograms mirrors Rust `ModelAccessPrograms` (#44893): discovery
// metadata that advertises which explicit access programs a caller may select.
// A non-nil value with an empty Cyber list is distinct from missing metadata.
type ModelAccessPrograms struct {
	Cyber []CyberAccessProgram `json:"cyber"`
}

// UnmarshalJSON ignores unknown cyber program names so a newer server catalog
// does not prevent older clients from loading it (Rust #44893).
func (m *ModelAccessPrograms) UnmarshalJSON(data []byte) error {
	if m == nil {
		return nil
	}
	var raw struct {
		Cyber []string `json:"cyber"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	programs := make([]CyberAccessProgram, 0, len(raw.Cyber))
	for _, name := range raw.Cyber {
		program := CyberAccessProgram(name)
		switch program {
		case CyberAccessProgramStandard, CyberAccessProgramDaybreakBlue, CyberAccessProgramDaybreakRed:
			programs = append(programs, program)
		}
	}
	m.Cyber = programs
	return nil
}

type ModelInfo struct {
	Slug                     string   `json:"slug"`
	DisplayName              string   `json:"display_name"`
	Description              string   `json:"description"`
	DefaultReasoningLevel    string   `json:"default_reasoning_level"`
	SupportedReasoningLevels []string `json:"supported_reasoning_levels"`
	Visibility               string   `json:"visibility"`
	SupportedInAPI           bool     `json:"supported_in_api"`
	Priority                 int      `json:"priority"`
	AdditionalSpeedTiers     []string `json:"additional_speed_tiers"`
	ServiceTiers             []string `json:"service_tiers"`
	DefaultServiceTier       string   `json:"default_service_tier"`
	// AvailableAccessPrograms is nil when the catalog does not provide
	// access-program metadata (Rust #44893).
	AvailableAccessPrograms        *ModelAccessPrograms `json:"available_access_programs,omitempty"`
	BaseInstructions               string               `json:"base_instructions"`
	ModelMessages                  *ModelMessages       `json:"model_messages"`
	IncludeSkillsUsageInstructions bool                 `json:"include_skills_usage_instructions"`
	IncludePluginUsageInstructions bool                 `json:"include_plugin_usage_instructions"`
	IncludeAppsUsageInstructions   bool                 `json:"include_apps_usage_instructions"`
	ModelSpecialty                 string               `json:"model_specialty"`
	// SupportsReasoningSummaries mirrors Rust ModelInfo.supports_reasoning_summary_parameter
	// (serde default_true: absent means true). The legacy Go wire name
	// supports_reasoning_summaries is still accepted on parse.
	SupportsReasoningSummaries bool             `json:"supports_reasoning_summary_parameter"`
	DefaultReasoningSummary    string           `json:"default_reasoning_summary"`
	SupportVerbosity           bool             `json:"support_verbosity"`
	DefaultVerbosity           string           `json:"default_verbosity"`
	WebSearchToolType          string           `json:"web_search_tool_type"`
	TruncationPolicy           TruncationPolicy `json:"truncation_policy"`
	SupportsParallelToolCalls  bool             `json:"supports_parallel_tool_calls"`
	ToolMode                   string           `json:"tool_mode"`
	MultiAgentVersion          string           `json:"multi_agent_version"`
	// MultiAgentReasoningEffort mirrors Rust ModelInfo.multi_agent_reasoning_effort
	// (the reasoning effort used for multi-agent work when the user selects Ultra).
	MultiAgentReasoningEffort     *string  `json:"multi_agent_reasoning_effort,omitempty"`
	SupportsImageDetailOriginal   bool     `json:"supports_image_detail_original"`
	ContextWindow                 int64    `json:"context_window"`
	MaxContextWindow              int64    `json:"max_context_window"`
	AutoCompactTokenLimit         int64    `json:"auto_compact_token_limit"`
	EffectiveContextWindowPercent int      `json:"effective_context_window_percent"`
	InputModalities               []string `json:"input_modalities"`
	UsedFallbackModelMetadata     bool     `json:"-"`
	SupportsSearchTool            bool     `json:"supports_search_tool"`
	// SupportsExperimentalContext mirrors Rust ModelInfo.supports_experimental_context
	// (serde default false): whether experimental context management may be
	// activated at session startup for this model.
	SupportsExperimentalContext bool              `json:"supports_experimental_context"`
	UseResponsesLite            bool              `json:"use_responses_lite"`
	// SupportsReasoningEffortUpdates mirrors Rust
	// ModelInfo.supports_reasoning_effort_updates (serde default false): whether
	// the model accepts reasoning-effort `configuration_update` items. Missing
	// metadata keeps effort changes on the ordinary request parameter.
	SupportsReasoningEffortUpdates bool           `json:"supports_reasoning_effort_updates"`
	NodeReplAutoReviewRequired  bool              `json:"node_repl_auto_review_required"`
	NodeReplDisabled            bool              `json:"node_repl_disabled"`
	AutoReviewModelOverride     string            `json:"auto_review_model_override"`
	Upgrade                     *ModelInfoUpgrade `json:"upgrade"`
	// AvailabilityNux mirrors Rust ModelInfo.availability_nux (the NUX shown
	// when the model preset becomes accessible to the user).
	AvailabilityNux *ModelAvailabilityNux `json:"availability_nux"`
	// ShellType / ApplyPatchToolType / CompHash / ExperimentalSupportedTools
	// carry the Rust openai_models::ModelInfo wire fields so catalog metadata
	// round-trips identically (L0 model-metadata surface).
	ShellType                  string   `json:"shell_type"`
	ApplyPatchToolType         string   `json:"apply_patch_tool_type"`
	CompHash                   string   `json:"comp_hash"`
	ExperimentalSupportedTools []string `json:"experimental_supported_tools"`
	// Guardian carries the model-owned Guardian coverage policy, mirroring
	// Rust openai_models::ModelInfo.guardian. A nil policy preserves legacy
	// behavior; omitted scopes are disabled. The keys are computer_use, shell,
	// file_changes, mcp, network, and permissions.
	Guardian *GuardianModelPolicy `json:"guardian,omitempty"`
}

// GuardianReviewMode describes how Guardian handles an action when the user
// selects automatic approval, mirroring Rust openai_models::GuardianReviewMode.
type GuardianReviewMode string

const (
	GuardianReviewModeDisabled    GuardianReviewMode = "disabled"
	GuardianReviewModeSynchronous GuardianReviewMode = "synchronous"
	GuardianReviewModeAdaptive    GuardianReviewMode = "adaptive"
	GuardianReviewModeUnknown     GuardianReviewMode = "unknown"
)

// GuardianModelPolicy carries the model-owned Guardian coverage policy by
// scope. Omitted scopes are disabled; unknown modes retain synchronous review.
// Code Mode wrappers have no approval scope; their nested tools follow this
// policy (Rust #45915).
type GuardianModelPolicy struct {
	ComputerUse *GuardianReviewMode `json:"computer_use,omitempty"`
	Shell       *GuardianReviewMode `json:"shell,omitempty"`
	FileChanges *GuardianReviewMode `json:"file_changes,omitempty"`
	MCP         *GuardianReviewMode `json:"mcp,omitempty"`
	Network     *GuardianReviewMode `json:"network,omitempty"`
	Permissions *GuardianReviewMode `json:"permissions,omitempty"`
}

// GuardianScope identifies an approval category understood by the model-owned
// Guardian policy, mirroring Rust openai_models::GuardianScope.
type GuardianScope string

const (
	GuardianScopeComputerUse GuardianScope = "computer_use"
	GuardianScopeShell       GuardianScope = "shell"
	GuardianScopeFileChanges GuardianScope = "file_changes"
	GuardianScopeMCP         GuardianScope = "mcp"
	GuardianScopeNetwork     GuardianScope = "network"
	GuardianScopePermissions GuardianScope = "permissions"
)

// ReviewMode resolves the model-provided Guardian mode for a scope. An omitted
// scope is disabled, mirroring Rust GuardianModelPolicy::review_mode.
func (p *GuardianModelPolicy) ReviewMode(scope GuardianScope) GuardianReviewMode {
	if p == nil {
		return GuardianReviewModeDisabled
	}
	var mode *GuardianReviewMode
	switch scope {
	case GuardianScopeComputerUse:
		mode = p.ComputerUse
	case GuardianScopeShell:
		mode = p.Shell
	case GuardianScopeFileChanges:
		mode = p.FileChanges
	case GuardianScopeMCP:
		mode = p.MCP
	case GuardianScopeNetwork:
		mode = p.Network
	case GuardianScopePermissions:
		mode = p.Permissions
	}
	if mode == nil {
		return GuardianReviewModeDisabled
	}
	return *mode
}

// GuardianReviewMode returns the model-provided Guardian mode for a scope, or
// nil when the model carries no Guardian policy (legacy behavior).
func (m *ModelInfo) GuardianReviewMode(scope GuardianScope) *GuardianReviewMode {
	if m == nil || m.Guardian == nil {
		return nil
	}
	mode := m.Guardian.ReviewMode(scope)
	return &mode
}

// ComputerUseReviewRequired reports whether computer-use actions require
// Guardian review. A model-owned policy takes precedence over the legacy
// node_repl_auto_review_required metadata bit, mirroring Rust
// ModelInfo::computer_use_review_required.
func (m *ModelInfo) ComputerUseReviewRequired() bool {
	if mode := m.GuardianReviewMode(GuardianScopeComputerUse); mode != nil {
		return *mode != GuardianReviewModeDisabled
	}
	return m != nil && m.NodeReplAutoReviewRequired
}

// ModelInfoUpgrade carries the replacement model and informational retirement
// time advertised by a model-catalog upgrade entry.
type ModelInfoUpgrade struct {
	Model             string `json:"model"`
	MigrationMarkdown string `json:"migration_markdown"`
	RetirementAt      *int64 `json:"retirement_at,omitempty"`
}

func (u *ModelInfoUpgrade) UnmarshalJSON(data []byte) error {
	var raw struct {
		Model             string          `json:"model"`
		MigrationMarkdown string          `json:"migration_markdown"`
		RetirementAt      json.RawMessage `json:"retirement_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*u = ModelInfoUpgrade{
		Model:             raw.Model,
		MigrationMarkdown: raw.MigrationMarkdown,
		RetirementAt:      optionalRFC3339UnixSeconds(raw.RetirementAt),
	}
	return nil
}

func optionalRFC3339UnixSeconds(raw json.RawMessage) *int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	unix := parsed.Unix()
	return &unix
}

func (m *ModelInfo) UnmarshalJSON(data []byte) error {
	type rawTruncationPolicy struct {
		Mode  string `json:"mode"`
		Limit int64  `json:"limit"`
	}
	var raw struct {
		Slug                              string                `json:"slug"`
		DisplayName                       string                `json:"display_name"`
		Description                       any                   `json:"description"`
		DefaultReasoningLevel             any                   `json:"default_reasoning_level"`
		SupportedReasoningLevels          []json.RawMessage     `json:"supported_reasoning_levels"`
		Visibility                        string                `json:"visibility"`
		SupportedInAPI                    bool                  `json:"supported_in_api"`
		Priority                          int                   `json:"priority"`
		AdditionalSpeedTiers              []string              `json:"additional_speed_tiers"`
		ServiceTiers                      []json.RawMessage     `json:"service_tiers"`
		DefaultServiceTier                any                   `json:"default_service_tier"`
		AvailableAccessPrograms           *ModelAccessPrograms  `json:"available_access_programs"`
		BaseInstructions                  string                `json:"base_instructions"`
		ModelMessages                     *ModelMessages        `json:"model_messages"`
		IncludeSkillsUsageInstructions    bool                  `json:"include_skills_usage_instructions"`
		IncludePluginUsageInstructions    bool                  `json:"include_plugin_usage_instructions"`
		IncludeAppsUsageInstructions      *bool                 `json:"include_apps_usage_instructions"`
		ModelSpecialty                    any                   `json:"model_specialty"`
		SupportsReasoningSummaryParameter *bool                 `json:"supports_reasoning_summary_parameter"`
		SupportsReasoningSummariesLegacy  *bool                 `json:"supports_reasoning_summaries"`
		DefaultReasoningSummary           string                `json:"default_reasoning_summary"`
		SupportVerbosity                  bool                  `json:"support_verbosity"`
		DefaultVerbosity                  any                   `json:"default_verbosity"`
		WebSearchToolType                 string                `json:"web_search_tool_type"`
		TruncationPolicy                  rawTruncationPolicy   `json:"truncation_policy"`
		SupportsParallelToolCalls         bool                  `json:"supports_parallel_tool_calls"`
		ToolMode                          any                   `json:"tool_mode"`
		MultiAgentVersion                 any                   `json:"multi_agent_version"`
		SupportsImageDetailOriginal       bool                  `json:"supports_image_detail_original"`
		ContextWindow                     int64                 `json:"context_window"`
		MaxContextWindow                  int64                 `json:"max_context_window"`
		AutoCompactTokenLimit             int64                 `json:"auto_compact_token_limit"`
		EffectiveContextWindowPercent     int                   `json:"effective_context_window_percent"`
		InputModalities                   []string              `json:"input_modalities"`
		SupportsSearchTool                bool                  `json:"supports_search_tool"`
		SupportsExperimentalContext       bool                  `json:"supports_experimental_context"`
		UseResponsesLite                  bool                  `json:"use_responses_lite"`
		SupportsReasoningEffortUpdates    bool                  `json:"supports_reasoning_effort_updates"`
		NodeReplAutoReviewRequired        bool                  `json:"node_repl_auto_review_required"`
		NodeReplDisabled                  bool                  `json:"node_repl_disabled"`
		AutoReviewModelOverride           any                   `json:"auto_review_model_override"`
		Upgrade                           *ModelInfoUpgrade     `json:"upgrade"`
		AvailabilityNux                   *ModelAvailabilityNux `json:"availability_nux"`
		ShellType                         string                `json:"shell_type"`
		ApplyPatchToolType                string                `json:"apply_patch_tool_type"`
		CompHash                          string                `json:"comp_hash"`
		ExperimentalSupportedTools        []string              `json:"experimental_supported_tools"`
		Guardian                          *GuardianModelPolicy  `json:"guardian"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = ModelInfo{
		Slug:                           raw.Slug,
		DisplayName:                    raw.DisplayName,
		Description:                    stringFromJSONValue(raw.Description),
		DefaultReasoningLevel:          stringFromJSONValue(raw.DefaultReasoningLevel),
		SupportedReasoningLevels:       reasoningLevelsFromJSON(raw.SupportedReasoningLevels),
		Visibility:                     raw.Visibility,
		SupportedInAPI:                 raw.SupportedInAPI,
		Priority:                       raw.Priority,
		AdditionalSpeedTiers:           cloneStrings(raw.AdditionalSpeedTiers),
		ServiceTiers:                   serviceTierIDsFromJSON(raw.ServiceTiers),
		DefaultServiceTier:             stringFromJSONValue(raw.DefaultServiceTier),
		AvailableAccessPrograms:        raw.AvailableAccessPrograms,
		BaseInstructions:               raw.BaseInstructions,
		ModelMessages:                  raw.ModelMessages,
		IncludeSkillsUsageInstructions: raw.IncludeSkillsUsageInstructions,
		IncludePluginUsageInstructions: raw.IncludePluginUsageInstructions,
		IncludeAppsUsageInstructions:   defaultTrueBool(raw.IncludeAppsUsageInstructions),
		ModelSpecialty:                 stringFromJSONValue(raw.ModelSpecialty),
		SupportsReasoningSummaries:     reasoningSummariesSupport(raw.SupportsReasoningSummaryParameter, raw.SupportsReasoningSummariesLegacy),
		DefaultReasoningSummary:        raw.DefaultReasoningSummary,
		SupportVerbosity:               raw.SupportVerbosity,
		DefaultVerbosity:               stringFromJSONValue(raw.DefaultVerbosity),
		WebSearchToolType:              raw.WebSearchToolType,
		TruncationPolicy:               TruncationPolicy(raw.TruncationPolicy),
		SupportsParallelToolCalls:      raw.SupportsParallelToolCalls,
		ToolMode:                       knownToolMode(stringFromJSONValue(raw.ToolMode)),
		MultiAgentVersion:              knownMultiAgentVersion(stringFromJSONValue(raw.MultiAgentVersion)),
		SupportsImageDetailOriginal:    raw.SupportsImageDetailOriginal,
		ContextWindow:                  raw.ContextWindow,
		MaxContextWindow:               raw.MaxContextWindow,
		AutoCompactTokenLimit:          raw.AutoCompactTokenLimit,
		EffectiveContextWindowPercent:  raw.EffectiveContextWindowPercent,
		InputModalities:                cloneStrings(raw.InputModalities),
		SupportsSearchTool:             raw.SupportsSearchTool,
		SupportsExperimentalContext:    raw.SupportsExperimentalContext,
		UseResponsesLite:               raw.UseResponsesLite,
		SupportsReasoningEffortUpdates: raw.SupportsReasoningEffortUpdates,
		NodeReplAutoReviewRequired:     raw.NodeReplAutoReviewRequired,
		NodeReplDisabled:               raw.NodeReplDisabled,
		AutoReviewModelOverride:        stringFromJSONValue(raw.AutoReviewModelOverride),
		Upgrade:                        raw.Upgrade,
		AvailabilityNux:                raw.AvailabilityNux,
		ShellType:                      raw.ShellType,
		ApplyPatchToolType:             raw.ApplyPatchToolType,
		CompHash:                       raw.CompHash,
		ExperimentalSupportedTools:     cloneStrings(raw.ExperimentalSupportedTools),
		Guardian:                       raw.Guardian,
	}
	if m.BaseInstructions == "" {
		m.BaseInstructions = BaseInstructions
	}
	if m.EffectiveContextWindowPercent == 0 {
		m.EffectiveContextWindowPercent = 95
	}
	if len(m.InputModalities) == 0 {
		m.InputModalities = []string{"text", "image"}
	}
	return nil
}

type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

type ModelPreset struct {
	Model                    string
	Name                     string
	Description              string
	IsDefault                bool
	Priority                 int
	Visibility               string
	DefaultReasoningLevel    string
	SupportedReasoningLevels []string
	MultiAgentVersion        string
	ServiceTiers             []string
}

type ModelsManagerConfig struct {
	ModelContextWindow         int64
	ModelAutoCompactTokenLimit int64
	ToolOutputTokenLimit       int64
	// BaseInstructions mirrors Rust's `Option<String>`: a present-but-empty
	// override is distinct from an absent one (Rust's
	// `explicit_empty_base_instructions_stay_empty_with_personality_none`).
	BaseInstructions *string
	// Personality is the resolved personality selection (if any). Rust #44946
	// only uses it to honor an explicit `personality = "none"` opt-out by
	// stripping the baked personality section.
	Personality                     string
	ModelSupportsReasoningSummaries *bool
	ModelCatalog                    *ModelsResponse
}

type RefreshStrategy string

const (
	RefreshOnline           RefreshStrategy = "online"
	RefreshOffline          RefreshStrategy = "offline"
	RefreshOnlineIfUncached RefreshStrategy = "online_if_uncached"
)

type ModelsManager interface {
	ListModels(strategy RefreshStrategy) []ModelPreset
	RawModelCatalog(strategy RefreshStrategy) ModelsResponse
	GetRemoteModels() []ModelInfo
	GetDefaultModel(model string, allowProviderModelFallback bool, strategy RefreshStrategy) string
	GetModelInfo(model string, config *ModelsManagerConfig) ModelInfo
	RefreshIfNewETag(etag string)
	// RefreshAfterAuthChange is Rust ModelsManager::refresh_after_auth_change
	// (#46508): a best-effort catalog refresh when the in-memory catalog belongs
	// to different credentials. Static catalogs need no refresh.
	RefreshAfterAuthChange()
	// CatalogIdentity is the opaque provider/auth identity the running catalog
	// belongs to (Rust's endpoint client identity, #46508); an empty identity
	// cannot be compared to credentials.
	CatalogIdentity() string
}

type StaticModelsManager struct {
	remoteModels []ModelInfo
}

func NewStaticModelsManager(modelCatalog ModelsResponse) *StaticModelsManager {
	return &StaticModelsManager{remoteModels: cloneModelInfos(modelCatalog.Models)}
}

func (m *StaticModelsManager) ListModels(strategy RefreshStrategy) []ModelPreset {
	return BuildAvailableModels(m.RawModelCatalog(strategy).Models)
}

func (m *StaticModelsManager) RawModelCatalog(_ RefreshStrategy) ModelsResponse {
	return ModelsResponse{Models: m.GetRemoteModels()}
}

func (m *StaticModelsManager) GetRemoteModels() []ModelInfo {
	return cloneModelInfos(m.remoteModels)
}

func (m *StaticModelsManager) GetDefaultModel(model string, allowProviderModelFallback bool, strategy RefreshStrategy) string {
	availableModels := m.ListModels(strategy)
	if allowProviderModelFallback {
		if requestedModelIsAvailable(model, availableModels) {
			return model
		}
		return defaultModelFromAvailable(availableModels)
	}
	if model != "" {
		return model
	}
	return defaultModelFromAvailable(availableModels)
}

func (m *StaticModelsManager) GetModelInfo(model string, config *ModelsManagerConfig) ModelInfo {
	return ConstructModelInfoFromCandidates(model, m.GetRemoteModels(), config)
}

func (m *StaticModelsManager) RefreshIfNewETag(_ string) {}

// RefreshAfterAuthChange is a no-op: a static catalog belongs to no credentials.
func (m *StaticModelsManager) RefreshAfterAuthChange() {}

// CatalogIdentity is empty: a static catalog belongs to no credentials.
func (m *StaticModelsManager) CatalogIdentity() string { return "" }

func BundledModelsResponse() ModelsResponse {
	if catalog, err := loadBundledModelsResponse(); err == nil && len(catalog.Models) > 0 {
		return catalog
	}
	return fallbackBundledModelsResponse()
}

// LoadModelsResponseFromFile mirrors Rust's load_catalog_json for the
// model_catalog_json config: the file must parse as a ModelsResponse and
// contain at least one model.
func LoadModelsResponseFromFile(path string) (ModelsResponse, error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return ModelsResponse{}, err
	}
	var catalog ModelsResponse
	if err := json.Unmarshal(data, &catalog); err != nil {
		return ModelsResponse{}, fmt.Errorf("failed to parse model_catalog_json path %q as JSON: %w", strings.TrimSpace(path), err)
	}
	if len(catalog.Models) == 0 {
		return ModelsResponse{}, fmt.Errorf("model_catalog_json path %q must contain at least one model", strings.TrimSpace(path))
	}
	return catalog, nil
}

// ModelsCatalogFromConfigValues mirrors Rust's model_catalog_json config: an
// optional path to a JSON model catalog applied at config read time. Invalid
// or empty catalogs fall back to the bundled catalog (Rust surfaces a config
// load error instead; callers that prefer strictness can use
// LoadModelsResponseFromFile directly).
func ModelsCatalogFromConfigValues(values map[string]any) *ModelsResponse {
	if values == nil {
		return nil
	}
	raw, ok := values["model_catalog_json"]
	if !ok {
		return nil
	}
	path := strings.TrimSpace(fmt.Sprint(raw))
	if path == "" {
		return nil
	}
	catalog, err := LoadModelsResponseFromFile(path)
	if err != nil || len(catalog.Models) == 0 {
		return nil
	}
	return &catalog
}

func fallbackBundledModelsResponse() ModelsResponse {
	return ModelsResponse{
		Models: []ModelInfo{
			{
				Slug: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", Description: "Latest frontier agentic coding model.",
				Visibility: VisibilityList, SupportedInAPI: true, Priority: 1, BaseInstructions: BaseInstructions,
				ToolMode: ToolModeCodeModeOnly, MultiAgentVersion: "v2", DefaultReasoningLevel: "low",
				SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
				ServiceTiers:             []string{"priority"},
				SupportVerbosity:         true, DefaultVerbosity: "low", WebSearchToolType: "text_and_image",
				TruncationPolicy: TruncationPolicy{Mode: TruncationModeTokens, Limit: 10000}, SupportsImageDetailOriginal: true,
				UseResponsesLite: true, DefaultReasoningSummary: "none",
				ContextWindow: 272000, MaxContextWindow: 872000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug: "gpt-5.6-terra", DisplayName: "GPT-5.6-Terra", Description: "Balanced agentic coding model for everyday work.",
				Visibility: VisibilityList, SupportedInAPI: true, Priority: 2, BaseInstructions: BaseInstructions,
				ToolMode: ToolModeCodeModeOnly, MultiAgentVersion: "v2", DefaultReasoningLevel: "medium",
				SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
				ServiceTiers:             []string{"priority"},
				SupportVerbosity:         true, DefaultVerbosity: "low", WebSearchToolType: "text_and_image",
				TruncationPolicy: TruncationPolicy{Mode: TruncationModeTokens, Limit: 10000}, SupportsImageDetailOriginal: true,
				UseResponsesLite: true, DefaultReasoningSummary: "none",
				ContextWindow: 272000, MaxContextWindow: 872000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug: "gpt-5.6-luna", DisplayName: "GPT-5.6-Luna", Description: "Fast and affordable agentic coding model.",
				Visibility: VisibilityList, SupportedInAPI: true, Priority: 3, BaseInstructions: BaseInstructions,
				ToolMode: ToolModeCodeModeOnly, MultiAgentVersion: "v1", DefaultReasoningLevel: "medium",
				SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"},
				ServiceTiers:             []string{"priority"},
				SupportVerbosity:         true, DefaultVerbosity: "low", WebSearchToolType: "text_and_image",
				TruncationPolicy: TruncationPolicy{Mode: TruncationModeTokens, Limit: 10000}, SupportsImageDetailOriginal: true,
				UseResponsesLite: true, DefaultReasoningSummary: "none",
				ContextWindow: 272000, MaxContextWindow: 872000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug:                           "gpt-5.5",
				DisplayName:                    "GPT-5.5",
				Description:                    "Frontier model for complex coding, research, and real-world work.",
				Visibility:                     VisibilityList,
				SupportedInAPI:                 true,
				Priority:                       7,
				ServiceTiers:                   []string{"priority"},
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				DefaultReasoningLevel:          "medium",
				SupportedReasoningLevels:       []string{"low", "medium", "high", "xhigh"},
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  272000,
				MaxContextWindow:               272000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text", "image"},
				SupportsParallelToolCalls:      true,
			},
			{
				Slug: "gpt-5.2", DisplayName: "GPT-5.2", Description: "Optimized for professional work and long-running agents.",
				Visibility: VisibilityList, SupportedInAPI: true, Priority: 29, BaseInstructions: BaseInstructions,
				DefaultReasoningLevel: "medium", SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh"},
				ContextWindow: 272000, MaxContextWindow: 272000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug:                           "gpt-5.4",
				DisplayName:                    "GPT-5.4",
				Description:                    "Strong model for everyday coding.",
				Visibility:                     VisibilityHide,
				SupportedInAPI:                 true,
				Priority:                       16,
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				DefaultReasoningLevel:          "medium",
				SupportedReasoningLevels:       []string{"low", "medium", "high", "xhigh"},
				ServiceTiers:                   []string{"priority"},
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  272000,
				MaxContextWindow:               1000000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text", "image"},
				SupportsParallelToolCalls:      true,
			},
			{
				Slug:                           "gpt-5.4-mini",
				DisplayName:                    "GPT-5.4-Mini",
				Description:                    "Small, fast, and cost-efficient model for simpler coding tasks.",
				Visibility:                     VisibilityHide,
				SupportedInAPI:                 true,
				Priority:                       23,
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				DefaultReasoningLevel:          "medium",
				SupportedReasoningLevels:       []string{"low", "medium", "high", "xhigh"},
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  272000,
				MaxContextWindow:               272000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text", "image"},
				SupportsParallelToolCalls:      true,
			},
			{
				Slug:                          "codex-auto-review",
				DisplayName:                   "Codex Auto Review",
				Description:                   "Automatic approval review model for Codex.",
				Visibility:                    VisibilityHide,
				SupportedInAPI:                true,
				Priority:                      43,
				BaseInstructions:              BaseInstructions,
				DefaultReasoningLevel:         "medium",
				SupportedReasoningLevels:      []string{"low", "medium", "high", "xhigh"},
				TruncationPolicy:              TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                 272000,
				MaxContextWindow:              1000000,
				EffectiveContextWindowPercent: 95,
				InputModalities:               []string{"text", "image"},
				SupportsParallelToolCalls:     true,
			},
		},
	}
}

func AmazonBedrockModelCatalog() ModelsResponse {
	// Rust #42619: the Bedrock catalog preserves each bundled OpenAI model's
	// metadata while overriding the Bedrock slug, display name, priority, and
	// (for the GPT-5 generation) the context windows. GPT-5.6/Astra keep the
	// bundled long-context metadata.
	bundled := BundledModelsResponse()
	return normalizeBedrockCatalog(ModelsResponse{
		Models: []ModelInfo{
			bedrockModel(bundled, "gpt-5.6-sol", AmazonBedrockGPT56SolModelID, "GPT-5.6 Sol", 0),
			bedrockModel(bundled, "gpt-6-astra", AmazonBedrockGPT6AstraModelID, "GPT-6-Astra", 1),
			bedrockModel(bundled, "gpt-5.6-terra", AmazonBedrockGPT56TerraModelID, "GPT-5.6 Terra", 2),
			bedrockModel(bundled, "gpt-5.6-luna", AmazonBedrockGPT56LunaModelID, "GPT-5.6 Luna", 3),
			gpt5BedrockModel(bundled, "gpt-5.5", AmazonBedrockGPT55ModelID, "GPT-5.5", 4),
			gpt5BedrockModel(bundled, "gpt-5.4", AmazonBedrockGPT54ModelID, "GPT-5.4", 5),
		},
	})
}

// normalizeBedrockCatalog mirrors Rust normalize_bedrock_catalog: Amazon
// Bedrock only supports the implicit "default" tier, rejects the multimodal
// search content types, and does not support multi-agent V2 response items.
func normalizeBedrockCatalog(catalog ModelsResponse) ModelsResponse {
	for i := range catalog.Models {
		model := &catalog.Models[i]
		model.AdditionalSpeedTiers = nil
		model.ServiceTiers = nil
		model.DefaultServiceTier = ""
		model.WebSearchToolType = "text"
		model.MultiAgentVersion = "v1"
	}
	return catalog
}

// bedrockModel builds a GPT-5.6/Astra Bedrock entry from its bundled OpenAI
// model, keeping the bundled context windows and clearing tool-selection
// metadata Bedrock does not use (Rust bedrock_model).
func bedrockModel(bundled ModelsResponse, openAISlug string, bedrockSlug string, displayName string, priority int) ModelInfo {
	model := bundledModelBySlug(bundled, openAISlug)
	model.Slug = bedrockSlug
	model.DisplayName = displayName
	model.Priority = priority
	model.Visibility = VisibilityList
	model.AvailabilityNux = nil
	model.Upgrade = nil
	model.UseResponsesLite = false
	model.ToolMode = ""
	// Rust #39102: the GPT-5.6/Astra Bedrock variants allow context-window
	// overrides up to 872,000 tokens.
	model.MaxContextWindow = 872000
	model.SupportedReasoningLevels = withoutReasoningLevel(model.SupportedReasoningLevels, "ultra")
	return model
}

// gpt5BedrockModel builds a GPT-5/5.4 Bedrock entry from its bundled OpenAI
// model, pinning the shared 272,000-token Bedrock context window (Rust
// gpt_5_bedrock_model).
func gpt5BedrockModel(bundled ModelsResponse, openAISlug string, bedrockSlug string, displayName string, priority int) ModelInfo {
	model := bundledModelBySlug(bundled, openAISlug)
	model.Slug = bedrockSlug
	model.DisplayName = displayName
	model.Priority = priority
	model.ContextWindow = 272000
	model.MaxContextWindow = 272000
	model.Visibility = VisibilityList
	model.AvailabilityNux = nil
	model.Upgrade = nil
	return model
}

func bundledModelBySlug(bundled ModelsResponse, slug string) ModelInfo {
	for _, model := range bundled.Models {
		if model.Slug == slug {
			return cloneModelInfo(model)
		}
	}
	// The code fallback catalog may predate a bundled slug; keep the Bedrock
	// catalog usable with the shared Bedrock window.
	return ModelInfo{
		Slug:                          slug,
		DisplayName:                   slug,
		SupportedInAPI:                true,
		Visibility:                    VisibilityList,
		ContextWindow:                 272000,
		MaxContextWindow:              272000,
		EffectiveContextWindowPercent: 95,
		InputModalities:               []string{"text"},
	}
}

func withoutReasoningLevel(levels []string, level string) []string {
	if len(levels) == 0 {
		return levels
	}
	out := make([]string, 0, len(levels))
	for _, candidate := range levels {
		if strings.EqualFold(strings.TrimSpace(candidate), level) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func WithDefaultOnlyServiceTier(catalog ModelsResponse) ModelsResponse {
	models := cloneModelInfos(catalog.Models)
	for i := range models {
		models[i].AdditionalSpeedTiers = nil
		models[i].ServiceTiers = nil
		models[i].DefaultServiceTier = ""
	}
	return ModelsResponse{Models: models}
}

func BuildAvailableModels(remoteModels []ModelInfo) []ModelPreset {
	models := cloneModelInfos(remoteModels)
	sort.SliceStable(models, func(i, j int) bool {
		return models[i].Priority < models[j].Priority
	})
	presets := make([]ModelPreset, 0, len(models))
	for _, model := range models {
		if !modelVisibleInCatalog(model.Visibility) || !model.SupportedInAPI {
			continue
		}
		presets = append(presets, ModelPreset{
			Model:                    model.Slug,
			Name:                     model.DisplayName,
			Description:              model.Description,
			Priority:                 model.Priority,
			Visibility:               model.Visibility,
			DefaultReasoningLevel:    model.DefaultReasoningLevel,
			SupportedReasoningLevels: cloneStrings(model.SupportedReasoningLevels),
			MultiAgentVersion:        model.MultiAgentVersion,
			ServiceTiers:             cloneStrings(model.ServiceTiers),
		})
	}
	if len(presets) > 0 {
		markDefaultPresetByVisibility(presets)
	}
	return presets
}

func loadBundledModelsResponse() (ModelsResponse, error) {
	for _, path := range bundledModelCatalogPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var catalog ModelsResponse
		if err := json.Unmarshal(data, &catalog); err != nil {
			return ModelsResponse{}, err
		}
		if len(catalog.Models) > 0 {
			return catalog, nil
		}
	}
	return ModelsResponse{}, os.ErrNotExist
}

func bundledModelCatalogPaths() []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		clean := filepath.Clean(path)
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	add(os.Getenv("CODEX_GO_MODELS_JSON"))
	add(filepath.Join("models-manager", "models.json"))
	// Development checkout layout: keep the Go TUI in sync with the sibling
	// Rust catalog when both repositories are present.
	add(filepath.Join("..", "git", "codex", "codex-rs", "models-manager", "models.json"))
	add(filepath.Join("..", "codex-main", "codex-rs", "models-manager", "models.json"))
	if _, file, _, ok := runtime.Caller(0); ok {
		dir := filepath.Dir(file)
		for current := dir; current != ""; current = filepath.Dir(current) {
			add(filepath.Join(current, "models-manager", "models.json"))
			add(filepath.Join(current, "..", "codex-main", "codex-rs", "models-manager", "models.json"))
			next := filepath.Dir(current)
			if next == current {
				break
			}
		}
	}
	return out
}

func stringFromJSONValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

// defaultTrueBool returns the explicit value or true when absent, mirroring
// Rust's `#[serde(default = "default_true")]` for include_apps_usage_instructions.
func defaultTrueBool(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

// reasoningSummariesSupport resolves the model's reasoning-summary support,
// mirroring Rust ModelInfo.supports_reasoning_summary_parameter with
// #[serde(default = "default_true")]: the Rust wire name wins, the legacy Go
// name is a parse-time alias, and an absent field defaults to true.
func reasoningSummariesSupport(primary, legacy *bool) bool {
	if primary != nil {
		return *primary
	}
	if legacy != nil {
		return *legacy
	}
	return true
}

func knownMultiAgentVersion(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "disabled", "v1", "v2":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func knownToolMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ToolModeDirect, ToolModeCodeMode, ToolModeCodeModeOnly:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

// ResolveToolMode mirrors Rust's requested_tool_mode in
// codex-rs/core/src/tools/mod.rs: an explicit model tool_mode wins; an unset
// tool_mode falls back to the code_mode_only/code_mode feature flags, and
// defaults to direct mode when neither is enabled. Custom providers (for
// example DeepSeek) that reject the code-mode exec freeform tool therefore get
// a direct tool surface unless code mode is explicitly requested.
func ResolveToolMode(modelToolMode string, featureSettings map[string]bool) string {
	if mode := knownToolMode(modelToolMode); mode != "" {
		return mode
	}
	switch {
	case features.Enabled(featureSettings, "code_mode_only"):
		return ToolModeCodeModeOnly
	case features.Enabled(featureSettings, "code_mode"):
		return ToolModeCodeMode
	default:
		return ToolModeDirect
	}
}

func reasoningLevelsFromJSON(values []json.RawMessage) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		var level string
		if err := json.Unmarshal(value, &level); err == nil {
			level = strings.TrimSpace(level)
		}
		if level == "" {
			var object map[string]any
			if err := json.Unmarshal(value, &object); err == nil {
				level = stringFromJSONValue(object["effort"])
			}
		}
		if level != "" {
			out = append(out, level)
		}
	}
	return out
}

func serviceTierIDsFromJSON(values []json.RawMessage) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		var id string
		if err := json.Unmarshal(value, &id); err == nil {
			id = strings.TrimSpace(id)
		}
		if id == "" {
			var object map[string]any
			if err := json.Unmarshal(value, &object); err == nil {
				id = stringFromJSONValue(object["id"])
			}
		}
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func modelVisibleInCatalog(visibility string) bool {
	visibility = strings.TrimSpace(strings.ToLower(visibility))
	return visibility == VisibilityVisible || visibility == VisibilityList || visibility == VisibilityHide
}

func modelVisibleInPicker(visibility string) bool {
	visibility = strings.TrimSpace(strings.ToLower(visibility))
	return visibility == VisibilityVisible || visibility == VisibilityList
}

func markDefaultPresetByVisibility(presets []ModelPreset) {
	for i := range presets {
		presets[i].IsDefault = false
	}
	for i := range presets {
		if modelVisibleInPicker(presets[i].Visibility) {
			presets[i].IsDefault = true
			return
		}
	}
	if len(presets) > 0 {
		presets[0].IsDefault = true
	}
}

func modelHiddenFromPicker(visibility string) bool {
	return !modelVisibleInPicker(visibility)
}

func ModelInfoFromSlug(slug string) ModelInfo {
	return ModelInfo{
		Slug:        slug,
		DisplayName: slug,
		// Rust model_info_from_slug: unified exec, no skills usage
		// instructions, reasoning summaries supported by default.
		ShellType:                      "unified_exec",
		Visibility:                     VisibilityNone,
		SupportedInAPI:                 true,
		Priority:                       99,
		BaseInstructions:               BaseInstructions,
		ModelMessages:                  localModelMessages(),
		IncludeSkillsUsageInstructions: false,
		IncludeAppsUsageInstructions:   false,
		SupportsReasoningSummaries:     true,
		DefaultReasoningSummary:        "auto",
		WebSearchToolType:              "text",
		TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
		ContextWindow:                  272000,
		MaxContextWindow:               272000,
		EffectiveContextWindowPercent:  95,
		InputModalities:                []string{"text", "image"},
		UsedFallbackModelMetadata:      true,
	}
}

func SupportsServiceTier(info *ModelInfo, serviceTier string) bool {
	if info == nil {
		return false
	}
	serviceTier = strings.TrimSpace(serviceTier)
	if serviceTier == "" {
		return false
	}
	for _, tier := range info.ServiceTiers {
		if tier == serviceTier {
			return true
		}
	}
	return false
}

func ServiceTierForRequest(info *ModelInfo, serviceTier string) string {
	serviceTier = normalizeServiceTierRequestValue(serviceTier)
	if serviceTier == "" || serviceTier == ServiceTierDefaultRequestValue {
		return ""
	}
	if !SupportsServiceTier(info, serviceTier) {
		return ""
	}
	return serviceTier
}

func normalizeServiceTierRequestValue(serviceTier string) string {
	serviceTier = strings.TrimSpace(serviceTier)
	if serviceTier == "fast" {
		return "priority"
	}
	return serviceTier
}

func WithConfigOverrides(model ModelInfo, config *ModelsManagerConfig) ModelInfo {
	if config == nil {
		return model
	}
	if config.ModelSupportsReasoningSummaries != nil && *config.ModelSupportsReasoningSummaries {
		model.SupportsReasoningSummaries = true
	}
	if config.ModelContextWindow > 0 {
		if model.MaxContextWindow > 0 && config.ModelContextWindow > model.MaxContextWindow {
			model.ContextWindow = model.MaxContextWindow
		} else {
			model.ContextWindow = config.ModelContextWindow
		}
	}
	if config.ModelAutoCompactTokenLimit > 0 {
		model.AutoCompactTokenLimit = config.ModelAutoCompactTokenLimit
	}
	if config.ToolOutputTokenLimit > 0 {
		if model.TruncationPolicy.Mode == TruncationModeTokens {
			model.TruncationPolicy.Limit = config.ToolOutputTokenLimit
		} else {
			model.TruncationPolicy.Mode = TruncationModeBytes
			model.TruncationPolicy.Limit = approxBytesForTokens(config.ToolOutputTokenLimit)
		}
	}
	if config.BaseInstructions != nil {
		model.BaseInstructions = *config.BaseInstructions
		setInstructionsTemplate(&model, *config.BaseInstructions)
	} else if strings.TrimSpace(config.Personality) == "none" &&
		model.ModelMessages != nil && strings.TrimSpace(model.ModelMessages.InstructionsTemplate) != "" {
		// Rust #44946/#45809: an explicit `personality = "none"` opt-out strips
		// the baked personality section from the model's literal template, and
		// the behavior is no longer gated by the retired personality feature
		// flag.
		model.ModelMessages.InstructionsTemplate = stripPersonalitySection(model.ModelMessages.InstructionsTemplate)
	}
	return model
}

// setInstructionsTemplate mirrors Rust's model_messages.instructions_template
// override: the template becomes the sole instruction source and any
// personality variables are cleared while the remaining message fields
// (approvals, collaboration modes, token budget, ...) are preserved
// (Rust df72fdb415).
func setInstructionsTemplate(model *ModelInfo, template string) {
	if model == nil || model.ModelMessages == nil {
		if model == nil {
			return
		}
		model.ModelMessages = &ModelMessages{}
	}
	messages := model.ModelMessages
	messages.InstructionsTemplate = template
	messages.PersonalityDefault = ""
	messages.PersonalityFriendly = ""
	messages.PersonalityPragmatic = ""
	// Rust #38619: a base-instructions override replaces the message set, so
	// catalog-provided multi-agent role/mode messages are cleared too.
	messages.MultiAgent = nil
	// Rust #41072: the override also drops the catalog-provided confirmation-
	// policy documents (with_config_overrides sets confirmation_policies: None).
	messages.ConfirmationPolicies = nil
}

func ConstructModelInfoFromCandidates(model string, candidates []ModelInfo, config *ModelsManagerConfig) ModelInfo {
	remote, ok := findModelByLongestPrefix(model, candidates)
	if !ok {
		remote, ok = findModelByNamespacedSuffix(model, candidates)
	}
	var info ModelInfo
	if ok {
		info = remote
		info.Slug = model
		info.UsedFallbackModelMetadata = false
	} else {
		info = ModelInfoFromSlug(model)
	}
	return WithConfigOverrides(info, config)
}

func mergeModelInfos(base []ModelInfo, updates []ModelInfo) []ModelInfo {
	out := cloneModelInfos(base)
	for _, update := range updates {
		replaced := false
		for i := range out {
			if out[i].Slug == update.Slug {
				out[i] = cloneModelInfo(update)
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, cloneModelInfo(update))
		}
	}
	return out
}

func defaultModelFromAvailable(available []ModelPreset) string {
	for _, model := range available {
		if model.IsDefault {
			return model.Model
		}
	}
	if len(available) == 0 {
		return ""
	}
	return available[0].Model
}

func requestedModelIsAvailable(requestedModel string, availableModels []ModelPreset) bool {
	if requestedModel == "" {
		return false
	}
	for _, model := range availableModels {
		if model.Model == requestedModel {
			return true
		}
	}
	return false
}

func findModelByLongestPrefix(model string, candidates []ModelInfo) (ModelInfo, bool) {
	var best ModelInfo
	found := false
	for _, candidate := range candidates {
		if !strings.HasPrefix(model, candidate.Slug) {
			continue
		}
		if !found || len(candidate.Slug) > len(best.Slug) {
			best = candidate
			found = true
		}
	}
	return best, found
}

func findModelByNamespacedSuffix(model string, candidates []ModelInfo) (ModelInfo, bool) {
	namespace, suffix, ok := strings.Cut(model, "/")
	if !ok || namespace == "" || strings.Contains(suffix, "/") {
		return ModelInfo{}, false
	}
	for _, r := range namespace {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return ModelInfo{}, false
		}
	}
	return findModelByLongestPrefix(suffix, candidates)
}

// localModelMessages mirrors Rust #44946: fallback model metadata uses the
// standard literal prompt with no personality template.
func localModelMessages() *ModelMessages {
	return &ModelMessages{InstructionsTemplate: BaseInstructions}
}

func cloneModelInfos(in []ModelInfo) []ModelInfo {
	out := make([]ModelInfo, len(in))
	copy(out, in)
	for i := range out {
		out[i] = cloneModelInfo(out[i])
	}
	return out
}

func cloneModelInfo(in ModelInfo) ModelInfo {
	out := in
	out.SupportedReasoningLevels = cloneStrings(out.SupportedReasoningLevels)
	out.AdditionalSpeedTiers = cloneStrings(out.AdditionalSpeedTiers)
	out.ServiceTiers = cloneStrings(out.ServiceTiers)
	out.InputModalities = cloneStrings(out.InputModalities)
	if out.ModelMessages != nil {
		messages := *out.ModelMessages
		if messages.CollaborationModes != nil {
			collaborationModes := *messages.CollaborationModes
			collaborationModes.Default = cloneStringPointer(collaborationModes.Default)
			collaborationModes.Plan = cloneStringPointer(collaborationModes.Plan)
			messages.CollaborationModes = &collaborationModes
		}
		if messages.TokenBudget != nil {
			tokenBudget := *messages.TokenBudget
			messages.TokenBudget = &tokenBudget
		}
		if messages.AutoReview != nil {
			autoReview := *messages.AutoReview
			autoReview.Policy = cloneStringPointer(autoReview.Policy)
			autoReview.PolicyTemplate = cloneStringPointer(autoReview.PolicyTemplate)
			autoReview.NodeReplPolicy = cloneStringPointer(autoReview.NodeReplPolicy)
			autoReview.RejectionInstructions = cloneStringPointer(autoReview.RejectionInstructions)
			autoReview.TimeoutInstructions = cloneStringPointer(autoReview.TimeoutInstructions)
			messages.AutoReview = &autoReview
		}
		if messages.Tools != nil {
			tools := *messages.Tools
			tools.SendUserMessageAsync = cloneToolMessage(tools.SendUserMessageAsync)
			tools.MultiAgent = cloneMultiAgentToolMessages(tools.MultiAgent)
			messages.Tools = &tools
		}
		out.ModelMessages = &messages
	}
	out.Upgrade = cloneModelInfoUpgrade(out.Upgrade)
	return out
}

func cloneToolMessage(in *ToolMessage) *ToolMessage {
	if in == nil {
		return nil
	}
	out := *in
	out.Description = cloneStringPointer(in.Description)
	out.Parameters = cloneStringPointer(in.Parameters)
	return &out
}

func cloneMultiAgentToolMessages(in *MultiAgentToolMessages) *MultiAgentToolMessages {
	if in == nil {
		return nil
	}
	return &MultiAgentToolMessages{
		SpawnAgent:     cloneToolMessage(in.SpawnAgent),
		SendMessage:    cloneToolMessage(in.SendMessage),
		FollowupTask:   cloneToolMessage(in.FollowupTask),
		WaitAgent:      cloneToolMessage(in.WaitAgent),
		InterruptAgent: cloneToolMessage(in.InterruptAgent),
		ListAgents:     cloneToolMessage(in.ListAgents),
	}
}

func cloneModelInfoUpgrade(in *ModelInfoUpgrade) *ModelInfoUpgrade {
	if in == nil {
		return nil
	}
	out := *in
	if in.RetirementAt != nil {
		value := *in.RetirementAt
		out.RetirementAt = &value
	}
	return &out
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func approxBytesForTokens(tokens int64) int64 {
	if tokens <= 0 {
		return 0
	}
	return tokens * 4
}
