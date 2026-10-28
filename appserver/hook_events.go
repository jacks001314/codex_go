package appserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"codex_go/config"
)

// HookEvents mirrors Rust protocol::hooks::HookEventsToml (codex-rs #56302).
// It is the shared hook configuration shape for managed requirements,
// hooks.json and owner-supplied environment attachments, using the PascalCase
// event keys Rust serializes.
type HookEvents struct {
	PreModelRequest   []config.ConfiguredHookGroup `json:"PreModelRequest"`
	PostModelRequest  []config.ConfiguredHookGroup `json:"PostModelRequest"`
	PreToolUse        []config.ConfiguredHookGroup `json:"PreToolUse"`
	PermissionRequest []config.ConfiguredHookGroup `json:"PermissionRequest"`
	PostToolUse       []config.ConfiguredHookGroup `json:"PostToolUse"`
	PreCompact        []config.ConfiguredHookGroup `json:"PreCompact"`
	PostCompact       []config.ConfiguredHookGroup `json:"PostCompact"`
	SessionStart      []config.ConfiguredHookGroup `json:"SessionStart"`
	SessionEnd        []config.ConfiguredHookGroup `json:"SessionEnd"`
	UserPromptSubmit  []config.ConfiguredHookGroup `json:"UserPromptSubmit"`
	SubagentStart     []config.ConfiguredHookGroup `json:"SubagentStart"`
	SubagentStop      []config.ConfiguredHookGroup `json:"SubagentStop"`
	Stop              []config.ConfiguredHookGroup `json:"Stop"`
	Interrupt         []config.ConfiguredHookGroup `json:"Interrupt"`
}

var hookEventOrder = []HookEventName{
	HookEventPreModelRequest,
	HookEventPostModelRequest,
	HookEventPreToolUse,
	HookEventPermissionRequest,
	HookEventPostToolUse,
	HookEventPreCompact,
	HookEventPostCompact,
	HookEventSessionStart,
	HookEventSessionEnd,
	HookEventUserPromptSubmit,
	HookEventSubagentStart,
	HookEventSubagentStop,
	HookEventStop,
	HookEventInterrupt,
}

// hookEventGroups is one HookEventName paired with its matcher groups, the Go
// shape of Rust's HookEventsToml::matcher_groups_mut.
type hookEventGroups struct {
	name   HookEventName
	json   string
	groups []config.ConfiguredHookGroup
}

// matcherGroups lists every event in Rust protocol order.
func (events HookEvents) matcherGroups() []hookEventGroups {
	return []hookEventGroups{
		{name: HookEventPreModelRequest, json: "PreModelRequest", groups: events.PreModelRequest},
		{name: HookEventPostModelRequest, json: "PostModelRequest", groups: events.PostModelRequest},
		{name: HookEventPreToolUse, json: "PreToolUse", groups: events.PreToolUse},
		{name: HookEventPermissionRequest, json: "PermissionRequest", groups: events.PermissionRequest},
		{name: HookEventPostToolUse, json: "PostToolUse", groups: events.PostToolUse},
		{name: HookEventPreCompact, json: "PreCompact", groups: events.PreCompact},
		{name: HookEventPostCompact, json: "PostCompact", groups: events.PostCompact},
		{name: HookEventSessionStart, json: "SessionStart", groups: events.SessionStart},
		{name: HookEventSessionEnd, json: "SessionEnd", groups: events.SessionEnd},
		{name: HookEventUserPromptSubmit, json: "UserPromptSubmit", groups: events.UserPromptSubmit},
		{name: HookEventSubagentStart, json: "SubagentStart", groups: events.SubagentStart},
		{name: HookEventSubagentStop, json: "SubagentStop", groups: events.SubagentStop},
		{name: HookEventStop, json: "Stop", groups: events.Stop},
		{name: HookEventInterrupt, json: "Interrupt", groups: events.Interrupt},
	}
}

// IsEmpty mirrors HookEventsToml::is_empty.
func (events HookEvents) IsEmpty() bool {
	for _, event := range events.matcherGroups() {
		if len(event.groups) > 0 {
			return false
		}
	}
	return true
}

// HandlerCount mirrors HookEventsToml::handler_count.
func (events HookEvents) HandlerCount() int {
	count := 0
	for _, event := range events.matcherGroups() {
		for _, group := range event.groups {
			count += len(group.Hooks)
		}
	}
	return count
}

// hookEventsFromManagedRequirements adapts managed hook requirements to the
// shared hook event shape. Managed directory fields are not hook handlers and
// stay on config.ManagedHooksRequirements.
func hookEventsFromManagedRequirements(managed *config.ManagedHooksRequirements) HookEvents {
	if managed == nil {
		return HookEvents{}
	}
	return HookEvents{
		PreModelRequest:   managed.PreModelRequest,
		PostModelRequest:  managed.PostModelRequest,
		PreToolUse:        managed.PreToolUse,
		PermissionRequest: managed.PermissionRequest,
		PostToolUse:       managed.PostToolUse,
		PreCompact:        managed.PreCompact,
		PostCompact:       managed.PostCompact,
		SessionStart:      managed.SessionStart,
		SessionEnd:        managed.SessionEnd,
		UserPromptSubmit:  managed.UserPromptSubmit,
		SubagentStart:     managed.SubagentStart,
		SubagentStop:      managed.SubagentStop,
		Stop:              managed.Stop,
		Interrupt:         managed.Interrupt,
	}
}

