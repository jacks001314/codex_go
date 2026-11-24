package appserver

// Rust parity: codex-rs/ext/guardian-v3/src/housekeeper.rs and sampling.rs
// (#62372). The routing pass itself has no model runtime: a host supplies a
// sampler, the prompt is composed once, and each batch's answer becomes the
// routes and description updates the housekeeper commits.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"codex_go/agentboard"
)

// GuardianRoutingSampler runs one tool-free routing request: the housekeeper
// instructions plus the JSON input the model sees, returning its JSON answer.
type GuardianRoutingSampler func(ctx context.Context, instructions string, input string) (string, error)

// GuardianV3DeciderOptions configures the routing decider.
type GuardianV3DeciderOptions struct {
	// RoutingInstructions replaces the built-in routing sentence when set
	// (features.guardianv3.housekeeper.routing_instructions).
	RoutingInstructions string
	// MaxInputBytes caps one serialized batch; zero leaves it uncapped.
	MaxInputBytes int
}

// GuardianHousekeeperInput is the routing batch exactly as the model sees it:
// the board's channel descriptions plus the originals in source order (Rust
// RoutingInput).
type GuardianHousekeeperInput struct {
	Catalog map[string]*string `json:"catalog"`
	Sources []json.RawMessage  `json:"sources"`
}

// NewGuardianRoutingDecider composes the routing prompt once and decodes each
// batch's answer into the destinations the housekeeper commits.
func NewGuardianRoutingDecider(sampler GuardianRoutingSampler, options GuardianV3DeciderOptions) (agentboard.GuardianRoutingDecider, error) {
	if sampler == nil {
		return nil, errors.New("Guardian routing requires a sampler")
	}
	instructions := agentboard.GuardianHousekeeperPrompt(options.RoutingInstructions)
	return func(ctx context.Context, batch agentboard.GuardianRoutingBatch) (agentboard.GuardianRoutingDecision, error) {
		input, err := GuardianHousekeeperInputJSON(batch, options.MaxInputBytes)
		if err != nil {
			return agentboard.GuardianRoutingDecision{}, err
		}
		answer, err := sampler(ctx, instructions, input)
		if err != nil {
			return agentboard.GuardianRoutingDecision{}, err
		}
		return ParseGuardianRoutingDecision(answer)
	}, nil
}

// GuardianHousekeeperInputJSON serializes one batch in source order with sorted
// catalog keys, so the same batch always produces the same prompt bytes.
func GuardianHousekeeperInputJSON(batch agentboard.GuardianRoutingBatch, limit int) (string, error) {
	input := GuardianHousekeeperInput{Catalog: map[string]*string{}}
	names := make([]string, 0, len(batch.Catalog))
	for name := range batch.Catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		description := batch.Catalog[name]
		if description == nil {
			input.Catalog[name] = nil
			continue
		}
		trimmed := *description
		input.Catalog[name] = &trimmed
	}
	for _, source := range batch.Sources {
		original := bytes.TrimSpace([]byte(source.Text))
		if len(original) == 0 || original[0] != '{' || !json.Valid(original) {
			encoded, err := json.Marshal(struct {
				ID       agentboard.GuardianEvidenceID `json:"id"`
				Original string                        `json:"original"`
			}{ID: source.ID, Original: source.Text})
			if err != nil {
				return "", err
			}
			original = encoded
		}
		input.Sources = append(input.Sources, json.RawMessage(original))
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	if limit > 0 && len(encoded) > limit {
		return "", fmt.Errorf("Guardian routing input is %d bytes, over the %d-byte budget", len(encoded), limit)
	}
	return string(encoded), nil
}

// ParseGuardianRoutingDecision reads the model's JSON answer (Rust
// RoutingOutput, deny_unknown_fields) and rejects routes or descriptions the
// housekeeper would have to repair.
func ParseGuardianRoutingDecision(answer string) (agentboard.GuardianRoutingDecision, error) {
	payload := trimGuardianJSONFence(answer)
	if payload == "" {
		return agentboard.GuardianRoutingDecision{}, errors.New("Guardian routing answer was empty")
	}
	var decoded struct {
		Routes       [][]string `json:"routes"`
		Descriptions []struct {
			Channel     string `json:"channel"`
			Description string `json:"description"`
		} `json:"descriptions"`
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return agentboard.GuardianRoutingDecision{}, fmt.Errorf("decode Guardian routing answer: %w", err)
	}
	decision := agentboard.GuardianRoutingDecision{Routes: decoded.Routes}
	seen := map[string]struct{}{}
	for _, route := range decoded.Routes {
		if len(route) == 0 {
			return agentboard.GuardianRoutingDecision{}, errors.New("Guardian routing answer has an empty route")
		}
	}
	for _, update := range decoded.Descriptions {
		channel := strings.TrimSpace(update.Channel)
		if channel == "" {
			return agentboard.GuardianRoutingDecision{}, errors.New("Guardian routing answer names an empty channel")
		}
		if _, ok := seen[channel]; ok {
			return agentboard.GuardianRoutingDecision{}, fmt.Errorf("Guardian routing answer repeats channel %q", channel)
		}
		seen[channel] = struct{}{}
		decision.Descriptions = append(decision.Descriptions, agentboard.GuardianDescriptionUpdate{
			Channel:     channel,
			Description: strings.TrimSpace(update.Description),
		})
	}
	return decision, nil
}

// trimGuardianJSONFence unwraps a fenced JSON block, because models commonly
// answer with a ```json body even when asked for bare JSON.
func trimGuardianJSONFence(answer string) string {
	trimmed := strings.TrimSpace(answer)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	if newline := strings.IndexByte(trimmed, '\n'); newline >= 0 {
		trimmed = trimmed[newline+1:]
	} else {
		return ""
	}
	if end := strings.LastIndex(trimmed, "```"); end >= 0 {
		trimmed = trimmed[:end]
	}
	return strings.TrimSpace(trimmed)
}
