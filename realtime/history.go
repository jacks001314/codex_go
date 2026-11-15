package realtime

// Canonical Voice history for every host. The reducer owns the presentation
// rules that decide when a transcript segment, a promoted agent item, or a
// session boundary enters the thread timeline, so the app-server item
// notifications and the persisted rollout agree with the Rust reducer.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// RealtimeItem is a thread-scoped realtime item in the canonical timeline. The
// wire shape flattens the tagged content into the item, matching the Rust
// app-server v2 item.
type RealtimeItem struct {
	ID                string
	RealtimeSessionID string
	ObservedAtMS      *int64
	Content           RealtimeItemContent
}

// RealtimeItemKind is the flattened content tag.
type RealtimeItemKind string

const (
	RealtimeItemKindSessionStarted    RealtimeItemKind = "realtimeSessionStarted"
	RealtimeItemKindFrame             RealtimeItemKind = "frame"
	RealtimeItemKindTranscriptSegment RealtimeItemKind = "transcriptSegment"
	RealtimeItemKindBemItemPromoted   RealtimeItemKind = "bemItemPromoted"
	RealtimeItemKindSessionClosed     RealtimeItemKind = "realtimeSessionClosed"
	RealtimeItemKindTurnInputAccepted RealtimeItemKind = "turnInputAccepted"
	RealtimeItemKindSessionEndInput   RealtimeItemKind = "sessionEndInputSaved"
)

// RealtimeTranscriptRole identifies who spoke a transcript segment.
type RealtimeTranscriptRole string

const (
	RealtimeTranscriptRoleUser      RealtimeTranscriptRole = "user"
	RealtimeTranscriptRoleAssistant RealtimeTranscriptRole = "assistant"
)

// RealtimeSessionOutcome describes how a realtime session ended.
type RealtimeSessionOutcome string

const (
	RealtimeSessionOutcomeEnded  RealtimeSessionOutcome = "ended"
	RealtimeSessionOutcomeFailed RealtimeSessionOutcome = "failed"
)

// BemItemPresentationKind selects how a backing agent item appears in the
// realtime conversation.
type BemItemPresentationKind string

const (
	BemPresentationWholeItem           BemItemPresentationKind = "wholeItem"
	BemPresentationInlineMarkdown      BemItemPresentationKind = "inlineMarkdown"
	BemPresentationInlineVisualization BemItemPresentationKind = "inlineVisualization"
)

// BemItemPresentation is a tagged presentation choice. Index only applies to
// inline visualizations.
type BemItemPresentation struct {
	Kind  BemItemPresentationKind
	Index uint32
}

// RealtimeItemContent carries the fields of exactly one item kind.
type RealtimeItemContent struct {
	Kind         RealtimeItemKind
	Role         RealtimeTranscriptRole
	Text         string
	TurnID       string
	ItemID       string
	Presentation BemItemPresentation
	Outcome      RealtimeSessionOutcome
	HandoffID    string
	HandoffMode  RealtimeHandoffMode
	SourceEvents []string
	InputItemID  string
	Input        string
}

// RealtimeHandoffMode records how an accepted realtime input entered the agent
// turn (Rust RealtimeHandoffMode).
type RealtimeHandoffMode string

const (
	RealtimeHandoffModeStart RealtimeHandoffMode = "start"
	RealtimeHandoffModeSteer RealtimeHandoffMode = "steer"
)

// RealtimeFrameChannels carries the payloads of one realtime frame.
type RealtimeFrameChannels struct {
	VoiceTranscript RealtimeVoiceTranscript
}

// RealtimeVoiceTranscript is the V1 voice payload of a realtime frame.
type RealtimeVoiceTranscript struct {
	Role RealtimeTranscriptRole
	Text string
}

// MarshalJSON emits the Rust wire shape for the selected kind.
func (c RealtimeItemContent) MarshalJSON() ([]byte, error) {
	switch c.Kind {
	case RealtimeItemKindSessionStarted:
		return []byte(`{"type":"realtimeSessionStarted"}`), nil
	case RealtimeItemKindFrame:
		return marshalFrameChannels(c)
	case RealtimeItemKindTranscriptSegment:
		if !validTranscriptRole(c.Role) {
			return nil, fmt.Errorf("invalid realtime transcript role %q", c.Role)
		}
		return []byte(`{"type":"transcriptSegment","role":` + jsonString(string(c.Role)) +
			`,"text":` + jsonString(c.Text) + `}`), nil
	case RealtimeItemKindBemItemPromoted:
		presentation, err := json.Marshal(c.Presentation)
		if err != nil {
			return nil, err
		}
		return []byte(`{"type":"bemItemPromoted","turnId":` + jsonString(c.TurnID) +
			`,"itemId":` + jsonString(c.ItemID) +
			`,"presentation":` + string(presentation) + `}`), nil
	case RealtimeItemKindSessionClosed:
		if !validSessionOutcome(c.Outcome) {
			return nil, fmt.Errorf("invalid realtime session outcome %q", c.Outcome)
		}
		return []byte(`{"type":"realtimeSessionClosed","outcome":` + jsonString(string(c.Outcome)) + `}`), nil
	case RealtimeItemKindTurnInputAccepted:
		mode, err := marshalHandoffMode(c.HandoffMode)
		if err != nil {
			return nil, err
		}
		sources, err := json.Marshal(c.SourceEvents)
		if err != nil {
			return nil, err
		}
		return []byte(`{"type":"turnInputAccepted","handoffId":` + jsonString(c.HandoffID) +
			`,"turnId":` + jsonString(c.TurnID) +
			`,"mode":` + mode +
			`,"sourceEventIds":` + string(sources) +
			`,"inputItemId":` + jsonString(c.InputItemID) +
			`,"input":` + jsonString(c.Input) + `}`), nil
	case RealtimeItemKindSessionEndInput:
		sources, err := json.Marshal(c.SourceEvents)
		if err != nil {
			return nil, err
		}
		return []byte(`{"type":"sessionEndInputSaved","sourceEventIds":` + string(sources) +
			`,"inputItemId":` + jsonString(c.InputItemID) +
			`,"input":` + jsonString(c.Input) + `}`), nil
	default:
		return nil, fmt.Errorf("invalid realtime item kind %q", c.Kind)
	}
}

