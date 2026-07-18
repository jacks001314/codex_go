package rollout

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"codex_go/session"
)

func ItemFromSessionItem(item *session.Item) *Item {
	if item == nil {
		return nil
	}
	out := &Item{
		ID:         item.ID,
		Type:       item.Type,
		Role:       item.Role,
		Text:       item.Text,
		Name:       item.Name,
		CallID:     item.CallID,
		Data:       cloneAnyMap(item.Data),
		Raw:        append(json.RawMessage(nil), item.Raw...),
		ResponseID: item.ResponseID,
		Metadata:   cloneAnyMap(item.Metadata),
	}
	if len(item.Content) > 0 {
		out.Content = make([]ContentPart, len(item.Content))
		for i := range item.Content {
			out.Content[i] = ContentPart{
				Type:     item.Content[i].Type,
				Text:     item.Content[i].Text,
				ImageURL: item.Content[i].ImageURL,
				Detail:   cloneStringPtr(item.Content[i].Detail),
			}
		}
	}
	return out
}

func AppendSessionItems(recorder *Recorder, items []session.Item, now time.Time) error {
	if recorder == nil {
		return nil
	}
	for i := range items {
		item := ItemFromSessionItem(&items[i])
		if item == nil {
			continue
		}
		line, err := LineFromItem(item, itemTime(items[i].CreatedAt, now))
		if err != nil {
			return err
		}
		if err := recorder.AppendLine(*line); err != nil {
			return err
		}
	}
	return nil
}

func RecordFromPath(path string, archived bool) (*session.Record, error) {
	lines, _, err := Load(path)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, errEmptyRollout(path)
	}
	var firstMeta *SessionMeta
	var meta *SessionMeta
	for i := range lines {
		if lines[i].Meta != nil {
			if firstMeta == nil {
				firstMeta = lines[i].Meta
			}
			meta = lines[i].Meta
		}
	}
	if meta == nil {
		return nil, errMissingSessionMeta(path)
	}
	createdAt := parseMetaTimestamp(firstMeta.Timestamp)
	if createdAt.IsZero() {
		createdAt = parseMetaTimestamp(meta.Timestamp)
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	items, rolloutTurns := sessionItemsFromRolloutLines(lines, createdAt)
	updatedAt := createdAt
	recencyAt := createdAt
	if info, statErr := os.Stat(path); statErr == nil {
		updatedAt = info.ModTime().UTC()
		recencyAt = updatedAt
	}
	if len(items) > 0 && !items[len(items)-1].CreatedAt.IsZero() {
		recencyAt = items[len(items)-1].CreatedAt
	}
	record := &session.Record{
		ID:             session.ThreadID(meta.ID),
		SessionID:      firstNonEmptyString(meta.SessionID, meta.ID),
		ForkedFromID:   session.ThreadID(meta.ForkedFromID),
		ParentThreadID: session.ThreadID(meta.ParentThreadID),
		Preview:        previewFromSessionItems(items),
		Archived:       archived,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		RecencyAt:      recencyAt,
		Metadata: session.Metadata{
			CWD:                     meta.CWD,
			Model:                   meta.Model,
			ModelProvider:           meta.ModelProvider,
			Source:                  meta.Source,
			ThreadSource:            meta.ThreadSource,
			Originator:              meta.Originator,
			HistoryMode:             meta.HistoryMode,
			MemoryMode:              meta.MemoryMode,
			BaseInstructions:        meta.BaseInstructions,
			SessionPrefix:           meta.SessionPrefix,
			CLIVersion:              meta.CLIVersion,
			AgentNickname:           meta.AgentNickname,
			AgentRole:               meta.AgentRole,
			AgentPath:               meta.AgentPath,
			DynamicTools:            cloneRawMessages(meta.DynamicTools),
			SelectedCapabilityRoots: cloneRawMessages(meta.SelectedCapabilityRoots),
			MultiAgentVersion:       meta.MultiAgentVersion,
			ContextWindow:           append(json.RawMessage(nil), meta.ContextWindow...),
			Git:                     cloneStringMap(meta.Git),
			PreviousResponseID: firstNonEmptyString(
				stringFromMap(meta.Extra, "previous_response_id"),
				stringFromMap(meta.Extra, "previousResponseId"),
			),
			LastResponseID: firstNonEmptyString(
				stringFromMap(meta.Extra, "last_response_id"),
				stringFromMap(meta.Extra, "lastResponseId"),
			),
			Extra:        cloneAnyMap(meta.Extra),
			RolloutTurns: rolloutTurns,
		},
		Items:       items,
		FromRollout: true,
	}
	applyRolloutContextMetadata(record, lines)
	applyRolloutTokenUsageMetadata(record, lines)
	return record, nil
}

func applyRolloutContextMetadata(record *session.Record, lines []Line) {
	if record == nil {
		return
	}
	for i := range lines {
		if len(lines[i].TurnContext) > 0 {
			record.Metadata.TurnContext = append(json.RawMessage(nil), lines[i].TurnContext...)
			applyTurnContextMetadata(&record.Metadata, lines[i].TurnContext)
		}
		if len(lines[i].WorldState) > 0 {
			record.Metadata.WorldState = append(json.RawMessage(nil), lines[i].WorldState...)
		}
	}
}

func applyRolloutTokenUsageMetadata(record *session.Record, lines []Line) {
	if record == nil {
		return
	}
	activeTurnID := ""
	tokenUsageTurnID := ""
	var tokenUsageInfo map[string]any
	for i := range lines {
		if lines[i].Type != "event_msg" || len(lines[i].Payload) == 0 {
			continue
		}
		var payload rolloutEventPayload
		if err := json.Unmarshal(lines[i].Payload, &payload); err != nil {
			continue
		}
		switch normalizeRolloutEventType(payload.Type) {
		case "turn_started":
			activeTurnID = firstNonEmptyString(payload.TurnID, payload.TurnIDCamel)
		case "turn_complete", "turn_aborted":
			turnID := firstNonEmptyString(payload.TurnID, payload.TurnIDCamel)
			if turnID == "" || activeTurnID == "" || activeTurnID == turnID {
				activeTurnID = ""
			}
		case "token_count":
			if len(payload.Info) == 0 {
				continue
			}
			tokenUsageInfo = cloneAnyMap(payload.Info)
			tokenUsageTurnID = activeTurnID
		}
	}
	if len(tokenUsageInfo) == 0 {
		return
	}
	if record.Metadata.Extra == nil {
		record.Metadata.Extra = map[string]any{}
	}
	record.Metadata.Extra["token_usage_info"] = tokenUsageInfo
	if tokenUsageTurnID != "" {
		record.Metadata.Extra["token_usage_turn_id"] = tokenUsageTurnID
	}
}

