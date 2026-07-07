package appserver

import (
	"encoding/json"
	"strings"
)

type HookUniversalOutput struct {
	ContinueProcessing bool
	StopReason         *string
	SuppressOutput     bool
	SystemMessage      *string
}

type HookSessionStartOutput struct {
	Universal         *HookUniversalOutput
	AdditionalContext *string
}

type HookPreToolUseOutput struct {
	Universal         *HookUniversalOutput
	BlockReason       *string
	AdditionalContext *string
	UpdatedInput      any
	InvalidReason     *string
}

type HookPermissionRequestDecisionKind string

const (
	HookPermissionRequestAllow HookPermissionRequestDecisionKind = "allow"
	HookPermissionRequestDeny  HookPermissionRequestDecisionKind = "deny"
)

type HookPermissionRequestDecision struct {
	Kind    HookPermissionRequestDecisionKind
	Message *string
}

type HookPermissionRequestOutput struct {
	Universal     *HookUniversalOutput
	Decision      *HookPermissionRequestDecision
	InvalidReason *string
}

type HookPostToolUseOutput struct {
	Universal          *HookUniversalOutput
	ShouldBlock        bool
	Reason             *string
	AdditionalContext  *string
	InvalidReason      *string
	InvalidBlockReason *string
}

type HookUserPromptSubmitOutput struct {
	Universal          *HookUniversalOutput
	ShouldBlock        bool
	Reason             *string
	AdditionalContext  *string
	InvalidBlockReason *string
}

type HookStopOutput struct {
	Universal          *HookUniversalOutput
	ShouldBlock        bool
	Reason             *string
	InvalidBlockReason *string
}

type HookStatelessOutput struct {
	Universal         *HookUniversalOutput
	AdditionalContext *string
	InvalidReason     *string
}

type hookUniversalWire struct {
	Continue       *bool   `json:"continue"`
	StopReason     *string `json:"stopReason"`
	SuppressOutput bool    `json:"suppressOutput"`
	SystemMessage  *string `json:"systemMessage"`
}

type hookSpecificAdditionalContextWire struct {
	AdditionalContext *string `json:"additionalContext"`
}

type hookPreToolUseSpecificWire struct {
	PermissionDecision       *string         `json:"permissionDecision"`
	PermissionDecisionReason *string         `json:"permissionDecisionReason"`
	UpdatedInput             map[string]any  `json:"updatedInput"`
	AdditionalContext        *string         `json:"additionalContext"`
	HookEventName            string          `json:"hookEventName"`
	RawUpdatedInput          map[string]any  `json:"-"`
	Raw                      map[string]any  `json:"-"`
	Unknown                  json.RawMessage `json:"-"`
}

type hookPermissionRequestSpecificWire struct {
	Decision *hookPermissionRequestDecisionWire `json:"decision"`
}

type hookPermissionRequestDecisionWire struct {
	Behavior           string `json:"behavior"`
	UpdatedInput       any    `json:"updatedInput"`
	UpdatedPermissions any    `json:"updatedPermissions"`
	Message            string `json:"message"`
	Interrupt          bool   `json:"interrupt"`
}

type hookPostToolUseSpecificWire struct {
	HookEventName        string  `json:"hookEventName"`
	AdditionalContext    *string `json:"additionalContext"`
	UpdatedMCPToolOutput any     `json:"updatedMCPToolOutput"`
}

type hookOutputEnvelope struct {
	hookUniversalWire
	Decision            *string                            `json:"decision"`
	Reason              *string                            `json:"reason"`
	HookSpecificRaw     json.RawMessage                    `json:"hookSpecificOutput"`
	HookSpecificGeneric *hookSpecificAdditionalContextWire `json:"-"`
}

func ParseHookSessionStartOutput(stdout string) *HookSessionStartOutput {
	wire := parseHookOutputEnvelope(stdout)
	if wire == nil {
		return nil
	}
	return &HookSessionStartOutput{
		Universal:         universalOutput(&wire.hookUniversalWire),
		AdditionalContext: parseHookAdditionalContext(wire.HookSpecificRaw),
	}
}