// MarshalJSON emits the flattened item, matching the Rust flatten attribute.
func (i RealtimeItem) MarshalJSON() ([]byte, error) {
	content, err := json.Marshal(i.Content)
	if err != nil {
		return nil, err
	}
	if len(content) < 2 || content[0] != '{' {
		return nil, fmt.Errorf("invalid realtime item content encoding")
	}
	var builder strings.Builder
	builder.WriteString(`{"id":`)
	builder.WriteString(jsonString(i.ID))
	builder.WriteString(`,"realtimeSessionId":`)
	builder.WriteString(jsonString(i.RealtimeSessionID))
	if i.ObservedAtMS != nil {
		builder.WriteString(`,"observedAtMs":`)
		builder.WriteString(fmt.Sprintf("%d", *i.ObservedAtMS))
	}
	if body := content[1:]; len(body) > 1 {
		builder.WriteByte(',')
		builder.Write(body)
	}
	return []byte(builder.String()), nil
}

// MarshalJSON emits the Rust presentation wire shape.
func (p BemItemPresentation) MarshalJSON() ([]byte, error) {
	switch p.Kind {
	case BemPresentationWholeItem:
		return []byte(`{"type":"wholeItem"}`), nil
	case BemPresentationInlineMarkdown:
		return []byte(`{"type":"inlineMarkdown"}`), nil
	case BemPresentationInlineVisualization:
		return []byte(`{"type":"inlineVisualization","index":` + fmt.Sprintf("%d", p.Index) + `}`), nil
	default:
		return nil, fmt.Errorf("invalid bem item presentation %q", p.Kind)
	}
}

// jsonString encodes a Go string as a JSON string. Encoding a string value
// cannot fail.
func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// marshalFrameChannels emits V1's only frame channel. The core and app-server
// wire shapes share the channel names.
func marshalFrameChannels(c RealtimeItemContent) ([]byte, error) {
	if !validTranscriptRole(c.Role) {
		return nil, fmt.Errorf("invalid realtime transcript role %q", c.Role)
	}
	return []byte(`{"type":"frame","channels":{"voice_transcript":{"role":` + jsonString(string(c.Role)) +
		`,"text":` + jsonString(c.Text) + `}}}`), nil
}

func marshalHandoffMode(mode RealtimeHandoffMode) (string, error) {
	switch mode {
	case RealtimeHandoffModeStart, RealtimeHandoffModeSteer:
		return jsonString(string(mode)), nil
	default:
		return "", fmt.Errorf("invalid realtime handoff mode %q", mode)
	}
}

// MarshalRolloutJSON emits the canonical snake_case wire shape persisted in a
// `realtime_item` rollout record (Rust RolloutItem::RealtimeItem).
func (i RealtimeItem) MarshalRolloutJSON() ([]byte, error) {
	content, err := i.Content.marshalRolloutJSON()
	if err != nil {
		return nil, err
	}
	var builder strings.Builder
	builder.WriteString(`{"id":`)
	builder.WriteString(jsonString(i.ID))
	builder.WriteString(`,"realtime_session_id":`)
	builder.WriteString(jsonString(i.RealtimeSessionID))
	if i.ObservedAtMS != nil {
		builder.WriteString(`,"observed_at_ms":`)
		builder.WriteString(fmt.Sprintf("%d", *i.ObservedAtMS))
	}
	if body := content[1:]; len(body) > 1 {
		builder.WriteByte(',')
		builder.Write(body)
	}
	return []byte(builder.String()), nil
}

// marshalRolloutJSON emits the canonical tagged content object.
func (c RealtimeItemContent) marshalRolloutJSON() ([]byte, error) {
	switch c.Kind {
	case RealtimeItemKindSessionStarted:
		return []byte(`{"type":"realtime_session_started"}`), nil
	case RealtimeItemKindFrame:
		return marshalFrameChannels(c)
	case RealtimeItemKindTranscriptSegment:
		if !validTranscriptRole(c.Role) {
			return nil, fmt.Errorf("invalid realtime transcript role %q", c.Role)
		}
		return []byte(`{"type":"transcript_segment","role":` + jsonString(string(c.Role)) +
			`,"text":` + jsonString(c.Text) + `}`), nil
	case RealtimeItemKindBemItemPromoted:
		presentation, err := c.Presentation.marshalRolloutJSON()
		if err != nil {
			return nil, err
		}
		return []byte(`{"type":"bem_item_promoted","turn_id":` + jsonString(c.TurnID) +
			`,"item_id":` + jsonString(c.ItemID) +
			`,"presentation":` + string(presentation) + `}`), nil
	case RealtimeItemKindSessionClosed:
		if !validSessionOutcome(c.Outcome) {
			return nil, fmt.Errorf("invalid realtime session outcome %q", c.Outcome)
		}
		return []byte(`{"type":"realtime_session_closed","outcome":` + jsonString(string(c.Outcome)) + `}`), nil
	case RealtimeItemKindTurnInputAccepted:
		mode, err := marshalHandoffMode(c.HandoffMode)
		if err != nil {
			return nil, err
		}
		sources, err := json.Marshal(c.SourceEvents)
		if err != nil {
			return nil, err
		}
		return []byte(`{"type":"turn_input_accepted","handoff_id":` + jsonString(c.HandoffID) +
			`,"turn_id":` + jsonString(c.TurnID) +
			`,"mode":` + mode +
			`,"source_event_ids":` + string(sources) +
			`,"input_item_id":` + jsonString(c.InputItemID) +
			`,"input":` + jsonString(c.Input) + `}`), nil
	case RealtimeItemKindSessionEndInput:
		sources, err := json.Marshal(c.SourceEvents)
		if err != nil {
			return nil, err
		}
		return []byte(`{"type":"session_end_input_saved","source_event_ids":` + string(sources) +
			`,"input_item_id":` + jsonString(c.InputItemID) +
			`,"input":` + jsonString(c.Input) + `}`), nil
	default:
		return nil, fmt.Errorf("invalid realtime item kind %q", c.Kind)
	}
}

