package session

import (
	"encoding/json"
	"strings"
)

// WorldState is Go's view of the persisted world-state comparison baseline.
//
// The persisted form is Rust's `WorldStateItem.state` (protocol.rs): a map
// keyed by the world-state section ID
// (core/src/context/world_state/*, `WorldStateSection::ID`), with each value
// being that section's snapshot. DecodeWorldState and EncodeWorldState
// translate between that wire shape and these per-section fields so a rollout
// written by either implementation stays readable by the other.
//
// The camelCase struct tags only remain for legacy Go stores written before
// the wire alignment; the keys actually written are the section IDs below.
type WorldState struct {
	AppsInstructions       json.RawMessage `json:"appsInstructions,omitempty"`
	Model                  json.RawMessage `json:"model,omitempty"`
	Personality            json.RawMessage `json:"personality,omitempty"`
	ContextWindowGuidance  json.RawMessage `json:"contextWindowGuidance,omitempty"`
	CollaborationMode      json.RawMessage `json:"collaborationMode,omitempty"`
	PersistentMode         json.RawMessage `json:"persistentMode,omitempty"`
	PermissionInstructions json.RawMessage `json:"permissionInstructions,omitempty"`
	// ApprovedCommandPrefixes carries the compact permissions world-state
	// section (Rust CompactPermissionsState, section id
	// `approved_command_prefixes`): the approved prefix set used when the full
	// permissions instructions are disabled.
	ApprovedCommandPrefixes json.RawMessage `json:"approvedCommandPrefixes,omitempty"`
	RealtimeConversation    json.RawMessage `json:"realtimeConversation,omitempty"`
	MultiAgentMode          json.RawMessage `json:"multiAgentMode,omitempty"`
	MultiAgentUsageHint     json.RawMessage `json:"multiAgentUsageHint,omitempty"`
	ModelCatalog            json.RawMessage `json:"modelCatalog,omitempty"`
	Tools                   json.RawMessage `json:"tools,omitempty"`
	// TopLevelTools carries the incremental Responses Lite tool catalog snapshot
	// (Rust section id `top_level_tools`, #50540): the window it was recorded for
	// and the stable hash of every declaration.
	TopLevelTools json.RawMessage `json:"topLevelTools,omitempty"`
	// BaseInstructions carries the retired Go-only `base_instructions`
	// snapshot (#51188). Rust replaced that section with `model`'s
	// WithInstructions form (#53872); the field stays decodable for migration
	// and is never written again.
	BaseInstructions json.RawMessage `json:"baseInstructions,omitempty"`
	// SkillsCatalog carries the incremental selected-environment skill catalog
	// snapshot (Rust #57262 section id `skills`): the entries the model has
	// already seen and the cumulative metadata budget spent on them.
	SkillsCatalog json.RawMessage `json:"skillsCatalog,omitempty"`
	// HostSkillsCatalog carries the incremental host skill catalog snapshot
	// (Rust #57262 section id `host_skills`).
	HostSkillsCatalog json.RawMessage `json:"hostSkillsCatalog,omitempty"`
	// Environments carries the environment section snapshot (Rust section id
	// `environments`, codex-rs/core/src/context/world_state/environment.rs).
	// The Go renderer always emits the full environment context, so only the
	// removal-sensitive values are recorded here: they let a later turn report
	// `<current_date status="unavailable" />` (and the shell_version
	// equivalent) when a value the model already saw disappears (#45825).
	Environments json.RawMessage `json:"environments,omitempty"`
	// CommandSessions carries the retained command-session snapshot (Rust
	// section id `command_sessions`, #59633): the harness-visible sessions the
	// model has already seen, used to suppress unchanged updates and to clear
	// stale handles after a restart or when the feature is disabled.
	CommandSessions json.RawMessage `json:"commandSessions,omitempty"`
	// PluginsInstructions carries the plugin guidance world-state snapshot
	// (Rust section id `plugins_instructions`): the hash of the generic wording
	// the model has seen and whether it came from the catalog (Rust #58894).
	PluginsInstructions json.RawMessage `json:"pluginsInstructions,omitempty"`
	// extra keeps sections Go does not model so writing the state back does not
	// erase snapshots that only Rust understands (`agents_md`,
	// `managed_developer_instructions`, `git_attribution`, ...).
	extra map[string]json.RawMessage
}

