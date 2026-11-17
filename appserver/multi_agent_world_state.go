package appserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/model"
	"codex_go/prompt"
	"codex_go/session"
	"codex_go/turn"
	"codex_go/utils"
)

// This file ports the multi-agent world-state sections of
// codex-rs/core/src/context/world_state/multi_agent_mode.rs,
// .../multi_agent_usage_hint.rs and core/src/session/multi_agents.rs.
//
// `multi_agent_usage_hint` persists Rust's `WorldStateHash` of the resolved
// `MultiAgentRoleInstructions` fragment, and `multi_agent_mode` persists
// `MultiAgentModeState` (`{mode, usage_hint_hash?}`). Configured role text is
// emitted verbatim; catalog/bundled text is composed with the shared runtime
// guidance and only catalog text carries `<multi_agent_role>` markers.

const (
	multiAgentModeSectionID        = "multi_agent_mode"
	multiAgentUsageHintSectionID   = "multi_agent_usage_hint"
	multiAgentModeOpenMarker       = "<multi_agent_mode>"
	multiAgentModeCloseMarker      = "</multi_agent_mode>"
	multiAgentRoleOpenMarker       = "<multi_agent_role>"
	multiAgentRoleCloseMarker      = "</multi_agent_role>"
	multiAgentRoleInstructionsKind = "multi_agent.role_instructions"
	multiAgentUsageHintKind        = "multi_agent.usage_hint"
	multiAgentModeInstructionsKind = "multi_agent.mode_instructions"
	multiAgentModeMaxTokens        = 400
	// hostedMultiAgentSkillGuidance is Rust's HOSTED_MULTI_AGENT_SKILL_GUIDANCE
	// (core/src/session/multi_agents.rs), used when native collaboration tools
	// are disabled.
	hostedMultiAgentSkillGuidance = "Before calling any multi-agent tool, read the full multi-agent skill using its listed package locator. Skip the read only if the full skill text is already in your current context. If the read fails, make note of the blocker."
)

// multiAgentRoleInstructions is Rust's MultiAgentRoleInstructions.
type multiAgentRoleInstructions struct {
	Text       string
	Marked     bool
	Configured bool
}

// render applies the fragment markers Rust's `ContextualUserFragment::render`
// adds. Rust concatenates markers without surrounding newlines and only marks
// catalog-owned text.
func (role multiAgentRoleInstructions) render() string {
	if !role.Marked {
		return role.Text
	}
	return multiAgentRoleOpenMarker + role.Text + multiAgentRoleCloseMarker
}

func (role multiAgentRoleInstructions) fingerprint() string {
	return session.TextWorldStateFragmentHash("developer", role.render())
}

// composeMultiAgentRoleBody mirrors the `Composed` branch of Rust's
// MultiAgentRoleInstructions::body. Rust's bundled role strings end with a
// newline; the Go constants omit it, so re-add it before the Rust separators.
func composeMultiAgentRoleBody(base string, omitUpdatePlan bool, maxConcurrency int, waitEnabled, exposeOverrides bool) string {
	if omitUpdatePlan {
		base = prompt.WithoutUpdatePlanInstructions(base)
	}
	var body strings.Builder
	body.WriteString(base)
	body.WriteString("\n")
	body.WriteString(agent.MultiAgentV2SharedUsageHint)
	body.WriteString("\n")
	body.WriteString("\n")
	if waitEnabled {
		body.WriteString(agent.MultiAgentV2WaitAgentUsageHint)
		body.WriteString("\n\n")
	}
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	fmt.Fprintf(&body, "There are %d available concurrency slots, meaning that up to %d agents can be active at once, including you.", maxConcurrency, maxConcurrency)
	if exposeOverrides {
		body.WriteString("\n\n")
		body.WriteString(agent.MultiAgentV2ModelOverrideUsageHint)
	}
	return body.String()
}

// rustBundledMultiAgentRole restores the trailing newline of Rust's raw-string
// bundled role constants.
func rustBundledMultiAgentRole(base string) string {
	if base == agent.MultiAgentV2RootUsageHint || base == agent.MultiAgentV2SubagentUsageHint {
		return base + "\n"
	}
	return base
}