// marshalRolloutJSON emits the canonical tagged presentation object.
func (p BemItemPresentation) marshalRolloutJSON() ([]byte, error) {
	switch p.Kind {
	case BemPresentationWholeItem:
		return []byte(`{"type":"whole_item"}`), nil
	case BemPresentationInlineMarkdown:
		return []byte(`{"type":"inline_markdown"}`), nil
	case BemPresentationInlineVisualization:
		return []byte(`{"type":"inline_visualization","index":` + fmt.Sprintf("%d", p.Index) + `}`), nil
	default:
		return nil, fmt.Errorf("invalid bem item presentation %q", p.Kind)
	}
}

// UnmarshalRolloutJSON decodes the canonical snake_case wire shape written by
// RolloutItem::RealtimeItem.
func (i *RealtimeItem) UnmarshalRolloutJSON(data []byte) error {
	if i == nil {
		return fmt.Errorf("realtime item is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("realtime item is required")
	}
	id, err := requiredRealtimeString(fields, "id")
	if err != nil {
		return err
	}
	sessionID, err := requiredRealtimeString(fields, "realtime_session_id")
	if err != nil {
		return err
	}
	observedAt, err := optionalRealtimeInt64(fields, "observed_at_ms")
	if err != nil {
		return err
	}
	content, err := decodeRealtimeRolloutContent(fields)
	if err != nil {
		return err
	}
	*i = RealtimeItem{ID: id, RealtimeSessionID: sessionID, ObservedAtMS: observedAt, Content: content}
	return nil
}

// decodeRealtimeRolloutContent reads the canonical tagged content variant from
// the flattened item fields.
func decodeRealtimeRolloutContent(fields map[string]json.RawMessage) (RealtimeItemContent, error) {
	kind, err := requiredRealtimeString(fields, "type")
	if err != nil {
		return RealtimeItemContent{}, err
	}
	switch kind {
	case "realtime_session_started":
		return RealtimeItemContent{Kind: RealtimeItemKindSessionStarted}, nil
	case "frame":
		return decodeFrameChannels(fields)
	case "transcript_segment":
		role, err := requiredRealtimeString(fields, "role")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		if !validTranscriptRole(RealtimeTranscriptRole(role)) {
			return RealtimeItemContent{}, fmt.Errorf("unknown realtime transcript role %q", role)
		}
		text, err := requiredRealtimeString(fields, "text")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		return RealtimeItemContent{
			Kind: RealtimeItemKindTranscriptSegment,
			Role: RealtimeTranscriptRole(role),
			Text: text,
		}, nil
	case "bem_item_promoted":
		turnID, err := requiredRealtimeString(fields, "turn_id")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		itemID, err := requiredRealtimeString(fields, "item_id")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		raw, ok := fields["presentation"]
		if !ok {
			return RealtimeItemContent{}, fmt.Errorf("missing field presentation")
		}
		presentation, err := decodeRolloutBemPresentation(raw)
		if err != nil {
			return RealtimeItemContent{}, err
		}
		return RealtimeItemContent{
			Kind:         RealtimeItemKindBemItemPromoted,
			TurnID:       turnID,
			ItemID:       itemID,
			Presentation: presentation,
		}, nil
	case "realtime_session_closed":
		outcome, err := requiredRealtimeString(fields, "outcome")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		if !validSessionOutcome(RealtimeSessionOutcome(outcome)) {
			return RealtimeItemContent{}, fmt.Errorf("unknown realtime session outcome %q", outcome)
		}
		return RealtimeItemContent{
			Kind:    RealtimeItemKindSessionClosed,
			Outcome: RealtimeSessionOutcome(outcome),
		}, nil
	case "turn_input_accepted":
		return decodeTurnInputAccepted(fields, "handoff_id", "turn_id", "source_event_ids", "input_item_id")
	case "session_end_input_saved":
		return decodeSessionEndInputSaved(fields, "source_event_ids", "input_item_id")
	default:
		return RealtimeItemContent{}, fmt.Errorf("unknown realtime item kind %q", kind)
	}
}

// decodeRolloutBemPresentation decodes the canonical presentation object.
func decodeRolloutBemPresentation(data []byte) (BemItemPresentation, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return BemItemPresentation{}, err
	}
	if fields == nil {
		return BemItemPresentation{}, fmt.Errorf("bem item presentation is required")
	}
	kind, err := requiredRealtimeString(fields, "type")
	if err != nil {
		return BemItemPresentation{}, err
	}
	switch kind {
	case "whole_item":
		return BemItemPresentation{Kind: BemPresentationWholeItem}, nil
	case "inline_markdown":
		return BemItemPresentation{Kind: BemPresentationInlineMarkdown}, nil
	case "inline_visualization":
		raw, ok := fields["index"]
		if !ok {
			return BemItemPresentation{}, fmt.Errorf("missing field index")
		}
		var presentation BemItemPresentation
		presentation.Kind = BemPresentationInlineVisualization
		if err := json.Unmarshal(raw, &presentation.Index); err != nil {
			return BemItemPresentation{}, fmt.Errorf("field index must be an unsigned integer: %w", err)
		}
		return presentation, nil
	default:
		return BemItemPresentation{}, fmt.Errorf("unknown bem item presentation %q", kind)
	}
}

