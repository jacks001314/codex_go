package session

import "encoding/json"

type WorldState struct {
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
	// BaseInstructions carries the recorded base-instruction snapshot (Rust
	// section id `base_instructions`, #51188).
	BaseInstructions json.RawMessage `json:"baseInstructions,omitempty"`
	// SkillsCatalog carries the incremental selected-environment skill catalog
	// snapshot (Rust #57262 section id `skills`): the entries the model has
	// already seen and the cumulative metadata budget spent on them.
	SkillsCatalog json.RawMessage `json:"skillsCatalog,omitempty"`
	// HostSkillsCatalog carries the incremental host skill catalog snapshot
	// (Rust #57262 section id `host_skills`).
	HostSkillsCatalog json.RawMessage `json:"hostSkillsCatalog,omitempty"`
}

func DecodeWorldState(raw json.RawMessage) (*WorldState, error) {
	if len(raw) == 0 {
		return &WorldState{}, nil
	}
	var state WorldState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	state.Model = append(json.RawMessage(nil), state.Model...)
	state.Personality = append(json.RawMessage(nil), state.Personality...)
	state.ContextWindowGuidance = append(json.RawMessage(nil), state.ContextWindowGuidance...)
	state.CollaborationMode = append(json.RawMessage(nil), state.CollaborationMode...)
	state.PersistentMode = append(json.RawMessage(nil), state.PersistentMode...)
	state.PermissionInstructions = append(json.RawMessage(nil), state.PermissionInstructions...)
	state.ApprovedCommandPrefixes = append(json.RawMessage(nil), state.ApprovedCommandPrefixes...)
	state.RealtimeConversation = append(json.RawMessage(nil), state.RealtimeConversation...)
	state.MultiAgentMode = append(json.RawMessage(nil), state.MultiAgentMode...)
	state.MultiAgentUsageHint = append(json.RawMessage(nil), state.MultiAgentUsageHint...)
	state.ModelCatalog = append(json.RawMessage(nil), state.ModelCatalog...)
	state.Tools = append(json.RawMessage(nil), state.Tools...)
	state.TopLevelTools = append(json.RawMessage(nil), state.TopLevelTools...)
	state.BaseInstructions = append(json.RawMessage(nil), state.BaseInstructions...)
	state.SkillsCatalog = append(json.RawMessage(nil), state.SkillsCatalog...)
	state.HostSkillsCatalog = append(json.RawMessage(nil), state.HostSkillsCatalog...)
	return &state, nil
}

func EncodeWorldState(state *WorldState) (json.RawMessage, error) {
	if state == nil {
		return nil, nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}