// cloneHookEvents deep-copies every group, handler and MCP input map so
// resolved environment configs never alias the map they were parsed from.
func cloneHookEvents(events HookEvents) HookEvents {
	clone := HookEvents{
		PreModelRequest:   cloneHookGroups(events.PreModelRequest),
		PostModelRequest:  cloneHookGroups(events.PostModelRequest),
		PreToolUse:        cloneHookGroups(events.PreToolUse),
		PermissionRequest: cloneHookGroups(events.PermissionRequest),
		PostToolUse:       cloneHookGroups(events.PostToolUse),
		PreCompact:        cloneHookGroups(events.PreCompact),
		PostCompact:       cloneHookGroups(events.PostCompact),
		SessionStart:      cloneHookGroups(events.SessionStart),
		SessionEnd:        cloneHookGroups(events.SessionEnd),
		UserPromptSubmit:  cloneHookGroups(events.UserPromptSubmit),
		SubagentStart:     cloneHookGroups(events.SubagentStart),
		SubagentStop:      cloneHookGroups(events.SubagentStop),
		Stop:              cloneHookGroups(events.Stop),
		Interrupt:         cloneHookGroups(events.Interrupt),
	}
	return clone
}

func cloneHookGroups(groups []config.ConfiguredHookGroup) []config.ConfiguredHookGroup {
	if groups == nil {
		return nil
	}
	out := make([]config.ConfiguredHookGroup, len(groups))
	for i := range groups {
		out[i] = config.ConfiguredHookGroup{
			Matcher: cloneStringPtrAppserver(groups[i].Matcher),
			Hooks:   make([]config.ConfiguredHookHandler, len(groups[i].Hooks)),
		}
		for j := range groups[i].Hooks {
			out[i].Hooks[j] = cloneConfiguredHookHandler(groups[i].Hooks[j])
		}
	}
	return out
}

func cloneConfiguredHookHandler(handler config.ConfiguredHookHandler) config.ConfiguredHookHandler {
	clone := handler
	clone.CommandWindows = cloneStringPtrAppserver(handler.CommandWindows)
	clone.StatusMessage = cloneStringPtrAppserver(handler.StatusMessage)
	if handler.TimeoutSec != nil {
		timeout := *handler.TimeoutSec
		clone.TimeoutSec = &timeout
	}
	clone.Input = cloneHookInput(handler.Input)
	return clone
}