func applyTurnContextMetadata(metadata *session.Metadata, raw json.RawMessage) {
	if metadata == nil || len(raw) == 0 {
		return
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return
	}
	if cwd := stringFromAny(values["cwd"]); cwd != "" {
		metadata.CWD = cwd
	}
	if model := stringFromAny(values["model"]); model != "" {
		metadata.Model = model
	}
	if approval := stringFromAny(values["approval_policy"]); approval != "" {
		metadata.ApprovalPolicy = approval
	}
	if sandbox := stringFromAny(values["sandbox_policy"]); sandbox != "" {
		metadata.SandboxPolicy = sandbox
	}
	if effort := stringFromAny(values["effort"]); effort != "" {
		if metadata.Extra == nil {
			metadata.Extra = map[string]any{}
		}
		metadata.Extra["effort"] = effort
	}
	if personality := stringFromAny(values["personality"]); personality != "" {
		if metadata.Extra == nil {
			metadata.Extra = map[string]any{}
		}
		metadata.Extra["personality"] = personality
	}
	if multiAgentVersion := stringFromAny(values["multi_agent_version"]); multiAgentVersion != "" {
		metadata.MultiAgentVersion = multiAgentVersion
	}
	if rawContext, ok := rawMapValue(values, "permission_profile"); ok {
		if metadata.Extra == nil {
			metadata.Extra = map[string]any{}
		}
		metadata.Extra["permission_profile"] = rawContext
	}
	if rawContext, ok := rawMapValue(values, "network"); ok {
		if metadata.Extra == nil {
			metadata.Extra = map[string]any{}
		}
		metadata.Extra["network"] = rawContext
	}
}

func sessionItemsFromRolloutLines(lines []Line, fallback time.Time) ([]session.Item, []session.TurnSnapshot) {
	builder := newRolloutReplayBuilder(fallback)
	for i := range lines {
		if lines[i].ThreadRolledBack != nil {
			builder.rollback(int(lines[i].ThreadRolledBack.NumTurns))
			continue
		}
		if lines[i].Type == "compacted" {
			if replacement, ok := compactedReplacementItems(lines[i].Payload); ok {
				builder.resetForCompactedReplacement()
				for j := range replacement {
					sessionItem := SessionItemFromRolloutItem(&replacement[j], lineItemCreatedAt(&lines[i], fallback), len(builder.items))
					if sessionItem.ID == "" {
						continue
					}
					builder.appendExistingItem(sessionItem)
				}
			} else {
				builder.markCompacted(i)
			}
			continue
		}
		if lines[i].Type == "event_msg" && builder.handleEventLine(&lines[i], i) {
			continue
		}
		item, ok := ItemFromLine(&lines[i])
		if !ok {
			continue
		}
		sessionItem := SessionItemFromRolloutItem(item, lineItemCreatedAt(&lines[i], fallback), len(builder.items))
		if sessionItem.ID == "" {
			continue
		}
		builder.appendExistingItem(sessionItem)
	}
	return builder.finish()
}

type rolloutReplayBuilder struct {
	items         []session.Item
	turns         []session.TurnSnapshot
	current       *rolloutReplayTurn
	fallback      time.Time
	nextItemIndex int
}

type rolloutReplayTurn struct {
	snapshot         session.TurnSnapshot
	itemCount        int
	openedExplicitly bool
	sawCompaction    bool
}

type rolloutEventPayload struct {
	Type               string          `json:"type"`
	TurnID             string          `json:"turn_id"`
	TurnIDCamel        string          `json:"turnId"`
	StartedAt          *int64          `json:"started_at"`
	StartedAtCamel     *int64          `json:"startedAt"`
	CompletedAt        *int64          `json:"completed_at"`
	CompletedAtCamel   *int64          `json:"completedAt"`
	DurationMS         *int64          `json:"duration_ms"`
	DurationMSCamel    *int64          `json:"durationMs"`
	CompletedAtMS      *int64          `json:"completed_at_ms"`
	CompletedAtMSCamel *int64          `json:"completedAtMs"`
	Message            string          `json:"message"`
	Error              string          `json:"error"`
	Item               json.RawMessage `json:"item"`
	Info               map[string]any  `json:"info"`
	ClientID           *string         `json:"client_id"`
	ClientIDCamel      *string         `json:"clientId"`
	Images             []string        `json:"images"`
	ImageDetails       []any           `json:"image_details"`
	ImageDetailsCamel  []any           `json:"imageDetails"`
	LocalImages        []string        `json:"local_images"`
	LocalImagesCamel   []string        `json:"localImages"`
	TextElements       json.RawMessage `json:"text_elements"`
	TextElementsCamel  json.RawMessage `json:"textElements"`
}

func newRolloutReplayBuilder(fallback time.Time) *rolloutReplayBuilder {
	return &rolloutReplayBuilder{
		items:         []session.Item{},
		fallback:      fallback,
		nextItemIndex: 1,
	}
}

func (b *rolloutReplayBuilder) finish() ([]session.Item, []session.TurnSnapshot) {
	if b == nil {
		return nil, nil
	}
	b.finishCurrentTurn()
	return b.items, b.turns
}

func (b *rolloutReplayBuilder) resetForCompactedReplacement() {
	b.items = []session.Item{}
	b.turns = nil
	b.current = nil
	b.nextItemIndex = 1
}

func (b *rolloutReplayBuilder) rollback(numTurns int) {
	if numTurns <= 0 {
		return
	}
	b.finishCurrentTurn()
	b.items = rollbackSessionItems(b.items, numTurns)
	if numTurns >= len(b.turns) {
		b.turns = nil
		return
	}
	b.turns = b.turns[:len(b.turns)-numTurns]
}

