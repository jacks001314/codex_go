package session

import "encoding/json"

type WorldState struct {
	Model                  json.RawMessage `json:"model,omitempty"`
	Personality            json.RawMessage `json:"personality,omitempty"`
	CollaborationMode      json.RawMessage `json:"collaborationMode,omitempty"`
	PermissionInstructions json.RawMessage `json:"permissionInstructions,omitempty"`
	RealtimeConversation   json.RawMessage `json:"realtimeConversation,omitempty"`
	MultiAgentMode         json.RawMessage `json:"multiAgentMode,omitempty"`
	Tools                  json.RawMessage `json:"tools,omitempty"`
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
	state.CollaborationMode = append(json.RawMessage(nil), state.CollaborationMode...)
	state.PermissionInstructions = append(json.RawMessage(nil), state.PermissionInstructions...)
	state.RealtimeConversation = append(json.RawMessage(nil), state.RealtimeConversation...)
	state.MultiAgentMode = append(json.RawMessage(nil), state.MultiAgentMode...)
	state.Tools = append(json.RawMessage(nil), state.Tools...)
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