// UnmarshalJSON decodes the flattened item and rejects unknown or missing
// fields, so a malformed timeline item fails closed.
func (i *RealtimeItem) UnmarshalJSON(data []byte) error {
	if i == nil {
		return fmt.Errorf("realtime item is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("realtime item is required")
	}
	for key := range fields {
		switch key {
		case "id", "realtimeSessionId", "observedAtMs", "type", "role", "text", "turnId", "itemId",
			"presentation", "outcome", "channels", "handoffId", "mode", "sourceEventIds",
			"inputItemId", "input":
		default:
			return fmt.Errorf("unknown realtime item field %q", key)
		}
	}
	id, err := requiredRealtimeString(fields, "id")
	if err != nil {
		return err
	}
	sessionID, err := requiredRealtimeString(fields, "realtimeSessionId")
	if err != nil {
		return err
	}
	observedAt, err := optionalRealtimeInt64(fields, "observedAtMs")
	if err != nil {
		return err
	}
	content, err := decodeRealtimeItemContent(fields)
	if err != nil {
		return err
	}
	*i = RealtimeItem{ID: id, RealtimeSessionID: sessionID, ObservedAtMS: observedAt, Content: content}
	return nil
}

// UnmarshalJSON decodes the tagged presentation object.
func (p *BemItemPresentation) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("bem item presentation is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("bem item presentation is required")
	}
	for key := range fields {
		switch key {
		case "type", "index":
		default:
			return fmt.Errorf("unknown bem item presentation field %q", key)
		}
	}
	kind, err := requiredRealtimeString(fields, "type")
	if err != nil {
		return err
	}
	presentation := BemItemPresentation{Kind: BemItemPresentationKind(kind)}
	switch presentation.Kind {
	case BemPresentationWholeItem, BemPresentationInlineMarkdown:
	case BemPresentationInlineVisualization:
		raw, ok := fields["index"]
		if !ok {
			return fmt.Errorf("missing field index")
		}
		if err := json.Unmarshal(raw, &presentation.Index); err != nil {
			return fmt.Errorf("field index must be an unsigned integer: %w", err)
		}
	default:
		return fmt.Errorf("unknown bem item presentation %q", kind)
	}
	*p = presentation
	return nil
}

// decodeRealtimeItemContent reads the tagged content variant from the flattened
// item fields.
func decodeRealtimeItemContent(fields map[string]json.RawMessage) (RealtimeItemContent, error) {
	kind, err := requiredRealtimeString(fields, "type")
	if err != nil {
		return RealtimeItemContent{}, err
	}
	content := RealtimeItemContent{Kind: RealtimeItemKind(kind)}
	switch content.Kind {
	case RealtimeItemKindSessionStarted:
		return content, nil
	case RealtimeItemKindFrame:
		return decodeFrameChannels(fields)
	case RealtimeItemKindTranscriptSegment:
		role, err := requiredRealtimeString(fields, "role")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		content.Role = RealtimeTranscriptRole(role)
		if !validTranscriptRole(content.Role) {
			return RealtimeItemContent{}, fmt.Errorf("unknown realtime transcript role %q", role)
		}
		text, err := requiredRealtimeString(fields, "text")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		content.Text = text
		return content, nil
	case RealtimeItemKindBemItemPromoted:
		turnID, err := requiredRealtimeString(fields, "turnId")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		itemID, err := requiredRealtimeString(fields, "itemId")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		raw, ok := fields["presentation"]
		if !ok {
			return RealtimeItemContent{}, fmt.Errorf("missing field presentation")
		}
		var presentation BemItemPresentation
		if err := json.Unmarshal(raw, &presentation); err != nil {
			return RealtimeItemContent{}, err
		}
		content.TurnID = turnID
		content.ItemID = itemID
		content.Presentation = presentation
		return content, nil
	case RealtimeItemKindSessionClosed:
		outcome, err := requiredRealtimeString(fields, "outcome")
		if err != nil {
			return RealtimeItemContent{}, err
		}
		content.Outcome = RealtimeSessionOutcome(outcome)
		if !validSessionOutcome(content.Outcome) {
			return RealtimeItemContent{}, fmt.Errorf("unknown realtime session outcome %q", outcome)
		}
		return content, nil
	case RealtimeItemKindTurnInputAccepted:
		return decodeTurnInputAccepted(fields, "handoffId", "turnId", "sourceEventIds", "inputItemId")
	case RealtimeItemKindSessionEndInput:
		return decodeSessionEndInputSaved(fields, "sourceEventIds", "inputItemId")
	default:
		return RealtimeItemContent{}, fmt.Errorf("unknown realtime item kind %q", kind)
	}
}

// decodeFrameChannels reads V1's sparse channel envelope.
func decodeFrameChannels(fields map[string]json.RawMessage) (RealtimeItemContent, error) {
	raw, ok := fields["channels"]
	if !ok {
		return RealtimeItemContent{}, fmt.Errorf("missing field channels")
	}
	var channels map[string]json.RawMessage
	if err := json.Unmarshal(raw, &channels); err != nil {
		return RealtimeItemContent{}, fmt.Errorf("field channels must be an object: %w", err)
	}
	transcript, ok := channels["voice_transcript"]
	if !ok {
		return RealtimeItemContent{}, fmt.Errorf("frame channels require voice_transcript")
	}
	var voice map[string]json.RawMessage
	if err := json.Unmarshal(transcript, &voice); err != nil {
		return RealtimeItemContent{}, fmt.Errorf("field voice_transcript must be an object: %w", err)
	}
	role, err := requiredRealtimeString(voice, "role")
	if err != nil {
		return RealtimeItemContent{}, err
	}
	if !validTranscriptRole(RealtimeTranscriptRole(role)) {
		return RealtimeItemContent{}, fmt.Errorf("unknown realtime transcript role %q", role)
	}
	text, err := requiredRealtimeString(voice, "text")
	if err != nil {
		return RealtimeItemContent{}, err
	}
	return RealtimeItemContent{
		Kind: RealtimeItemKindFrame,
		Role: RealtimeTranscriptRole(role),
		Text: text,
	}, nil
}