func (b *rolloutReplayBuilder) markCompacted(lineIndex int) {
	turn := b.ensureTurn(lineIndex)
	turn.sawCompaction = true
}

func (b *rolloutReplayBuilder) handleEventLine(line *Line, lineIndex int) bool {
	if b == nil || line == nil || len(line.Payload) == 0 {
		return false
	}
	var payload rolloutEventPayload
	if err := json.Unmarshal(line.Payload, &payload); err != nil {
		return false
	}
	switch normalizeRolloutEventType(payload.Type) {
	case "turn_started":
		b.handleTurnStarted(payload, lineIndex)
		return true
	case "turn_complete":
		b.handleTurnComplete(payload)
		return true
	case "turn_aborted":
		b.handleTurnAborted(payload, lineIndex)
		return true
	case "error":
		b.handleTurnError(payload, lineIndex)
		return true
	case "item_completed":
		b.handleItemCompleted(payload, line, lineIndex)
		return true
	case "user_message":
		b.handleUserMessage(payload, line, lineIndex)
		return true
	case "agent_message":
		b.handleAgentMessage(payload, line, lineIndex)
		return true
	default:
		return false
	}
}

func (b *rolloutReplayBuilder) handleTurnStarted(payload rolloutEventPayload, lineIndex int) {
	b.finishCurrentTurn()
	turnID := firstNonEmptyString(payload.TurnID, payload.TurnIDCamel, fmt.Sprintf("rollout-%d", lineIndex))
	b.current = &rolloutReplayTurn{
		snapshot: session.TurnSnapshot{
			ID:        turnID,
			Status:    "inProgress",
			StartedAt: firstNonNilInt64(payload.StartedAt, payload.StartedAtCamel),
		},
		openedExplicitly: true,
	}
}

func (b *rolloutReplayBuilder) handleTurnComplete(payload rolloutEventPayload) {
	turnID := firstNonEmptyString(payload.TurnID, payload.TurnIDCamel)
	if b.current != nil && turnID != "" && b.current.snapshot.ID != turnID && strings.HasPrefix(b.current.snapshot.ID, "rollout-") {
		b.current.snapshot.ID = turnID
	}
	apply := func(snapshot *session.TurnSnapshot) {
		if snapshot == nil {
			return
		}
		if snapshot.Status == "" || snapshot.Status == "completed" || snapshot.Status == "inProgress" {
			snapshot.Status = "completed"
		}
		snapshot.CompletedAt = firstNonNilInt64(payload.CompletedAt, payload.CompletedAtCamel)
		snapshot.DurationMS = firstNonNilInt64(payload.DurationMS, payload.DurationMSCamel)
	}
	if b.current != nil && (turnID == "" || b.current.snapshot.ID == turnID) {
		apply(&b.current.snapshot)
		b.finishCurrentTurn()
		return
	}
	for i := range b.turns {
		if b.turns[i].ID == turnID {
			apply(&b.turns[i])
			return
		}
	}
	if b.current != nil {
		apply(&b.current.snapshot)
		b.finishCurrentTurn()
	}
}

func (b *rolloutReplayBuilder) handleTurnAborted(payload rolloutEventPayload, lineIndex int) {
	turnID := firstNonEmptyString(payload.TurnID, payload.TurnIDCamel)
	apply := func(snapshot *session.TurnSnapshot) {
		if snapshot == nil {
			return
		}
		snapshot.Status = "interrupted"
		snapshot.CompletedAt = firstNonNilInt64(payload.CompletedAt, payload.CompletedAtCamel)
		snapshot.DurationMS = firstNonNilInt64(payload.DurationMS, payload.DurationMSCamel)
	}
	if b.current != nil && (turnID == "" || b.current.snapshot.ID == turnID) {
		apply(&b.current.snapshot)
		return
	}
	if turnID == "" && len(b.items) > 0 {
		turnID = sessionItemTurnID(&b.items[len(b.items)-1], len(b.items)-1)
	}
	if turnID == "" {
		turnID = fmt.Sprintf("rollout-%d", lineIndex)
	}
	for i := range b.turns {
		if b.turns[i].ID == turnID {
			apply(&b.turns[i])
			return
		}
	}
	if b.current != nil {
		apply(&b.current.snapshot)
		return
	}
	snapshot := session.TurnSnapshot{ID: turnID}
	apply(&snapshot)
	b.turns = append(b.turns, snapshot)
}

func (b *rolloutReplayBuilder) handleTurnError(payload rolloutEventPayload, lineIndex int) {
	message := firstNonEmptyString(payload.Message, payload.Error)
	turn := b.ensureTurn(lineIndex)
	turn.snapshot.Status = "failed"
	turn.snapshot.ErrorMessage = message
	turn.openedExplicitly = true
}