// resolveMultiAgentRoleInstructions ports `resolve_usage_hints` for one role:
// configured text wins and is verbatim; otherwise catalog text (or the bundled
// default) is composed, and disabled native tools suppress the bundled hint.
func resolveMultiAgentRoleInstructions(configured *string, catalog *string, bundled string, v2 *config.MultiAgentV2Config, omitUpdatePlan bool) (multiAgentRoleInstructions, bool) {
	if configured != nil {
		if *configured == "" {
			return multiAgentRoleInstructions{}, false
		}
		return multiAgentRoleInstructions{Text: *configured, Configured: true}, true
	}
	base := ""
	marked := false
	if catalog != nil {
		base = *catalog
		marked = true
	} else {
		base = rustBundledMultiAgentRole(bundled)
	}
	if base == "" {
		return multiAgentRoleInstructions{}, false
	}
	if v2 != nil && v2.DisableNativeTools {
		return multiAgentRoleInstructions{}, false
	}
	maxConcurrency, waitEnabled, exposeOverrides := 1, true, false
	if v2 != nil {
		maxConcurrency = v2.MaxConcurrentThreadsPerSession
		waitEnabled = v2.WaitAgentEnabled
		exposeOverrides = v2.ExposeSpawnAgentModelOverrides
	}
	return multiAgentRoleInstructions{
		Text:   composeMultiAgentRoleBody(base, omitUpdatePlan, maxConcurrency, waitEnabled, exposeOverrides),
		Marked: marked,
	}, true
}

// multiAgentModeValue is the wire form of Rust's MultiAgentMode.
type multiAgentModeValue struct {
	Kind string // "none", "explicitRequestOnly", "proactive", "custom"
	Text string
}

func (mode multiAgentModeValue) rawMessage() json.RawMessage {
	switch mode.Kind {
	case "explicitRequestOnly", "proactive":
		encoded, _ := json.Marshal(mode.Kind)
		return encoded
	case "custom":
		encoded, _ := json.Marshal(map[string]string{"custom": mode.Text})
		return encoded
	default:
		return json.RawMessage("null")
	}
}

func (mode multiAgentModeValue) instructionsBody() string {
	switch mode.Kind {
	case "custom":
		return mode.Text
	case "proactive":
		return proactiveMultiAgentModeText
	case "explicitRequestOnly":
		return explicitRequestOnlyMultiAgentModeText
	default:
		return ""
	}
}

// multiAgentModeSnapshot is Rust's MultiAgentModeState serde shape. `mode` is
// always present (null when multi-agent is inactive) and `usage_hint_hash` is
// omitted when no usage hint exists.
type multiAgentModeSnapshot struct {
	Mode          json.RawMessage `json:"mode"`
	UsageHintHash string          `json:"usage_hint_hash,omitempty"`
}

type decodedMultiAgentMode struct {
	Kind          string
	Text          string
	UsageHintHash string
	State         worldStatePrevious
}

type worldStatePrevious int

const (
	worldStateAbsent worldStatePrevious = iota
	worldStateUnknown
	worldStateKnown
)

// decodeMultiAgentModeSnapshot reads the Rust shape and the retired Go
// `{mode:"custom", text:"…"}` shape.
func decodeMultiAgentModeSnapshot(raw json.RawMessage) decodedMultiAgentMode {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return decodedMultiAgentMode{State: worldStateAbsent}
	}
	var bare string
	if err := json.Unmarshal(trimmed, &bare); err == nil {
		return decodedMultiAgentMode{Kind: strings.TrimSpace(bare), State: worldStateKnown}
	}
	var legacy struct {
		Mode          json.RawMessage `json:"mode"`
		Text          string          `json:"text"`
		UsageHintHash string          `json:"usage_hint_hash"`
	}
	if err := json.Unmarshal(trimmed, &legacy); err != nil {
		return decodedMultiAgentMode{State: worldStateUnknown}
	}
	decoded := decodedMultiAgentMode{UsageHintHash: strings.TrimSpace(legacy.UsageHintHash), State: worldStateKnown}
	mode := bytes.TrimSpace(legacy.Mode)
	if len(mode) == 0 || bytes.Equal(mode, []byte("null")) {
		decoded.Kind = "none"
		return decoded
	}
	var modeID string
	if err := json.Unmarshal(mode, &modeID); err == nil {
		decoded.Kind = strings.TrimSpace(modeID)
		// The retired Go snapshot stored custom mode text in a sibling field.
		if decoded.Kind == "custom" {
			decoded.Text = legacy.Text
		}
		return decoded
	}
	var custom struct {
		Custom *string `json:"custom"`
	}
	if err := json.Unmarshal(mode, &custom); err == nil && custom.Custom != nil {
		decoded.Kind = "custom"
		decoded.Text = *custom.Custom
		return decoded
	}
	decoded.Kind = "none"
	return decoded
}