// hookEventsFromAny parses an owner-supplied environment hook table.
func hookEventsFromAny(value any) (HookEvents, error) {
	if value == nil {
		return HookEvents{}, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return HookEvents{}, fmt.Errorf("invalid environment hooks: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var events HookEvents
	if err := decoder.Decode(&events); err != nil {
		return HookEvents{}, fmt.Errorf("invalid environment hooks: %w", err)
	}
	if err := validateHookEventHandlers(events); err != nil {
		return HookEvents{}, err
	}
	return canonicalizeHookEvents(normalizeHookEventInputs(events)), nil
}

// canonicalizeHookEvents stores empty event lists as nil so a parsed config
// and a re-parsed serialized config compare equal.
func canonicalizeHookEvents(events HookEvents) HookEvents {
	nilEmpty := func(groups []config.ConfiguredHookGroup) []config.ConfiguredHookGroup {
		if len(groups) == 0 {
			return nil
		}
		return groups
	}
	events.PreModelRequest = nilEmpty(events.PreModelRequest)
	events.PostModelRequest = nilEmpty(events.PostModelRequest)
	events.PreToolUse = nilEmpty(events.PreToolUse)
	events.PermissionRequest = nilEmpty(events.PermissionRequest)
	events.PostToolUse = nilEmpty(events.PostToolUse)
	events.PreCompact = nilEmpty(events.PreCompact)
	events.PostCompact = nilEmpty(events.PostCompact)
	events.SessionStart = nilEmpty(events.SessionStart)
	events.SessionEnd = nilEmpty(events.SessionEnd)
	events.UserPromptSubmit = nilEmpty(events.UserPromptSubmit)
	events.SubagentStart = nilEmpty(events.SubagentStart)
	events.SubagentStop = nilEmpty(events.SubagentStop)
	events.Stop = nilEmpty(events.Stop)
	events.Interrupt = nilEmpty(events.Interrupt)
	return events
}

// hookEventsToAny serializes an environment hook table. Empty input maps and
// empty event lists are written as empty arrays, matching HookEventsToml.
func hookEventsToAny(events HookEvents) map[string]any {
	out := make(map[string]any, len(hookEventOrder))
	for _, event := range events.matcherGroups() {
		out[event.json] = hookGroupsToAny(event.groups)
	}
	return out
}

func hookGroupsToAny(groups []config.ConfiguredHookGroup) []any {
	out := make([]any, 0, len(groups))
	for _, group := range groups {
		hooks := make([]any, 0, len(group.Hooks))
		for _, handler := range group.Hooks {
			hooks = append(hooks, hookHandlerToAny(handler))
		}
		entry := map[string]any{"hooks": hooks}
		if group.Matcher != nil {
			entry["matcher"] = *group.Matcher
		}
		out = append(out, entry)
	}
	return out
}

func hookHandlerToAny(handler config.ConfiguredHookHandler) map[string]any {
	out := map[string]any{"type": handler.Type}
	switch handler.Type {
	case string(HookHandlerCommand):
		out["command"] = handler.Command
		if handler.CommandWindows != nil {
			out["commandWindows"] = *handler.CommandWindows
		}
	case string(HookHandlerMCPTool):
		out["server"] = handler.Server
		out["tool"] = handler.Tool
		input := cloneHookInput(handler.Input)
		if input == nil {
			input = map[string]any{}
		}
		out["input"] = input
	}
	if handler.TimeoutSec != nil {
		out["timeoutSec"] = *handler.TimeoutSec
	}
	if handler.Async {
		out["async"] = true
	}
	if handler.StatusMessage != nil {
		out["statusMessage"] = *handler.StatusMessage
	}
	return out
}

// normalizeHookEventInputs converts json.Number values decoded from an
// environment attachment into the same int64/float64 shapes the requirements
// parser produces.
func normalizeHookEventInputs(events HookEvents) HookEvents {
	normalize := func(groups []config.ConfiguredHookGroup) []config.ConfiguredHookGroup {
		for i := range groups {
			for j := range groups[i].Hooks {
				groups[i].Hooks[j].Input = normalizeHookInputValues(groups[i].Hooks[j].Input)
			}
		}
		return groups
	}
	events.PreModelRequest = normalize(events.PreModelRequest)
	events.PostModelRequest = normalize(events.PostModelRequest)
	events.PreToolUse = normalize(events.PreToolUse)
	events.PermissionRequest = normalize(events.PermissionRequest)
	events.PostToolUse = normalize(events.PostToolUse)
	events.PreCompact = normalize(events.PreCompact)
	events.PostCompact = normalize(events.PostCompact)
	events.SessionStart = normalize(events.SessionStart)
	events.SessionEnd = normalize(events.SessionEnd)
	events.UserPromptSubmit = normalize(events.UserPromptSubmit)
	events.SubagentStart = normalize(events.SubagentStart)
	events.SubagentStop = normalize(events.SubagentStop)
	events.Stop = normalize(events.Stop)
	events.Interrupt = normalize(events.Interrupt)
	return events
}

func normalizeHookInputValues(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = normalizeHookInputValue(value)
	}
	return out
}

func normalizeHookInputValue(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		if fraction, err := typed.Float64(); err == nil {
			return fraction
		}
		return typed.String()
	case map[string]any:
		return normalizeHookInputValues(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = normalizeHookInputValue(typed[i])
		}
		return out
	default:
		return value
	}
}

// validateHookEventHandlers rejects handler types Rust's tagged enum cannot
// represent and MCP inputs that cannot be stored as TOML, mirroring
// deserialize_mcp_tool_input (#56302).
func validateHookEventHandlers(events HookEvents) error {
	for _, event := range events.matcherGroups() {
		for _, group := range event.groups {
			for _, handler := range group.Hooks {
				switch handler.Type {
				case string(HookHandlerCommand), string(HookHandlerPrompt), string(HookHandlerAgent):
					continue
				case string(HookHandlerMCPTool):
					if !mcpHookInputRepresentable(handler.Input) {
						return fmt.Errorf("MCP hook input must be representable as TOML")
					}
				default:
					return fmt.Errorf("unknown hook handler type %q in %s", handler.Type, event.name)
				}
			}
		}
	}
	return nil
}

const maxInt64Value = int64(^uint64(0) >> 1)

// mcpHookInputRepresentable mirrors Rust's representable() check: null cannot
// be written to TOML and unsigned values above i64::MAX cannot round-trip.
func mcpHookInputRepresentable(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case json.Number:
		if _, err := typed.Int64(); err == nil {
			return true
		}
		if unsigned, err := strconv.ParseUint(typed.String(), 10, 64); err == nil {
			return unsigned <= uint64(maxInt64Value)
		}
		_, err := strconv.ParseFloat(typed.String(), 64)
		return err == nil
	case uint64:
		return typed <= uint64(maxInt64Value)
	case map[string]any:
		for _, item := range typed {
			if !mcpHookInputRepresentable(item) {
				return false
			}
		}
		return true
	case []any:
		for _, item := range typed {
			if !mcpHookInputRepresentable(item) {
				return false
			}
		}
		return true
	default:
		return true
	}
}