func ParseHookSubagentStartOutput(stdout string) *HookSessionStartOutput {
	return ParseHookSessionStartOutput(stdout)
}

func ParseHookPreToolUseOutput(stdout string) *HookPreToolUseOutput {
	wire := parseHookOutputEnvelope(stdout)
	if wire == nil {
		return nil
	}
	universal := universalOutput(&wire.hookUniversalWire)
	var specific hookPreToolUseSpecificWire
	hasSpecific := decodeHookSpecific(wire.HookSpecificRaw, &specific)
	additionalContext := specific.AdditionalContext
	invalidReason := unsupportedPreToolUseUniversal(universal)
	if invalidReason == nil {
		if hasSpecific && (specific.PermissionDecision != nil || specific.PermissionDecisionReason != nil || specific.UpdatedInput != nil) {
			invalidReason = unsupportedPreToolUseSpecific(&specific)
		} else {
			invalidReason = unsupportedPreToolUseLegacy(wire.Decision, wire.Reason)
		}
	}
	var blockReason *string
	var updatedInput any
	if invalidReason == nil {
		if hasSpecific && (specific.PermissionDecision != nil || specific.PermissionDecisionReason != nil || specific.UpdatedInput != nil) {
			if specific.PermissionDecision != nil && *specific.PermissionDecision == "deny" {
				blockReason = trimmedStringPointer(specific.PermissionDecisionReason)
			}
			if specific.PermissionDecision != nil && *specific.PermissionDecision == "allow" && specific.UpdatedInput != nil {
				updatedInput = specific.UpdatedInput
			}
		} else if wire.Decision != nil && *wire.Decision == "block" {
			blockReason = trimmedStringPointer(wire.Reason)
		}
	}
	return &HookPreToolUseOutput{
		Universal:         universal,
		BlockReason:       blockReason,
		AdditionalContext: additionalContext,
		UpdatedInput:      updatedInput,
		InvalidReason:     invalidReason,
	}
}

func ParseHookPermissionRequestOutput(stdout string) *HookPermissionRequestOutput {
	wire := parseHookOutputEnvelope(stdout)
	if wire == nil {
		return nil
	}
	universal := universalOutput(&wire.hookUniversalWire)
	var specific hookPermissionRequestSpecificWire
	_ = decodeHookSpecific(wire.HookSpecificRaw, &specific)
	invalidReason := unsupportedPermissionRequestUniversal(universal)
	if invalidReason == nil && specific.Decision != nil {
		invalidReason = unsupportedPermissionRequestDecision(specific.Decision)
	}
	var decision *HookPermissionRequestDecision
	if invalidReason == nil && specific.Decision != nil {
		decision = permissionRequestDecision(specific.Decision)
	}
	return &HookPermissionRequestOutput{
		Universal:     universal,
		Decision:      decision,
		InvalidReason: invalidReason,
	}
}

func ParseHookPostToolUseOutput(stdout string) *HookPostToolUseOutput {
	wire := parseHookOutputEnvelope(stdout)
	if wire == nil {
		return nil
	}
	universal := universalOutput(&wire.hookUniversalWire)
	var specific hookPostToolUseSpecificWire
	_ = decodeHookSpecific(wire.HookSpecificRaw, &specific)
	invalidReason := unsupportedPostToolUseUniversal(universal)
	if invalidReason == nil && specific.UpdatedMCPToolOutput != nil {
		invalidReason = stringPointer("PostToolUse hook returned unsupported updatedMCPToolOutput")
	}
	shouldBlock := wire.Decision != nil && *wire.Decision == "block"
	invalidBlockReason := invalidHookBlockReason("PostToolUse", shouldBlock, wire.Reason)
	if !shouldBlock && universal.ContinueProcessing && wire.Reason != nil {
		invalidBlockReason = stringPointer("PostToolUse hook returned reason without decision")
	}
	return &HookPostToolUseOutput{
		Universal:          universal,
		ShouldBlock:        shouldBlock && invalidReason == nil && invalidBlockReason == nil,
		Reason:             wire.Reason,
		AdditionalContext:  specific.AdditionalContext,
		InvalidReason:      invalidReason,
		InvalidBlockReason: invalidBlockReason,
	}
}