func (b *rolloutReplayBuilder) handleItemCompleted(payload rolloutEventPayload, line *Line, lineIndex int) {
	if len(payload.Item) == 0 {
		return
	}
	item, ok := sessionItemFromRolloutEventItem(payload.Item, line, lineIndex, firstNonNilInt64(payload.CompletedAtMS, payload.CompletedAtMSCamel))
	if !ok {
		return
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	if turnID := firstNonEmptyString(payload.TurnID, payload.TurnIDCamel); turnID != "" {
		item.Metadata["turnId"] = turnID
	}
	b.appendExistingItem(item)
}

func sessionItemFromRolloutEventItem(raw json.RawMessage, line *Line, lineIndex int, completedAtMS *int64) (session.Item, bool) {
	var item Item
	if err := json.Unmarshal(raw, &item); err != nil {
		return session.Item{}, false
	}
	item.Raw = append(json.RawMessage(nil), raw...)
	item.Type = normalizeRolloutItemType(item.Type)
	if item.Type == "" || item.Type == "message" {
		return session.Item{}, false
	}
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	var rawData map[string]any
	if err := json.Unmarshal(raw, &rawData); err == nil {
		for key, value := range rawData {
			if _, ok := item.Data[key]; !ok {
				item.Data[key] = value
			}
		}
	}
	createdAt := lineItemCreatedAt(line, time.Time{})
	if completedAtMS != nil && *completedAtMS > 0 {
		createdAt = time.UnixMilli(*completedAtMS).UTC()
	}
	sessionItem := SessionItemFromRolloutItem(&item, createdAt, lineIndex)
	if sessionItem.ID == "" {
		return session.Item{}, false
	}
	return sessionItem, true
}

func (b *rolloutReplayBuilder) handleUserMessage(payload rolloutEventPayload, line *Line, lineIndex int) {
	if b.current != nil && !b.current.openedExplicitly && !(b.current.sawCompaction && b.current.itemCount == 0) {
		b.finishCurrentTurn()
	}
	turn := b.ensureTurn(lineIndex)
	item := session.Item{
		ID:        b.nextItemID(),
		Type:      "message",
		Role:      "user",
		Text:      payload.Message,
		CreatedAt: lineItemCreatedAt(line, b.fallback),
		Metadata:  map[string]any{"turnId": turn.snapshot.ID, "kind": "user_message"},
		Data:      map[string]any{},
		Content:   rolloutUserMessageContent(payload),
	}
	if clientID := firstNonNilString(payload.ClientID, payload.ClientIDCamel); clientID != "" {
		item.Data["clientId"] = clientID
		item.Data["client_id"] = clientID
	}
	if len(payload.TextElements) > 0 {
		item.Data["textElements"] = jsonRawToAny(payload.TextElements)
	}
	if len(payload.TextElementsCamel) > 0 {
		item.Data["textElements"] = jsonRawToAny(payload.TextElementsCamel)
	}
	b.appendGeneratedItem(item)
}

func (b *rolloutReplayBuilder) handleAgentMessage(payload rolloutEventPayload, line *Line, lineIndex int) {
	if strings.TrimSpace(payload.Message) == "" {
		return
	}
	turn := b.ensureTurn(lineIndex)
	b.appendGeneratedItem(session.Item{
		ID:        b.nextItemID(),
		Type:      "agent_message",
		Role:      "assistant",
		Text:      payload.Message,
		CreatedAt: lineItemCreatedAt(line, b.fallback),
		Metadata:  map[string]any{"turnId": turn.snapshot.ID},
	})
}

func (b *rolloutReplayBuilder) ensureTurn(lineIndex int) *rolloutReplayTurn {
	if b.current == nil {
		b.current = &rolloutReplayTurn{
			snapshot: session.TurnSnapshot{
				ID:     fmt.Sprintf("rollout-%d", lineIndex),
				Status: "completed",
			},
		}
	}
	return b.current
}

func (b *rolloutReplayBuilder) finishCurrentTurn() {
	if b.current == nil {
		return
	}
	turn := b.current
	b.current = nil
	if turn.itemCount == 0 && !turn.openedExplicitly && !turn.sawCompaction {
		return
	}
	if turn.snapshot.Status == "" {
		turn.snapshot.Status = "completed"
	}
	b.turns = append(b.turns, turn.snapshot)
}

func (b *rolloutReplayBuilder) appendGeneratedItem(item session.Item) {
	b.items = append(b.items, item)
	if b.current != nil {
		b.current.itemCount++
	}
}

func (b *rolloutReplayBuilder) appendExistingItem(item session.Item) {
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	if b.current != nil && firstNonEmptyString(stringFromMap(item.Metadata, "turnId"), stringFromMap(item.Metadata, "turn_id"), stringFromMap(item.Data, "turnId"), stringFromMap(item.Data, "turn_id")) == "" {
		item.Metadata["turnId"] = b.current.snapshot.ID
	}
	b.items = append(b.items, item)
	if b.current == nil {
		return
	}
	if sessionItemTurnID(&item, len(b.items)-1) == b.current.snapshot.ID {
		b.current.itemCount++
	}
}

func (b *rolloutReplayBuilder) nextItemID() string {
	id := fmt.Sprintf("item-%d", b.nextItemIndex)
	b.nextItemIndex++
	return id
}

func normalizeRolloutEventType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "task_started", "turn_started", "turnstarted":
		return "turn_started"
	case "task_complete", "turn_complete", "turncomplete":
		return "turn_complete"
	case "turn_aborted", "turnaborted":
		return "turn_aborted"
	case "error":
		return "error"
	case "item_completed", "itemcompleted":
		return "item_completed"
	case "token_count", "tokencount":
		return "token_count"
	case "user_message", "usermessage":
		return "user_message"
	case "agent_message", "agentmessage":
		return "agent_message"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func rolloutUserMessageContent(payload rolloutEventPayload) []session.ContentPart {
	content := []session.ContentPart{}
	if strings.TrimSpace(payload.Message) != "" {
		content = append(content, session.ContentPart{Type: "input_text", Text: payload.Message})
	}
	imageDetails := payload.ImageDetails
	if len(imageDetails) == 0 {
		imageDetails = payload.ImageDetailsCamel
	}
	for i, image := range payload.Images {
		if strings.TrimSpace(image) == "" {
			continue
		}
		content = append(content, session.ContentPart{Type: "input_image", ImageURL: strings.TrimSpace(image), Detail: stringPtrFromAnyAt(imageDetails, i)})
	}
	return content
}

func firstNonNilInt64(values ...*int64) *int64 {
	for _, value := range values {
		if value == nil {
			continue
		}
		cloned := *value
		return &cloned
	}
	return nil
}

func firstNonNilString(values ...*string) string {
	for _, value := range values {
		if value == nil || strings.TrimSpace(*value) == "" {
			continue
		}
		return strings.TrimSpace(*value)
	}
	return ""
}

func jsonRawToAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

func stringPtrFromAnyAt(values []any, index int) *string {
	if index < 0 || index >= len(values) {
		return nil
	}
	value := stringFromAny(values[index])
	if strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	return &value
}

func rollbackSessionItems(items []session.Item, numTurns int) []session.Item {
	if numTurns <= 0 || len(items) == 0 {
		return append([]session.Item(nil), items...)
	}
	turnIDs := make([]string, 0)
	seen := make(map[string]bool)
	for i := range items {
		turnID := sessionItemTurnID(&items[i], i)
		if seen[turnID] {
			continue
		}
		seen[turnID] = true
		turnIDs = append(turnIDs, turnID)
	}
	if numTurns >= len(turnIDs) {
		return []session.Item{}
	}
	drop := make(map[string]bool)
	for _, turnID := range turnIDs[len(turnIDs)-numTurns:] {
		drop[turnID] = true
	}
	out := make([]session.Item, 0, len(items))
	for i := range items {
		if drop[sessionItemTurnID(&items[i], i)] {
			continue
		}
		out = append(out, items[i])
	}
	return out
}

func sessionItemTurnID(item *session.Item, index int) string {
	if item != nil {
		if value, ok := item.Metadata["turnId"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
		if value, ok := item.Metadata["turn_id"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
		if value, ok := item.Data["turnId"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
		if value, ok := item.Data["turn_id"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return fmt.Sprintf("turn-%d", index+1)
}

func SessionItemFromRolloutItem(item *Item, createdAt time.Time, index int) session.Item {
	if item == nil {
		return session.Item{}
	}
	itemType := normalizeRolloutItemType(item.Type)
	role := item.Role
	if itemType == "message" && role == "assistant" {
		itemType = "agent_message"
	}
	if itemType == "message" && role == "" {
		role = "user"
	}
	text := firstNonEmptyString(item.Text, textFromRolloutContent(item.Content))
	if itemType == "reasoning" && text == "" {
		text = stringsFromAny(item.Data["summary"])
	}
	metadata := cloneAnyMap(item.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	if len(item.Raw) > 0 {
		var raw any
		if err := json.Unmarshal(item.Raw, &raw); err == nil {
			metadata["rawResponseItem"] = raw
		}
	}
	if turnID := firstNonEmptyString(stringFromMap(item.Metadata, "turnId"), stringFromMap(item.Metadata, "turn_id"), stringFromMap(item.Data, "turnId"), stringFromMap(item.Data, "turn_id")); turnID != "" {
		metadata["turnId"] = turnID
	}
	sessionItem := session.Item{
		ID:         firstNonEmptyString(item.ID, fallbackItemID(index)),
		Type:       firstNonEmptyString(itemType, "raw_response_item"),
		Role:       role,
		Text:       text,
		Name:       item.Name,
		CallID:     item.CallID,
		Content:    sessionContentPartsFromRollout(item.Content),
		Data:       cloneAnyMap(item.Data),
		Raw:        append(json.RawMessage(nil), item.Raw...),
		CreatedAt:  createdAt,
		ResponseID: item.ResponseID,
		Metadata:   metadata,
	}
	normalizeComplexSessionItemFromRollout(&sessionItem, item)
	return sessionItem
}

func normalizeComplexSessionItemFromRollout(out *session.Item, item *Item) {
	if out == nil || item == nil {
		return
	}
	if out.Data == nil {
		out.Data = map[string]any{}
	}
	raw := map[string]any{}
	if len(item.Raw) > 0 {
		_ = json.Unmarshal(item.Raw, &raw)
	}
	for key, value := range raw {
		if _, ok := out.Data[key]; !ok {
			out.Data[key] = value
		}
	}
	itemType := normalizeRolloutItemType(firstNonEmptyString(out.Type, stringFromMap(raw, "type")))
	if itemType != "" {
		out.Type = itemType
	}
	switch itemType {
	case "mcpToolCall", "mcp_tool_call":
		markRolloutMCPItem(out, raw)
	case "dynamicToolCall", "dynamic_tool_call":
		markRolloutDynamicItem(out, raw)
	case "fileChange", "file_change":
		markRolloutFileChangeItem(out, raw)
	case "commandExecution", "command_execution":
		markRolloutCommandItem(out, raw)
	case "function_call", "custom_tool_call", "tool_search_call":
		normalizeRolloutToolCall(out, raw)
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_output":
		normalizeRolloutToolOutput(out, raw)
	}
}

func normalizeRolloutToolCall(out *session.Item, raw map[string]any) {
	name := firstNonEmptyString(out.Name, stringFromMap(out.Data, "name"), stringFromMap(raw, "name"))
	if name != "" {
		out.Name = name
		out.Data["name"] = name
	}
	if out.CallID == "" {
		out.CallID = firstNonEmptyString(stringFromMap(out.Data, "call_id"), stringFromMap(out.Data, "callId"), stringFromMap(raw, "call_id"), stringFromMap(raw, "callId"), out.ID)
	}
	if rawType := normalizeRolloutItemType(stringFromMap(raw, "type")); rawType == "mcpToolCall" || rawType == "mcp_tool_call" {
		markRolloutMCPItem(out, raw)
		return
	}
	if rawType := normalizeRolloutItemType(stringFromMap(raw, "type")); rawType == "dynamicToolCall" || rawType == "dynamic_tool_call" {
		markRolloutDynamicItem(out, raw)
		return
	}
	if rawType := normalizeRolloutItemType(stringFromMap(raw, "type")); rawType == "fileChange" || rawType == "file_change" {
		markRolloutFileChangeItem(out, raw)
		return
	}
	if name == "apply_patch" || strings.HasSuffix(name, ".apply_patch") {
		out.Data["fileChange"] = true
		if changes := rolloutChangesFromAny(firstPresentAny(raw, out.Data, "changes", "fileChanges", "file_changes")); len(changes) > 0 {
			out.Data["changes"] = changes
		}
		return
	}
	if server, tool := splitRolloutToolName(name); server != "" && tool != "" && !rolloutLooksLikeShellTool(name) {
		out.Data["mcpToolCall"] = true
		out.Data["server"] = firstNonEmptyString(stringFromMap(out.Data, "server"), server)
		out.Data["tool"] = firstNonEmptyString(stringFromMap(out.Data, "tool"), tool)
	}
}

func normalizeRolloutToolOutput(out *session.Item, raw map[string]any) {
	if out.CallID == "" {
		out.CallID = firstNonEmptyString(stringFromMap(out.Data, "call_id"), stringFromMap(out.Data, "callId"), stringFromMap(raw, "call_id"), stringFromMap(raw, "callId"), out.ID)
	}
	if rolloutBoolFromAny(firstPresentAny(raw, out.Data, "mcpToolCall", "mcp_tool_call")) {
		markRolloutMCPItem(out, raw)
		return
	}
	if rolloutBoolFromAny(firstPresentAny(raw, out.Data, "dynamicToolCall", "dynamic_tool_call")) {
		markRolloutDynamicItem(out, raw)
		return
	}
	if rolloutBoolFromAny(firstPresentAny(raw, out.Data, "fileChange", "file_change")) || out.Name == "apply_patch" {
		markRolloutFileChangeItem(out, raw)
		return
	}
	if result := mapFromAny(firstPresentAny(raw, out.Data, "result")); result != nil {
		if rolloutBoolFromAny(result["isError"]) || rolloutBoolFromAny(result["is_error"]) {
			out.Data["success"] = false
		}
		if _, ok := out.Data["content"]; !ok {
			if content, exists := result["content"]; exists {
				out.Data["content"] = content
			}
		}
		if _, ok := out.Data["structuredContent"]; !ok {
			if content, exists := result["structuredContent"]; exists {
				out.Data["structuredContent"] = content
			}
		}
		if _, ok := out.Data["_meta"]; !ok {
			if meta, exists := result["_meta"]; exists {
				out.Data["_meta"] = meta
			}
		}
	}
}

func markRolloutMCPItem(out *session.Item, raw map[string]any) {
	out.Type = firstNonEmptyString(out.Type, "function_call")
	out.Data["mcpToolCall"] = true
	copyFirstRolloutValue(out.Data, raw, "server", "server")
	copyFirstRolloutValue(out.Data, raw, "tool", "tool")
	copyFirstRolloutValue(out.Data, raw, "arguments", "arguments")
	copyFirstRolloutValue(out.Data, raw, "appContext", "appContext", "app_context")
	copyFirstRolloutValue(out.Data, raw, "mcpAppResourceUri", "mcpAppResourceUri", "mcp_app_resource_uri")
	copyFirstRolloutValue(out.Data, raw, "pluginId", "pluginId", "plugin_id")
	copyFirstRolloutValue(out.Data, raw, "result", "result")
	copyFirstRolloutValue(out.Data, raw, "error", "error")
	copyFirstRolloutValue(out.Data, raw, "durationMs", "durationMs", "duration_ms")
	if out.Name == "" {
		if server := stringFromMap(out.Data, "server"); server != "" {
			if tool := stringFromMap(out.Data, "tool"); tool != "" {
				out.Name = server + "." + tool
			}
		}
	}
	if _, ok := out.Data["success"]; !ok {
		switch strings.TrimSpace(stringFromMap(out.Data, "status")) {
		case "failed":
			out.Data["success"] = false
		case "completed":
			out.Data["success"] = true
		}
	}
}

func markRolloutDynamicItem(out *session.Item, raw map[string]any) {
	out.Data["dynamicToolCall"] = true
	copyFirstRolloutValue(out.Data, raw, "namespace", "namespace")
	copyFirstRolloutValue(out.Data, raw, "tool", "tool")
	copyFirstRolloutValue(out.Data, raw, "arguments", "arguments")
	copyFirstRolloutValue(out.Data, raw, "contentItems", "contentItems", "content_items")
	copyFirstRolloutValue(out.Data, raw, "success", "success")
	copyFirstRolloutValue(out.Data, raw, "error", "error")
	copyFirstRolloutValue(out.Data, raw, "durationMs", "durationMs", "duration_ms")
	if out.Name == "" {
		out.Name = joinRolloutNamespaceTool(stringFromMap(out.Data, "namespace"), stringFromMap(out.Data, "tool"))
	}
}

func markRolloutFileChangeItem(out *session.Item, raw map[string]any) {
	out.Data["fileChange"] = true
	copyFirstRolloutValue(out.Data, raw, "status", "status")
	if changes := rolloutChangesFromAny(firstPresentAny(raw, out.Data, "changes", "fileChanges", "file_changes")); len(changes) > 0 {
		out.Data["changes"] = changes
	}
	if out.Name == "" {
		out.Name = "apply_patch"
	}
}

func markRolloutCommandItem(out *session.Item, raw map[string]any) {
	copyFirstRolloutValue(out.Data, raw, "command", "command")
	copyFirstRolloutValue(out.Data, raw, "cwd", "cwd")
	copyFirstRolloutValue(out.Data, raw, "processId", "processId", "process_id")
	copyFirstRolloutValue(out.Data, raw, "source", "source")
	copyFirstRolloutValue(out.Data, raw, "status", "status")
	copyFirstRolloutValue(out.Data, raw, "commandActions", "commandActions", "command_actions")
	copyFirstRolloutValue(out.Data, raw, "aggregatedOutput", "aggregatedOutput", "aggregated_output")
	copyFirstRolloutValue(out.Data, raw, "exitCode", "exitCode", "exit_code")
	copyFirstRolloutValue(out.Data, raw, "durationMs", "durationMs", "duration_ms")
	if command := rolloutCommandStringFromAny(firstPresentAny(raw, out.Data, "command")); command != "" {
		out.Data["command"] = command
	}
	if cwd := rolloutPathStringFromAny(firstPresentAny(raw, out.Data, "cwd")); cwd != "" {
		out.Data["cwd"] = cwd
	}
	if durationMS, ok := rolloutDurationMSFromAny(firstPresentAny(raw, out.Data, "duration")); ok {
		out.Data["durationMs"] = durationMS
		out.Data["duration_ms"] = durationMS
	}
	if durationMS, ok := rolloutInt64FromAny(firstPresentAny(raw, out.Data, "durationMs", "duration_ms")); ok {
		out.Data["durationMs"] = durationMS
		out.Data["duration_ms"] = durationMS
	}
	if _, ok := out.Data["commandActions"]; !ok {
		if actions := rolloutCommandActionsFromAny(firstPresentAny(raw, out.Data, "parsed_cmd", "parsedCmd")); len(actions) > 0 {
			out.Data["commandActions"] = actions
			out.Data["command_actions"] = actions
		}
	}
	if output := rawStringFromMap(out.Data, "aggregatedOutput"); strings.TrimSpace(output) != "" && out.Text == "" {
		out.Text = output
	}
}

func normalizeRolloutItemType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "usermessage", "user_message":
		return "message"
	case "agentmessage", "agent_message":
		return "agent_message"
	case "hookprompt", "hook_prompt":
		return "hookPrompt"
	case "plan":
		return "plan"
	case "reasoning":
		return "reasoning"
	case "commandexecution", "command_execution":
		return "commandExecution"
	case "dynamictoolcall", "dynamic_tool_call":
		return "dynamicToolCall"
	case "collabagenttoolcall", "collab_agent_tool_call":
		return "collabAgentToolCall"
	case "subagentactivity", "sub_agent_activity":
		return "subAgentActivity"
	case "websearch", "web_search":
		return "webSearch"
	case "imageview", "image_view":
		return "imageView"
	case "sleep":
		return "sleep"
	case "imagegeneration", "image_generation":
		return "imageGeneration"
	case "filechange", "file_change":
		return "fileChange"
	case "mcptoolcall", "mcp_tool_call":
		return "mcpToolCall"
	case "contextcompaction", "context_compaction":
		return "contextCompaction"
	case "enteredreviewmode", "entered_review_mode":
		return "enteredReviewMode"
	case "exitedreviewmode", "exited_review_mode":
		return "exitedReviewMode"
	default:
		return strings.TrimSpace(value)
	}
}

func rolloutCommandStringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []string:
		return rolloutShellJoin(typed)
	case []any:
		args := make([]string, 0, len(typed))
		for _, entry := range typed {
			text, ok := entry.(string)
			if !ok {
				return ""
			}
			args = append(args, text)
		}
		return rolloutShellJoin(args)
	default:
		return ""
	}
}

func rolloutShellJoin(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, rolloutShellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func rolloutShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\r\n'\"\\$`!&|;<>(){}[]*?") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func rolloutPathStringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		for _, key := range []string{"path", "abs", "value", "uri"} {
			if text := stringFromMap(typed, key); text != "" {
				return text
			}
		}
	default:
		if mapped := mapFromAny(value); mapped != nil {
			return rolloutPathStringFromAny(mapped)
		}
	}
	return ""
}

func rolloutDurationMSFromAny(value any) (int64, bool) {
	switch typed := value.(type) {
	case string:
		if parsed, err := time.ParseDuration(strings.TrimSpace(typed)); err == nil {
			return parsed.Milliseconds(), true
		}
		return rolloutInt64FromAny(typed)
	case map[string]any:
		secs, _ := rolloutInt64FromAny(firstPresentAny(typed, nil, "secs", "seconds"))
		nanos, _ := rolloutInt64FromAny(firstPresentAny(typed, nil, "nanos", "nanoseconds"))
		if secs != 0 || nanos != 0 {
			return secs*1000 + nanos/int64(time.Millisecond), true
		}
		return rolloutInt64FromAny(firstPresentAny(typed, nil, "millis", "milliseconds", "ms"))
	default:
		return rolloutInt64FromAny(value)
	}
}

func rolloutInt64FromAny(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case int32:
		return int64(typed), true
	case uint:
		return int64(typed), true
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(typed), true
	case float64:
		return int64(typed), true
	case json.Number:
		value, err := typed.Int64()
		return value, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func rolloutCommandActionsFromAny(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(typed))
		for _, action := range typed {
			normalized := rolloutCommandActionFromMap(action)
			if len(normalized) > 0 {
				out = append(out, normalized)
			}
		}
		return out
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, entry := range typed {
			action := rolloutCommandActionFromMap(mapFromAny(entry))
			if len(action) > 0 {
				out = append(out, action)
			}
		}
		return out
	default:
		if mapped := mapFromAny(value); mapped != nil {
			action := rolloutCommandActionFromMap(mapped)
			if len(action) > 0 {
				return []map[string]any{action}
			}
		}
		return nil
	}
}

func rolloutCommandActionFromMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	actionType := strings.ToLower(strings.TrimSpace(stringFromMap(value, "type")))
	command := firstNonEmptyString(stringFromMap(value, "command"), stringFromMap(value, "cmd"))
	switch actionType {
	case "read":
		out := map[string]any{"type": "read", "command": command}
		if name := stringFromMap(value, "name"); name != "" {
			out["name"] = name
		}
		if path := rolloutPathStringFromAny(value["path"]); path != "" {
			out["path"] = path
		}
		return out
	case "listfiles", "list_files":
		out := map[string]any{"type": "listFiles", "command": command}
		if path := rolloutPathStringFromAny(value["path"]); path != "" {
			out["path"] = path
		}
		return out
	case "search":
		out := map[string]any{"type": "search", "command": command}
		if query := stringFromMap(value, "query"); query != "" {
			out["query"] = query
		}
		if path := rolloutPathStringFromAny(value["path"]); path != "" {
			out["path"] = path
		}
		return out
	case "unknown":
		return map[string]any{"type": "unknown", "command": command}
	default:
		if command == "" {
			return nil
		}
		return map[string]any{"type": "unknown", "command": command}
	}
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func itemTime(createdAt time.Time, fallback time.Time) time.Time {
	if !createdAt.IsZero() {
		return createdAt.UTC()
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return time.Now().UTC()
}

func parseMetaTimestamp(value string) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC()
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

func lineItemCreatedAt(line *Line, fallback time.Time) time.Time {
	if line != nil {
		if parsed, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
			return parsed.UTC()
		}
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return time.Now().UTC()
}

func sessionContentPartsFromRollout(content []ContentPart) []session.ContentPart {
	if len(content) == 0 {
		return nil
	}
	out := make([]session.ContentPart, 0, len(content))
	for i := range content {
		out = append(out, session.ContentPart{
			Type:     content[i].Type,
			Text:     content[i].Text,
			ImageURL: content[i].ImageURL,
			Detail:   cloneStringPtr(content[i].Detail),
		})
	}
	return out
}

func textFromRolloutContent(content []ContentPart) string {
	parts := make([]string, 0, len(content))
	for i := range content {
		if content[i].Text != "" {
			parts = append(parts, content[i].Text)
		}
	}
	return strings.Join(parts, "\n")
}

func previewFromSessionItems(items []session.Item) string {
	for i := range items {
		if items[i].Role == "user" && strings.TrimSpace(items[i].Text) != "" {
			return strings.TrimSpace(items[i].Text)
		}
	}
	for i := range items {
		if strings.TrimSpace(items[i].Text) != "" {
			return strings.TrimSpace(items[i].Text)
		}
	}
	return ""
}

func stringsFromAny(value any) string {
	switch typed := value.(type) {
	case []string:
		return strings.Join(typed, "\n")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func rawStringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func stringFromAny(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func rawMapValue(values map[string]any, key string) (map[string]any, bool) {
	if values == nil {
		return nil, false
	}
	raw, ok := values[key]
	if !ok {
		return nil, false
	}
	value, ok := raw.(map[string]any)
	return value, ok
}

func firstPresentAny(primary map[string]any, fallback map[string]any, keys ...string) any {
	for _, key := range keys {
		if primary != nil {
			if value, ok := primary[key]; ok {
				return value
			}
		}
		if fallback != nil {
			if value, ok := fallback[key]; ok {
				return value
			}
		}
	}
	return nil
}

func copyFirstRolloutValue(target map[string]any, source map[string]any, targetKey string, sourceKeys ...string) {
	if target == nil {
		return
	}
	if _, ok := target[targetKey]; ok {
		return
	}
	for _, key := range sourceKeys {
		if source != nil {
			if value, ok := source[key]; ok {
				target[targetKey] = value
				return
			}
		}
		if value, ok := target[key]; ok {
			target[targetKey] = value
			return
		}
	}
}

func mapFromAny(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil
		}
		return decoded
	}
}

func rolloutBoolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func rolloutLooksLikeShellTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "shell", "exec_command", "apply_patch":
		return true
	default:
		return false
	}
}