func decodeTurnInputAccepted(fields map[string]json.RawMessage, handoffKey, turnKey, sourcesKey, inputItemKey string) (RealtimeItemContent, error) {
	handoffID, err := requiredRealtimeString(fields, handoffKey)
	if err != nil {
		return RealtimeItemContent{}, err
	}
	turnID, err := requiredRealtimeString(fields, turnKey)
	if err != nil {
		return RealtimeItemContent{}, err
	}
	mode, err := decodeHandoffMode(fields)
	if err != nil {
		return RealtimeItemContent{}, err
	}
	sources, err := requiredRealtimeStrings(fields, sourcesKey)
	if err != nil {
		return RealtimeItemContent{}, err
	}
	inputItemID, err := requiredRealtimeString(fields, inputItemKey)
	if err != nil {
		return RealtimeItemContent{}, err
	}
	input, err := requiredRealtimeString(fields, "input")
	if err != nil {
		return RealtimeItemContent{}, err
	}
	return RealtimeItemContent{
		Kind:         RealtimeItemKindTurnInputAccepted,
		HandoffID:    handoffID,
		TurnID:       turnID,
		HandoffMode:  mode,
		SourceEvents: sources,
		InputItemID:  inputItemID,
		Input:        input,
	}, nil
}

func decodeSessionEndInputSaved(fields map[string]json.RawMessage, sourcesKey, inputItemKey string) (RealtimeItemContent, error) {
	sources, err := requiredRealtimeStrings(fields, sourcesKey)
	if err != nil {
		return RealtimeItemContent{}, err
	}
	inputItemID, err := requiredRealtimeString(fields, inputItemKey)
	if err != nil {
		return RealtimeItemContent{}, err
	}
	input, err := requiredRealtimeString(fields, "input")
	if err != nil {
		return RealtimeItemContent{}, err
	}
	return RealtimeItemContent{
		Kind:         RealtimeItemKindSessionEndInput,
		SourceEvents: sources,
		InputItemID:  inputItemID,
		Input:        input,
	}, nil
}

func decodeHandoffMode(fields map[string]json.RawMessage) (RealtimeHandoffMode, error) {
	value, err := requiredRealtimeString(fields, "mode")
	if err != nil {
		return "", err
	}
	mode := RealtimeHandoffMode(value)
	switch mode {
	case RealtimeHandoffModeStart, RealtimeHandoffModeSteer:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown realtime handoff mode %q", value)
	}
}

func requiredRealtimeStrings(fields map[string]json.RawMessage, name string) ([]string, error) {
	raw, ok := fields[name]
	if !ok {
		return nil, fmt.Errorf("missing field %s", name)
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("field %s must be a string array: %w", name, err)
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

func optionalRealtimeInt64(fields map[string]json.RawMessage, name string) (*int64, error) {
	raw, ok := fields[name]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("field %s must be an integer: %w", name, err)
	}
	return &value, nil
}

// requiredRealtimeString reads a required string field.
func requiredRealtimeString(fields map[string]json.RawMessage, name string) (string, error) {
	raw, ok := fields[name]
	if !ok {
		return "", fmt.Errorf("missing field %s", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("field %s must be a string: %w", name, err)
	}
	return value, nil
}

func validTranscriptRole(role RealtimeTranscriptRole) bool {
	return role == RealtimeTranscriptRoleUser || role == RealtimeTranscriptRoleAssistant
}

func validSessionOutcome(outcome RealtimeSessionOutcome) bool {
	return outcome == RealtimeSessionOutcomeEnded || outcome == RealtimeSessionOutcomeFailed
}

// RealtimeEventOrder selects when the emitted items sit relative to the source
// core event they were derived from.
type RealtimeEventOrder int

const (
	// RealtimeOrderAfterEvent publishes the items after the source event.
	RealtimeOrderAfterEvent RealtimeEventOrder = iota
	// RealtimeOrderBeforeEvent publishes the items before the source event.
	RealtimeOrderBeforeEvent
)

// RealtimeTranscriptStream is the live transcript tail attached to one stream
// event: an optional started item plus the delta that produced it.
type RealtimeTranscriptStream struct {
	StartedItem *RealtimeItem
	ItemID      string
	Delta       string
}

// RealtimeEventEffects is the reducer output for one observed event.
type RealtimeEventEffects struct {
	// SealFrames reports that durable frame history must reach disk before a
	// completion notification acknowledges it (Rust RealtimeEventEffects).
	SealFrames       bool
	Order            RealtimeEventOrder
	Items            []RealtimeItem
	TranscriptStream *RealtimeTranscriptStream
}

// Empty reports whether the effects carry nothing to publish.
func (e RealtimeEventEffects) Empty() bool {
	return len(e.Items) == 0 && e.TranscriptStream == nil
}

// observationClock assigns first-observed timestamps for one thread owner.
// Wall-clock rollback never moves a later observation backward.
type observationClock struct {
	lastAssignedMS *int64
}

func (c *observationClock) observe(wallClockMS int64) int64 {
	if c.lastAssignedMS == nil || wallClockMS > *c.lastAssignedMS {
		value := wallClockMS
		c.lastAssignedMS = &value
	}
	return *c.lastAssignedMS
}

func (c *observationClock) floorMS() *int64 {
	if c.lastAssignedMS == nil {
		return nil
	}
	value := *c.lastAssignedMS
	return &value
}

func (c *observationClock) restoreFloor(ms *int64) {
	if ms != nil {
		c.observe(*ms)
	}
}

// SetFramesEnabled resolves this thread's frame enrollment. Enrollment is
// cached after the first call, matching Rust's first-successful-lookup rule.
func (s *RealtimeHistoryState) SetFramesEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.framesKnown {
		return
	}
	s.frames = enabled
	s.framesKnown = true
}

// RestoreObservationClockFloor restores the non-decreasing clock floor from a
// rollout or compaction checkpoint.
func (s *RealtimeHistoryState) RestoreObservationClockFloor(floorMS *int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observationClock.restoreFloor(floorMS)
}

// ObservationClockFloor reports the current floor for a compaction checkpoint.
func (s *RealtimeHistoryState) ObservationClockFloor() *int64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observationClock.floorMS()
}

// SetObservationClock overrides the wall clock for tests.
func (s *RealtimeHistoryState) SetObservationClock(now func() int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nowMS = now
}