func ParseHookPreCompactOutput(stdout string) *HookStatelessOutput {
	wire := parseHookOutputEnvelope(stdout)
	if wire == nil {
		return nil
	}
	return &HookStatelessOutput{
		Universal:         universalOutput(&wire.hookUniversalWire),
		AdditionalContext: parseHookAdditionalContext(wire.HookSpecificRaw),
	}
}

func ParseHookPostCompactOutput(stdout string) *HookStatelessOutput {
	return ParseHookPreCompactOutput(stdout)
}

func ParseHookUserPromptSubmitOutput(stdout string) *HookUserPromptSubmitOutput {
	wire := parseHookOutputEnvelope(stdout)
	if wire == nil {
		return nil
	}
	shouldBlock := wire.Decision != nil && *wire.Decision == "block"
	invalidBlockReason := invalidHookBlockReason("UserPromptSubmit", shouldBlock, wire.Reason)
	return &HookUserPromptSubmitOutput{
		Universal:          universalOutput(&wire.hookUniversalWire),
		ShouldBlock:        shouldBlock && invalidBlockReason == nil,
		Reason:             wire.Reason,
		AdditionalContext:  parseHookAdditionalContext(wire.HookSpecificRaw),
		InvalidBlockReason: invalidBlockReason,
	}
}

func ParseHookStopOutput(stdout string) *HookStopOutput {
	return parseHookStopOutput(stdout, "Stop")
}

func ParseHookSubagentStopOutput(stdout string) *HookStopOutput {
	return parseHookStopOutput(stdout, "SubagentStop")
}

func HookOutputLooksLikeJSON(stdout string) bool {
	trimmed := strings.TrimLeft(stdout, " \t\r\n")
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func parseHookStopOutput(stdout string, eventName string) *HookStopOutput {
	wire := parseHookOutputEnvelope(stdout)
	if wire == nil {
		return nil
	}
	shouldBlock := wire.Decision != nil && *wire.Decision == "block"
	invalidBlockReason := invalidHookBlockReason(eventName, shouldBlock, wire.Reason)
	return &HookStopOutput{
		Universal:          universalOutput(&wire.hookUniversalWire),
		ShouldBlock:        shouldBlock && invalidBlockReason == nil,
		Reason:             wire.Reason,
		InvalidBlockReason: invalidBlockReason,
	}
}

func parseHookOutputEnvelope(stdout string) *hookOutputEnvelope {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return nil
	}
	var probe any
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return nil
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil
	}
	var envelope hookOutputEnvelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return nil
	}
	return &envelope
}

func decodeHookSpecific(raw json.RawMessage, out any) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	return json.Unmarshal(raw, out) == nil
}

func parseHookAdditionalContext(raw json.RawMessage) *string {
	var specific hookSpecificAdditionalContextWire
	if !decodeHookSpecific(raw, &specific) {
		return nil
	}
	return specific.AdditionalContext
}

func universalOutput(wire *hookUniversalWire) *HookUniversalOutput {
	continueProcessing := true
	if wire != nil && wire.Continue != nil {
		continueProcessing = *wire.Continue
	}
	if wire == nil {
		return &HookUniversalOutput{ContinueProcessing: continueProcessing}
	}
	return &HookUniversalOutput{
		ContinueProcessing: continueProcessing,
		StopReason:         cloneString(wire.StopReason),
		SuppressOutput:     wire.SuppressOutput,
		SystemMessage:      cloneString(wire.SystemMessage),
	}
}

func unsupportedPreToolUseUniversal(universal *HookUniversalOutput) *string {
	switch {
	case universal != nil && !universal.ContinueProcessing:
		return stringPointer("PreToolUse hook returned unsupported continue:false")
	case universal != nil && universal.StopReason != nil:
		return stringPointer("PreToolUse hook returned unsupported stopReason")
	case universal != nil && universal.SuppressOutput:
		return stringPointer("PreToolUse hook returned unsupported suppressOutput")
	default:
		return nil
	}
}