func splitRolloutToolName(name string) (string, string) {
	name = strings.TrimSpace(name)
	index := strings.Index(name, ".")
	if index <= 0 || index+1 >= len(name) {
		return "", ""
	}
	return name[:index], name[index+1:]
}

func joinRolloutNamespaceTool(namespace string, tool string) string {
	namespace = strings.TrimSpace(namespace)
	tool = strings.TrimSpace(tool)
	if namespace == "" {
		return tool
	}
	if tool == "" {
		return namespace
	}
	return namespace + "." + tool
}

func rolloutChangesFromAny(value any) []map[string]any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []map[string]any:
		return append([]map[string]any(nil), typed...)
	case []any:
		changes := make([]map[string]any, 0, len(typed))
		for _, entry := range typed {
			change := rolloutChangeFromAny(entry)
			if len(change) > 0 {
				changes = append(changes, change)
			}
		}
		return changes
	case map[string]any:
		changes := make([]map[string]any, 0, len(typed))
		for path, rawChange := range typed {
			change := rolloutChangeFromAny(rawChange)
			if stringFromMap(change, "path") == "" {
				change["path"] = path
			}
			if len(change) > 0 {
				changes = append(changes, change)
			}
		}
		return changes
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var array []map[string]any
		if err := json.Unmarshal(data, &array); err == nil {
			return rolloutChangesFromAny(array)
		}
		var object map[string]any
		if err := json.Unmarshal(data, &object); err == nil {
			return rolloutChangesFromAny(object)
		}
		return nil
	}
}

