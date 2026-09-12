package realtime

// Canonical Voice history for every host. The reducer owns the presentation
// rules that decide when a transcript segment, a promoted agent item, or a
// session boundary enters the thread timeline, so the app-server item
// notifications and the persisted rollout agree with the Rust reducer.

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// RealtimeItem is a thread-scoped realtime item in the canonical timeline. The
// wire shape flattens the tagged content into the item, matching the Rust
// app-server v2 item.
type RealtimeItem struct {
	ID                string
	RealtimeSessionID string
	Content           RealtimeItemContent
}

// RealtimeItemKind is the flattened content tag.
type RealtimeItemKind string

const (
	RealtimeItemKindSessionStarted    RealtimeItemKind = "realtimeSessionStarted"
	RealtimeItemKindTranscriptSegment RealtimeItemKind = "transcriptSegment"
	RealtimeItemKindBemItemPromoted   RealtimeItemKind = "bemItemPromoted"
	RealtimeItemKindSessionClosed     RealtimeItemKind = "realtimeSessionClosed"
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
}

// MarshalJSON emits the Rust wire shape for the selected kind.
func (c RealtimeItemContent) MarshalJSON() ([]byte, error) {
	switch c.Kind {
	case RealtimeItemKindSessionStarted:
		return []byte(`{"type":"realtimeSessionStarted"}`), nil
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
		case "id", "realtimeSessionId", "type", "role", "text", "turnId", "itemId", "presentation", "outcome":
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
	content, err := decodeRealtimeItemContent(fields)
	if err != nil {
		return err
	}
	*i = RealtimeItem{ID: id, RealtimeSessionID: sessionID, Content: content}
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
	default:
		return RealtimeItemContent{}, fmt.Errorf("unknown realtime item kind %q", kind)
	}
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
	Order            RealtimeEventOrder
	Items            []RealtimeItem
	TranscriptStream *RealtimeTranscriptStream
}

// Empty reports whether the effects carry nothing to publish.
func (e RealtimeEventEffects) Empty() bool {
	return len(e.Items) == 0 && e.TranscriptStream == nil
}

type activeSegment struct {
	sessionID string
	id        string
	role      RealtimeTranscriptRole
	text      string
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
	return s.activeSessionID
}

// StartSession opens a new canonical session, sealing any previous segments.
func (s *RealtimeHistoryState) StartSession(sessionID string) RealtimeEventEffects {
	if s == nil || sessionID == "" || s.activeSessionID == sessionID {
		return RealtimeEventEffects{}
	}
	var items []RealtimeItem
	s.sealSegments(&items, false)
	s.activeSessionID = sessionID
	s.failed = false
	items = append(items, RealtimeItem{
		ID:                s.nextID(),
		RealtimeSessionID: sessionID,
		Content:           RealtimeItemContent{Kind: RealtimeItemKindSessionStarted},
	})
	if s.activeTurnID != "" {
		s.sessionByTurnLocked()[s.activeTurnID] = sessionID
	}
	return RealtimeEventEffects{Order: RealtimeOrderAfterEvent, Items: items}
}

// BindTurnSession records which session produced the given turn, mirroring the
// Rust turn-started binding that also consumes a pending handoff.
func (s *RealtimeHistoryState) BindTurnSession(turnID string) {
	if s == nil || turnID == "" {
		return
	}
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
	s.activeTurnID = turnID
}

// ClearActiveTurn clears the recorded turn when it completes or aborts.
func (s *RealtimeHistoryState) ClearActiveTurn(turnID string) {
	if s == nil {
		return
	}
	if turnID == "" || s.activeTurnID == turnID {
		s.activeTurnID = ""
	}
}

// NoteHandoffRequested queues the active session for the next turn.
func (s *RealtimeHistoryState) NoteHandoffRequested() {
	if s == nil || s.activeSessionID == "" {
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
	s.failed = true
}

// TranscriptDelta appends live transcript text and reports the stream tail.
func (s *RealtimeHistoryState) TranscriptDelta(role RealtimeTranscriptRole, delta string) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	return RealtimeEventEffects{TranscriptStream: s.addDelta(role, delta)}
}

// TranscriptDone seals the active segment for the role and reports any items.
func (s *RealtimeHistoryState) TranscriptDone(role RealtimeTranscriptRole, text string) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	var items []RealtimeItem
	stream := s.finishSegment(&items, role, text)
	return RealtimeEventEffects{Items: items, TranscriptStream: stream}
}

// SessionClosed seals the session and appends the closing item.
func (s *RealtimeHistoryState) SessionClosed() RealtimeEventEffects {
	if s == nil || s.activeSessionID == "" {
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
	items = append(items, RealtimeItem{
		ID:                s.nextID(),
		RealtimeSessionID: sessionID,
		Content:           RealtimeItemContent{Kind: RealtimeItemKindSessionClosed, Outcome: outcome},
	})
	return RealtimeEventEffects{Order: RealtimeOrderAfterEvent, Items: items}
}

// ObserveAgentItem records a completed or started backing agent item and
// reports any promotions it triggers.
func (s *RealtimeHistoryState) ObserveAgentItem(turnID, itemID, text string, completed bool) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	if !completed {
		s.streamingAgent = &streamingAgentMessage{itemID: itemID, text: text}
	}
	var items []RealtimeItem
	s.observeAssistantMessage(&items, turnID, itemID, text)
	return RealtimeEventEffects{Items: items}
}

// StreamAgentMessageDelta accumulates streaming agent text and re-evaluates
// promotions, matching the Rust content-delta observation.
func (s *RealtimeHistoryState) StreamAgentMessageDelta(turnID, itemID, delta string) RealtimeEventEffects {
	if s == nil {
		return RealtimeEventEffects{}
	}
	if s.streamingAgent == nil || s.streamingAgent.itemID != itemID {
		s.streamingAgent = &streamingAgentMessage{itemID: itemID}
	}
	s.streamingAgent.text += delta
	var items []RealtimeItem
	s.observeAssistantMessage(&items, turnID, itemID, s.streamingAgent.text)
	return RealtimeEventEffects{Items: items}
}

// FinishStreamingAgentMessage clears the streaming buffer when its item
// completes.
func (s *RealtimeHistoryState) FinishStreamingAgentMessage(itemID string) {
	if s == nil || s.streamingAgent == nil {
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
	var items []RealtimeItem
	s.addPromotion(&items, turnID, itemID, presentation)
	return RealtimeEventEffects{Items: items}
}

// SealUserInput seals the live segments before a new user message enters the
// timeline.
func (s *RealtimeHistoryState) SealUserInput() RealtimeEventEffects {
	if s == nil || !s.shouldSealUserInput() {
		return RealtimeEventEffects{}
	}
	var items []RealtimeItem
	s.sealSegments(&items, true)
	return RealtimeEventEffects{Order: RealtimeOrderBeforeEvent, Items: items}
}

func (s *RealtimeHistoryState) shouldSealUserInput() bool {
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
	var startedItem *RealtimeItem
	if segment.text == "" {
		item := RealtimeItem{
			ID:                segment.id,
			RealtimeSessionID: segment.sessionID,
			Content: RealtimeItemContent{
				Kind: RealtimeItemKindTranscriptSegment,
				Role: segment.role,
			},
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
			Content: RealtimeItemContent{
				Kind: RealtimeItemKindTranscriptSegment,
				Role: segment.role,
				Text: segment.text,
			},
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
	*items = append(*items, RealtimeItem{
		ID:                s.nextID(),
		RealtimeSessionID: sessionID,
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