func unsupportedPermissionRequestUniversal(universal *HookUniversalOutput) *string {
	switch {
	case universal != nil && !universal.ContinueProcessing:
		return stringPointer("PermissionRequest hook returned unsupported continue:false")
	case universal != nil && universal.StopReason != nil:
		return stringPointer("PermissionRequest hook returned unsupported stopReason")
	case universal != nil && universal.SuppressOutput:
		return stringPointer("PermissionRequest hook returned unsupported suppressOutput")
	default:
		return nil
	}
}

func unsupportedPostToolUseUniversal(universal *HookUniversalOutput) *string {
	if universal != nil && universal.SuppressOutput {
		return stringPointer("PostToolUse hook returned unsupported suppressOutput")
	}
	return nil
}

func unsupportedPreToolUseSpecific(output *hookPreToolUseSpecificWire) *string {
	if output == nil {
		return nil
	}
	if output.UpdatedInput != nil && (output.PermissionDecision == nil || *output.PermissionDecision != "allow") {
		return stringPointer("PreToolUse hook returned updatedInput without permissionDecision:allow")
	}
	if output.PermissionDecision == nil {
		if output.PermissionDecisionReason != nil {
			return stringPointer("PreToolUse hook returned permissionDecisionReason without permissionDecision")
		}
		return nil
	}
	switch *output.PermissionDecision {
	case "allow":
		if output.UpdatedInput == nil {
			return stringPointer("PreToolUse hook returned unsupported permissionDecision:allow")
		}
	case "ask":
		return stringPointer("PreToolUse hook returned unsupported permissionDecision:ask")
	case "deny":
		if trimmedStringPointer(output.PermissionDecisionReason) == nil {
			return stringPointer("PreToolUse hook returned permissionDecision:deny without a non-empty permissionDecisionReason")
		}
	}
	return nil
}

func unsupportedPreToolUseLegacy(decision *string, reason *string) *string {
	if decision == nil {
		if reason != nil {
			return stringPointer("PreToolUse hook returned reason without decision")
		}
		return nil
	}
	switch *decision {
	case "approve":
		return stringPointer("PreToolUse hook returned unsupported decision:approve")
	case "block":
		if trimmedStringPointer(reason) == nil {
			return stringPointer("PreToolUse hook returned decision:block without a non-empty reason")
		}
	}
	return nil
}

func unsupportedPermissionRequestDecision(decision *hookPermissionRequestDecisionWire) *string {
	if decision == nil {
		return nil
	}
	switch {
	case decision.UpdatedInput != nil:
		return stringPointer("PermissionRequest hook returned unsupported updatedInput")
	case decision.UpdatedPermissions != nil:
		return stringPointer("PermissionRequest hook returned unsupported updatedPermissions")
	case decision.Interrupt:
		return stringPointer("PermissionRequest hook returned unsupported interrupt:true")
	default:
		return nil
	}
}

func permissionRequestDecision(decision *hookPermissionRequestDecisionWire) *HookPermissionRequestDecision {
	if decision == nil {
		return nil
	}
	switch decision.Behavior {
	case "allow":
		return &HookPermissionRequestDecision{Kind: HookPermissionRequestAllow}
	case "deny":
		message := trimmedStringPointer(&decision.Message)
		if message == nil {
			message = stringPointer("PermissionRequest hook denied approval")
		}
		return &HookPermissionRequestDecision{Kind: HookPermissionRequestDeny, Message: message}
	default:
		return nil
	}
}

func invalidHookBlockReason(eventName string, shouldBlock bool, reason *string) *string {
	if shouldBlock && trimmedStringPointer(reason) == nil {
		return stringPointer(eventName + " hook returned decision:block without a non-empty reason")
	}
	return nil
}

func trimmedStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func stringPointer(value string) *string {
	return &value
}