func rolloutChangeFromAny(value any) map[string]any {
	change := mapFromAny(value)
	if change == nil {
		return nil
	}
	out := cloneAnyMap(change)
	if _, ok := out["path"]; !ok {
		if path := firstNonEmptyString(stringFromMap(change, "filePath"), stringFromMap(change, "file_path"), stringFromMap(change, "file")); path != "" {
			out["path"] = path
		}
	}
	if _, ok := out["kind"]; !ok {
		out["kind"] = rolloutPatchKindFromChange(change)
	} else if kind := mapFromAny(out["kind"]); kind != nil {
		out["kind"] = normalizeRolloutPatchKind(kind)
	} else if kind := stringFromAny(out["kind"]); kind != "" {
		out["kind"] = normalizeRolloutPatchKind(map[string]any{"type": kind})
	}
	if _, ok := out["diff"]; !ok {
		if diff := firstNonBlankRawString(rawStringFromMap(change, "unified_diff"), rawStringFromMap(change, "unifiedDiff"), rawStringFromMap(change, "content")); diff != "" {
			out["diff"] = diff
		}
	}
	return out
}

func rolloutPatchKindFromChange(change map[string]any) map[string]any {
	if change == nil {
		return map[string]any{"type": "update"}
	}
	if kind := stringFromMap(change, "type"); kind != "" {
		return normalizeRolloutPatchKind(map[string]any{
			"type":      kind,
			"move_path": firstNonEmptyString(stringFromMap(change, "move_path"), stringFromMap(change, "movePath")),
		})
	}
	if _, ok := change["Add"]; ok {
		return map[string]any{"type": "add"}
	}
	if _, ok := change["add"]; ok {
		return map[string]any{"type": "add"}
	}
	if _, ok := change["Delete"]; ok {
		return map[string]any{"type": "delete"}
	}
	if _, ok := change["delete"]; ok {
		return map[string]any{"type": "delete"}
	}
	return normalizeRolloutPatchKind(map[string]any{
		"type":      "update",
		"move_path": firstNonEmptyString(stringFromMap(change, "move_path"), stringFromMap(change, "movePath")),
	})
}

func normalizeRolloutPatchKind(kind map[string]any) map[string]any {
	kindType := strings.TrimSpace(stringFromMap(kind, "type"))
	switch kindType {
	case "add", "delete":
		return map[string]any{"type": kindType}
	default:
		out := map[string]any{"type": "update"}
		if movePath := firstNonEmptyString(stringFromMap(kind, "move_path"), stringFromMap(kind, "movePath")); movePath != "" {
			out["move_path"] = movePath
		}
		return out
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonBlankRawString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func fallbackItemID(index int) string {
	return fmt.Sprintf("rollout-item-%d", index+1)
}

func errEmptyRollout(path string) error {
	return fmt.Errorf("rollout is empty: %s", path)
}

func errMissingSessionMeta(path string) error {
	return fmt.Errorf("rollout session metadata not found: %s", path)
}