// decodeMultiAgentUsageHintSnapshot reads Rust's `WorldStateHash` snapshot. The
// retired Go section stored the rendered hint text, which simply never matches
// the new fingerprint and is rewritten on the next step.
func decodeMultiAgentUsageHintSnapshot(raw json.RawMessage) (string, worldStatePrevious) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", worldStateAbsent
	}
	var fingerprint string
	if err := json.Unmarshal(trimmed, &fingerprint); err != nil {
		return "", worldStateUnknown
	}
	return strings.TrimSpace(fingerprint), worldStateKnown
}

// multiAgentUsageHintForStep resolves the role instructions for the sampling
// step (Rust `session::multi_agents::usage_hint_text` + `resolve_usage_hints`).
func (r *RuntimeRouter) multiAgentUsageHintForStep(record *session.Record, cfg *config.Config, info *model.ModelInfo, v2 *config.MultiAgentV2Config) (multiAgentRoleInstructions, bool) {
	if r == nil || record == nil || cfg == nil || v2 == nil {
		return multiAgentRoleInstructions{}, false
	}
	isSubagent := strings.TrimSpace(record.Metadata.AgentPath) != "" || record.Metadata.AgentDepth > 0
	rootCatalogHintText, subagentCatalogHintText := r.multiAgentCatalogRoleInstructionsForModel(cfg, nil, info)
	omitUpdatePlan := !cfg.UpdatePlanEnabled() && rootCatalogHintText == nil && subagentCatalogHintText == nil
	if isSubagent {
		return resolveMultiAgentRoleInstructions(v2.SubagentUsageHintText, subagentCatalogHintText, agent.MultiAgentV2SubagentUsageHint, v2, omitUpdatePlan)
	}
	return resolveMultiAgentRoleInstructions(v2.RootAgentUsageHintText, rootCatalogHintText, agent.MultiAgentV2RootUsageHint, v2, omitUpdatePlan)
}

func truncateMultiAgentModeText(text string) string {
	return utils.TruncateText(text, utils.TokensPolicy(multiAgentModeMaxTokens))
}

// effectiveMultiAgentMode ports Rust `session::multi_agents::effective_multi_agent_mode`.
// The deprecated Go `multiAgentMode` turn parameter still selects the builtin
// policy when present; otherwise Ultra effort enables proactive delegation.
func (r *RuntimeRouter) effectiveMultiAgentMode(record *session.Record, params *turn.TurnStartParams, info *model.ModelInfo, cfg *config.Config, v2 *config.MultiAgentV2Config) multiAgentModeValue {
	if v2 == nil {
		return multiAgentModeValue{Kind: "none"}
	}
	modelMessages := (*model.MultiAgentMessages)(nil)
	if !v2.DisableNativeTools && info != nil && info.ModelMessages != nil {
		modelMessages = info.ModelMessages.MultiAgent
	}
	hint := v2.MultiAgentModeHintText
	if hint == nil && modelMessages != nil && modelMessages.Mode != nil {
		hint = modelMessages.Mode.HintText
	}
	if hint != nil {
		return multiAgentModeValue{Kind: "custom", Text: truncateMultiAgentModeText(*hint)}
	}
	explicitOverride := ""
	if params != nil && params.MultiAgentMode != nil {
		candidate := strings.TrimSpace(*params.MultiAgentMode)
		if candidate == string(MultiAgentModeProactive) || candidate == string(MultiAgentModeExplicitRequestOnly) {
			explicitOverride = candidate
		}
	}
	proactive := explicitOverride == string(MultiAgentModeProactive)
	if explicitOverride == "" && cfg != nil && params != nil && strings.TrimSpace(appReasoningEffortForTurn(cfg, params)) == "ultra" {
		proactive = true
	}
	if v2.DisableNativeTools {
		policy := explicitRequestOnlyMultiAgentModeText
		if proactive {
			policy = proactiveMultiAgentModeText
		}
		policy = strings.ReplaceAll(policy, "collaboration tools", "multi-agent tools")
		return multiAgentModeValue{Kind: "custom", Text: truncateMultiAgentModeText("\n" + policy + "\n\n" + hostedMultiAgentSkillGuidance + "\n")}
	}
	if modelMessages != nil && modelMessages.Mode != nil {
		if proactive && modelMessages.Mode.Proactive != nil {
			return multiAgentModeValue{Kind: "custom", Text: truncateMultiAgentModeText(*modelMessages.Mode.Proactive)}
		}
		if !proactive && modelMessages.Mode.Explicit != nil {
			return multiAgentModeValue{Kind: "custom", Text: truncateMultiAgentModeText(*modelMessages.Mode.Explicit)}
		}
	}
	if proactive {
		return multiAgentModeValue{Kind: "proactive"}
	}
	return multiAgentModeValue{Kind: "explicitRequestOnly"}
}