// worldStateSectionIDs maps each Rust world-state section ID onto the field
// holding its snapshot, plus the legacy Go key that older stores used.
var worldStateSectionIDs = []struct {
	id         string
	legacy     string
	decodeOnly bool
	field      func(*WorldState) *json.RawMessage
}{
	{"apps_instructions", "appsInstructions", false, func(s *WorldState) *json.RawMessage { return &s.AppsInstructions }},
	{"model", "model", false, func(s *WorldState) *json.RawMessage { return &s.Model }},
	{"personality", "personality", false, func(s *WorldState) *json.RawMessage { return &s.Personality }},
	{"context_window_guidance", "contextWindowGuidance", false, func(s *WorldState) *json.RawMessage { return &s.ContextWindowGuidance }},
	{"collaboration_mode", "collaborationMode", false, func(s *WorldState) *json.RawMessage { return &s.CollaborationMode }},
	{"persistent_mode", "persistentMode", false, func(s *WorldState) *json.RawMessage { return &s.PersistentMode }},
	{"permissions", "permissionInstructions", false, func(s *WorldState) *json.RawMessage { return &s.PermissionInstructions }},
	{"approved_command_prefixes", "approvedCommandPrefixes", false, func(s *WorldState) *json.RawMessage { return &s.ApprovedCommandPrefixes }},
	{"realtime", "realtimeConversation", false, func(s *WorldState) *json.RawMessage { return &s.RealtimeConversation }},
	{"multi_agent_mode", "multiAgentMode", false, func(s *WorldState) *json.RawMessage { return &s.MultiAgentMode }},
	{"multi_agent_usage_hint", "multiAgentUsageHint", false, func(s *WorldState) *json.RawMessage { return &s.MultiAgentUsageHint }},
	{"model_catalog", "modelCatalog", false, func(s *WorldState) *json.RawMessage { return &s.ModelCatalog }},
	{"tools", "tools", false, func(s *WorldState) *json.RawMessage { return &s.Tools }},
	{"top_level_tools", "topLevelTools", false, func(s *WorldState) *json.RawMessage { return &s.TopLevelTools }},
	{"base_instructions", "baseInstructions", true, func(s *WorldState) *json.RawMessage { return &s.BaseInstructions }},
	{"skills", "skillsCatalog", false, func(s *WorldState) *json.RawMessage { return &s.SkillsCatalog }},
	{"host_skills", "hostSkillsCatalog", false, func(s *WorldState) *json.RawMessage { return &s.HostSkillsCatalog }},
	{"environments", "environments", false, func(s *WorldState) *json.RawMessage { return &s.Environments }},
	{"command_sessions", "commandSessions", false, func(s *WorldState) *json.RawMessage { return &s.CommandSessions }},
	{"plugins_instructions", "pluginsInstructions", false, func(s *WorldState) *json.RawMessage { return &s.PluginsInstructions }},
}

// DecodeWorldState reads the Rust-shaped section map, accepting the legacy
// camelCase keys older Go stores used and preserving sections Go does not
// model.
func DecodeWorldState(raw json.RawMessage) (*WorldState, error) {
	state := &WorldState{}
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return state, nil
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sections); err != nil {
		return nil, err
	}
	known := make(map[string]struct{}, len(worldStateSectionIDs)*2)
	for _, section := range worldStateSectionIDs {
		known[section.id] = struct{}{}
		if section.legacy != section.id {
			known[section.legacy] = struct{}{}
		}
		value, ok := sections[section.id]
		if !ok {
			value = sections[section.legacy]
		}
		if len(value) > 0 {
			*section.field(state) = append(json.RawMessage(nil), value...)
		}
	}
	for key, value := range sections {
		if _, ok := known[key]; ok {
			continue
		}
		if state.extra == nil {
			state.extra = make(map[string]json.RawMessage, len(sections))
		}
		state.extra[key] = append(json.RawMessage(nil), value...)
	}
	return state, nil
}

// EncodeWorldState writes the Rust-shaped section map. Empty sections are
// omitted (Rust keeps absent entries absent) and unknown sections decoded
// earlier are written back unchanged.
func EncodeWorldState(state *WorldState) (json.RawMessage, error) {
	if state == nil {
		return nil, nil
	}
	sections := make(map[string]json.RawMessage, len(worldStateSectionIDs)+len(state.extra))
	for key, value := range state.extra {
		sections[key] = value
	}
	for _, section := range worldStateSectionIDs {
		if section.decodeOnly {
			continue
		}
		value := *section.field(state)
		if len(value) == 0 || strings.TrimSpace(string(value)) == "null" {
			continue
		}
		sections[section.id] = value
	}
	return json.Marshal(sections)
}