// framesEnabled reports the cached enrollment. Callers hold s.mu.
func (s *RealtimeHistoryState) framesEnabled() bool {
	return s.framesKnown && s.frames
}

// observeNow assigns the next observation timestamp. Callers hold s.mu.
func (s *RealtimeHistoryState) observeNow() int64 {
	now := time.Now().UnixMilli
	if s.nowMS != nil {
		now = s.nowMS
	}
	return s.observationClock.observe(now())
}

type activeSegment struct {
	sessionID    string
	id           string
	role         RealtimeTranscriptRole
	text         string
	observedAtMS *int64
}

type activeTranscriptSegments struct {
	user            *activeSegment
	assistant       *activeSegment
	firstActiveRole RealtimeTranscriptRole
}

func (s *activeTranscriptSegments) slot(role RealtimeTranscriptRole) **activeSegment {
	if role == RealtimeTranscriptRoleAssistant {
		return &s.assistant
	}
	return &s.user
}

func (s *activeTranscriptSegments) take(role RealtimeTranscriptRole) *activeSegment {
	slot := s.slot(role)
	segment := *slot
	*slot = nil
	if s.firstActiveRole == role {
		switch {
		case s.user != nil:
			s.firstActiveRole = s.user.role
		case s.assistant != nil:
			s.firstActiveRole = s.assistant.role
		default:
			s.firstActiveRole = ""
		}
	}
	return segment
}

type streamingAgentMessage struct {
	itemID string
	text   string
}

// RealtimeHistoryState retains only live session state. Durable history is
// served by the rollout index.
type RealtimeHistoryState struct {
	mu sync.Mutex

	// observationClock assigns first-observed timestamps that never move
	// backward for this thread owner (Rust ObservationClock).
	observationClock observationClock
	// frames is resolved once at first use. Unenrolled threads keep the legacy
	// transcript_segment spelling without observed timestamps.
	frames      bool
	framesKnown bool
	nowMS       func() int64

	activeSessionID string
	segments        activeTranscriptSegments
	streamingAgent  *streamingAgentMessage
	activeTurnID    string
	sessionByTurn   map[string]string
	promotedKeys    map[string]bool
	pendingHandoffs []string
	failed          bool
	newID           func() string
}

// SetIDGenerator overrides item identifier generation, primarily for tests.
func (s *RealtimeHistoryState) SetIDGenerator(generator func() string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.newID = generator
}

func (s *RealtimeHistoryState) nextID() string {
	if s != nil && s.newID != nil {
		return s.newID()
	}
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

func (s *RealtimeHistoryState) sessionByTurnLocked() map[string]string {
	if s.sessionByTurn == nil {
		s.sessionByTurn = map[string]string{}
	}
	return s.sessionByTurn
}

func (s *RealtimeHistoryState) promotedKeysLocked() map[string]bool {
	if s.promotedKeys == nil {
		s.promotedKeys = map[string]bool{}
	}
	return s.promotedKeys
}

// ActiveSessionID reports the session currently recorded in the timeline.
func (s *RealtimeHistoryState) ActiveSessionID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeSessionID
}

// StartSession opens a new canonical session, sealing any previous segments.
func (s *RealtimeHistoryState) StartSession(sessionID string) RealtimeEventEffects {
	if s == nil || sessionID == "" {
		return RealtimeEventEffects{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeSessionID == sessionID {
		return RealtimeEventEffects{}
	}
	var items []RealtimeItem
	s.sealSegments(&items, false)
	s.activeSessionID = sessionID
	s.failed = false
	var observedAt *int64
	itemID := ""
	if s.framesEnabled() {
		// The call ID lets a reader seek directly to its opening position.
		itemID = "realtime-session-start:" + sessionID
		value := s.observeNow()
		observedAt = &value
	} else {
		itemID = s.nextID()
	}
	items = append(items, RealtimeItem{
		ID:                itemID,
		RealtimeSessionID: sessionID,
		ObservedAtMS:      observedAt,
		Content:           RealtimeItemContent{Kind: RealtimeItemKindSessionStarted},
	})
	if s.activeTurnID != "" {
		s.sessionByTurnLocked()[s.activeTurnID] = sessionID
	}
	return RealtimeEventEffects{SealFrames: s.framesEnabled(), Order: RealtimeOrderAfterEvent, Items: items}
}

// BindTurnSession records which session produced the given turn, mirroring the
// Rust turn-started binding that also consumes a pending handoff.
func (s *RealtimeHistoryState) BindTurnSession(turnID string) {
	if s == nil || turnID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID := ""
	if len(s.pendingHandoffs) > 0 {
		sessionID = s.pendingHandoffs[0]
		s.pendingHandoffs = s.pendingHandoffs[1:]
	} else {
		sessionID = s.activeSessionID
	}
	if sessionID == "" {
		return
	}
	turns := s.sessionByTurnLocked()
	if _, exists := turns[turnID]; !exists {
		turns[turnID] = sessionID
	}
}

// SetActiveTurn records the turn that a subsequent session start belongs to.
func (s *RealtimeHistoryState) SetActiveTurn(turnID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeTurnID = turnID
}

// ClearActiveTurn clears the recorded turn when it completes or aborts.
func (s *RealtimeHistoryState) ClearActiveTurn(turnID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if turnID == "" || s.activeTurnID == turnID {
		s.activeTurnID = ""
	}
}

// NoteHandoffRequested queues the active session for the next turn.
func (s *RealtimeHistoryState) NoteHandoffRequested() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeSessionID == "" {
		return
	}
	s.pendingHandoffs = append(s.pendingHandoffs, s.activeSessionID)
}

// MarkFailed records that the session produced an error, which decides the
// outcome of the closing item.
func (s *RealtimeHistoryState) MarkFailed() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = true
}

// TranscriptDelta appends live transcript text and reports the stream tail.
func (s *RealtimeHistoryState) TranscriptDelta(role RealtimeTranscriptRole, delta string) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return RealtimeEventEffects{SealFrames: s.framesEnabled(), TranscriptStream: s.addDelta(role, delta)}
}

// TranscriptDone seals the active segment for the role and reports any items.
func (s *RealtimeHistoryState) TranscriptDone(role RealtimeTranscriptRole, text string) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []RealtimeItem
	stream := s.finishSegment(&items, role, text)
	return RealtimeEventEffects{SealFrames: s.framesEnabled(), Items: items, TranscriptStream: stream}
}

// SessionClosed seals the session and appends the closing item.
func (s *RealtimeHistoryState) SessionClosed() RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeSessionID == "" {
		return RealtimeEventEffects{}
	}
	sessionID := s.activeSessionID
	s.activeSessionID = ""
	var items []RealtimeItem
	s.sealSegments(&items, false)
	outcome := RealtimeSessionOutcomeEnded
	if s.failed {
		outcome = RealtimeSessionOutcomeFailed
	}
	s.failed = false
	var observedAt *int64
	if s.framesEnabled() {
		value := s.observeNow()
		observedAt = &value
	}
	items = append(items, RealtimeItem{
		ID:                s.nextID(),
		RealtimeSessionID: sessionID,
		ObservedAtMS:      observedAt,
		Content:           RealtimeItemContent{Kind: RealtimeItemKindSessionClosed, Outcome: outcome},
	})
	return RealtimeEventEffects{SealFrames: s.framesEnabled(), Order: RealtimeOrderAfterEvent, Items: items}
}

// ObserveAgentItem records a completed or started backing agent item and
// reports any promotions it triggers.
func (s *RealtimeHistoryState) ObserveAgentItem(turnID, itemID, text string, completed bool) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !completed {
		s.streamingAgent = &streamingAgentMessage{itemID: itemID, text: text}
	}
	var items []RealtimeItem
	s.observeAssistantMessage(&items, turnID, itemID, text)
	return RealtimeEventEffects{SealFrames: s.framesEnabled(), Items: items}
}

// StreamAgentMessageDelta accumulates streaming agent text and re-evaluates
// promotions, matching the Rust content-delta observation.
func (s *RealtimeHistoryState) StreamAgentMessageDelta(turnID, itemID, delta string) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streamingAgent == nil || s.streamingAgent.itemID != itemID {
		s.streamingAgent = &streamingAgentMessage{itemID: itemID}
	}
	s.streamingAgent.text += delta
	var items []RealtimeItem
	s.observeAssistantMessage(&items, turnID, itemID, s.streamingAgent.text)
	return RealtimeEventEffects{SealFrames: s.framesEnabled(), Items: items}
}

// FinishStreamingAgentMessage clears the streaming buffer when its item
// completes.
func (s *RealtimeHistoryState) FinishStreamingAgentMessage(itemID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streamingAgent == nil {
		return
	}
	if s.streamingAgent.itemID == itemID {
		s.streamingAgent = nil
	}
}

// PromoteAgentItem adds a promotion for a backing agent item. Whole-item
// promotions are also used by image generation, sub-agent, and tool-call
// completions.
func (s *RealtimeHistoryState) PromoteAgentItem(turnID, itemID string, presentation BemItemPresentation) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []RealtimeItem
	s.addPromotion(&items, turnID, itemID, presentation)
	return RealtimeEventEffects{SealFrames: s.framesEnabled(), Items: items}
}

// SealUserInput seals the live segments before a new user message enters the
// timeline.
func (s *RealtimeHistoryState) SealUserInput() RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.shouldSealUserInputLocked() {
		return RealtimeEventEffects{}
	}
	var items []RealtimeItem
	s.sealSegments(&items, true)
	return RealtimeEventEffects{SealFrames: s.framesEnabled(), Order: RealtimeOrderBeforeEvent, Items: items}
}

// SealUserInputForPrompt seals the live segments before a typed user turn
// enters the timeline, skipping realtime handoff delegations the way Rust's
// `should_seal_user_input` does.
func (s *RealtimeHistoryState) SealUserInputForPrompt(prompt string) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.shouldSealUserInputLocked() {
		return RealtimeEventEffects{}
	}
	trimmed := strings.TrimSpace(prompt)
	if strings.HasPrefix(trimmed, "<realtime_delegation>") && strings.HasSuffix(trimmed, "</realtime_delegation>") {
		return RealtimeEventEffects{}
	}
	var items []RealtimeItem
	s.sealSegments(&items, true)
	return RealtimeEventEffects{SealFrames: s.framesEnabled(), Order: RealtimeOrderBeforeEvent, Items: items}
}

// shouldSealUserInputLocked reports whether live segments need sealing. Callers
// must hold s.mu.
func (s *RealtimeHistoryState) shouldSealUserInputLocked() bool {
	if s.activeSessionID == "" {
		return false
	}
	for _, segment := range []*activeSegment{s.segments.user, s.segments.assistant} {
		if segment != nil && segment.text != "" {
			return true
		}
	}
	return false
}

func (s *RealtimeHistoryState) addDelta(role RealtimeTranscriptRole, delta string) *RealtimeTranscriptStream {
	if !validTranscriptRole(role) {
		return nil
	}
	sessionID := s.activeSessionID
	if sessionID == "" {
		return nil
	}
	if s.segments.firstActiveRole == "" {
		s.segments.firstActiveRole = role
	}
	slot := s.segments.slot(role)
	if *slot == nil {
		*slot = &activeSegment{sessionID: sessionID, id: s.nextID(), role: role}
	}
	segment := *slot
	frames := s.framesEnabled()
	var observedAt *int64
	if frames {
		if segment.observedAtMS == nil {
			value := s.observeNow()
			segment.observedAtMS = &value
		}
		observedAt = segment.observedAtMS
	}
	var startedItem *RealtimeItem
	if segment.text == "" {
		item := RealtimeItem{
			ID:                segment.id,
			RealtimeSessionID: segment.sessionID,
			ObservedAtMS:      observedAt,
			Content:           speechContent(frames, segment.role, ""),
		}
		startedItem = &item
	}
	segment.text += delta
	return &RealtimeTranscriptStream{StartedItem: startedItem, ItemID: segment.id, Delta: delta}
}

func (s *RealtimeHistoryState) finishSegment(items *[]RealtimeItem, role RealtimeTranscriptRole, text string) *RealtimeTranscriptStream {
	if !validTranscriptRole(role) {
		return nil
	}
	if segment := s.segments.take(role); segment != nil {
		// A split leaves an empty continuation; the upstream final may repeat
		// text that was already committed before the split.
		s.sealActiveSegment(items, segment, false)
		return nil
	}
	if text == "" {
		return nil
	}
	stream := s.addDelta(role, text)
	if segment := s.segments.take(role); segment != nil {
		s.sealActiveSegment(items, segment, false)
	}
	return stream
}

func (s *RealtimeHistoryState) sealSegments(items *[]RealtimeItem, continuation bool) {
	segments := s.segments
	s.segments = activeTranscriptSegments{}
	roles := []RealtimeTranscriptRole{RealtimeTranscriptRoleUser, RealtimeTranscriptRoleAssistant}
	if segments.firstActiveRole == RealtimeTranscriptRoleAssistant {
		roles = []RealtimeTranscriptRole{RealtimeTranscriptRoleAssistant, RealtimeTranscriptRoleUser}
	}
	for _, role := range roles {
		if segment := segments.take(role); segment != nil {
			s.sealActiveSegment(items, segment, continuation)
		}
	}
}

func (s *RealtimeHistoryState) sealActiveSegment(items *[]RealtimeItem, segment *activeSegment, continuation bool) {
	if segment == nil {
		return
	}
	if segment.text != "" {
		*items = append(*items, RealtimeItem{
			ID:                segment.id,
			RealtimeSessionID: segment.sessionID,
			ObservedAtMS:      segment.observedAtMS,
			Content:           speechContent(s.framesEnabled(), segment.role, segment.text),
		})
	}
	if continuation {
		if s.segments.firstActiveRole == "" {
			s.segments.firstActiveRole = segment.role
		}
		*s.segments.slot(segment.role) = &activeSegment{
			sessionID: segment.sessionID,
			id:        s.nextID(),
			role:      segment.role,
		}
	}
}

// speechContent selects the canonical spelling for one voice transcript.
// Enrolled threads write V1 frames; legacy and unenrolled writers keep the
// original transcript_segment variant.
func speechContent(frames bool, role RealtimeTranscriptRole, text string) RealtimeItemContent {
	if frames {
		return RealtimeItemContent{Kind: RealtimeItemKindFrame, Role: role, Text: text}
	}
	return RealtimeItemContent{Kind: RealtimeItemKindTranscriptSegment, Role: role, Text: text}
}

func (s *RealtimeHistoryState) addPromotion(items *[]RealtimeItem, turnID, itemID string, presentation BemItemPresentation) {
	sessionID, ok := s.sessionByTurnLocked()[turnID]
	if !ok || sessionID == "" {
		return
	}
	key := promotionKey(itemID, presentation)
	if s.promotedKeysLocked()[key] {
		return
	}
	s.promotedKeysLocked()[key] = true
	s.sealSegments(items, true)
	var observedAt *int64
	if s.framesEnabled() {
		value := s.observeNow()
		observedAt = &value
	}
	*items = append(*items, RealtimeItem{
		ID:                s.nextID(),
		RealtimeSessionID: sessionID,
		ObservedAtMS:      observedAt,
		Content: RealtimeItemContent{
			Kind:         RealtimeItemKindBemItemPromoted,
			TurnID:       turnID,
			ItemID:       itemID,
			Presentation: presentation,
		},
	})
}

func promotionKey(itemID string, presentation BemItemPresentation) string {
	switch presentation.Kind {
	case BemPresentationWholeItem:
		return itemID + ":whole-item"
	case BemPresentationInlineMarkdown:
		return itemID + ":inline-markdown"
	case BemPresentationInlineVisualization:
		return itemID + ":inline-visualization:" + fmt.Sprintf("%d", presentation.Index)
	default:
		return itemID + ":" + string(presentation.Kind)
	}
}

const (
	inlineMarkdownDirective      = "::codex-realtime-inline{}"
	inlineVisualizationDirective = "::codex-inline-vis{"
	backtickFence                = "```"
	tildeFence                   = "~~~"
)

// visualizeDirective is U+E200 "visualize" U+E202 "{".
var visualizeDirective = "\ue200visualize\ue202{"

func (s *RealtimeHistoryState) observeAssistantMessage(items *[]RealtimeItem, turnID, itemID, text string) {
	if s.sessionByTurnLocked()[turnID] == "" {
		return
	}
	lines := rustLines(trimStart(text))
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	}
	if strings.HasPrefix(first, "[") {
		if _, content, found := strings.Cut(first, "]"); found {
			first = trimStart(content)
			if first == "" && len(lines) > 1 {
				first = lines[1]
			}
		}
	}
	if first == inlineMarkdownDirective && strings.Contains(text, "\n") {
		s.addPromotion(items, turnID, itemID, BemItemPresentation{Kind: BemPresentationInlineMarkdown})
		return
	}

	inFence := false
	var visualizationIndex uint32
	for _, line := range rustLines(text) {
		trimmed := trimStart(line)
		if strings.HasPrefix(trimmed, backtickFence) || strings.HasPrefix(trimmed, tildeFence) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, inlineVisualizationDirective) || strings.HasPrefix(trimmed, visualizeDirective) {
			s.addPromotion(items, turnID, itemID, BemItemPresentation{
				Kind:  BemPresentationInlineVisualization,
				Index: visualizationIndex,
			})
			visualizationIndex++
		}
	}
}

// trimStart removes leading Unicode whitespace, matching Rust trim_start.
func trimStart(text string) string {
	return strings.TrimLeftFunc(text, unicode.IsSpace)
}

// rustLines splits like Rust str::lines: it drops the final empty element and
// strips a trailing carriage return from each line.
func rustLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for index, line := range lines {
		lines[index] = strings.TrimSuffix(line, "\r")
	}
	return lines
}
