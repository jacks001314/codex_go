package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const realtimeContextAppendMaxBytes = 500

func dialRealtimeTransport(ctx context.Context, threadID string, backend *TransportBackendConfig, config *SessionConfig, callID string, initialize bool) (*realtimeTransportSession, error) {
	endpoint, err := realtimeWebsocketURL(backend, config, callID)
	if err != nil {
		return nil, err
	}
	headers := backend.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	if config.RealtimeSessionID != "" {
		headers.Set("x-session-id", config.RealtimeSessionID)
	}
	switch config.Version {
	case VersionV1:
		if headers.Get("openai-alpha") == "" {
			headers.Set("openai-alpha", "quicksilver=v1")
		}
	case VersionV3:
		if headers.Get("openai-alpha") == "" {
			headers.Set("openai-alpha", "quicksilver=v2")
		}
	}
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: backend.HTTPClient, HTTPHeader: headers})
	if err != nil {
		return nil, fmt.Errorf("connect realtime websocket: %w", err)
	}
	connectionCtx, cancel := context.WithCancel(ctx)
	connection := &realtimeTransportSession{threadID: threadID, config: *config, conn: conn, ctx: connectionCtx, cancel: cancel}
	if initialize || config.Version != VersionV3 {
		if err := connection.writeJSON(realtimeSessionUpdate(config)); err != nil {
			connection.cancel()
			conn.CloseNow()
			return nil, fmt.Errorf("initialize realtime websocket: %w", err)
		}
	}
	if initialize && config.Version == VersionV3 {
		for {
			messageType, payload, err := conn.Read(connectionCtx)
			if err != nil {
				connection.cancel()
				conn.CloseNow()
				return nil, fmt.Errorf("wait for realtime session start: %w", err)
			}
			if messageType == websocket.MessageBinary {
				connection.cancel()
				conn.CloseNow()
				return nil, fmt.Errorf("start realtime session: unexpected binary realtime websocket event")
			}
			event, ok := parseRealtimeWireEvent(config.Version, payload)
			if !ok {
				continue
			}
			if event.Type == "error" {
				connection.cancel()
				conn.CloseNow()
				return nil, fmt.Errorf("%s", event.Message)
			}
			if event.Type == "session.updated" {
				connection.pendingEvents = append(connection.pendingEvents, event)
				break
			}
			connection.cancel()
			conn.CloseNow()
			return nil, fmt.Errorf("frameless realtime session received an event before session.started")
		}
	}
	return connection, nil
}

func realtimeWebsocketURL(backend *TransportBackendConfig, config *SessionConfig, callID string) (string, error) {
	base := backend.WebsocketBaseURL
	if callID != "" {
		base = backend.SidebandBaseURL
	}
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid realtime websocket base URL %q", base)
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported realtime websocket URL scheme %q", parsed.Scheme)
	}
	cleaned := strings.TrimSuffix(parsed.Path, "/")
	if config.Version == VersionV3 {
		switch {
		case cleaned == "", cleaned == "/", cleaned == "/v1":
			parsed.Path = "/v1/live"
		case strings.HasSuffix(cleaned, "/realtime"):
			parsed.Path = strings.TrimSuffix(cleaned, "/realtime") + "/live"
		default:
			parsed.Path = cleaned
		}
		if callID != "" {
			parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/" + url.PathEscape(callID)
		}
	} else {
		switch {
		case cleaned == "", cleaned == "/":
			parsed.Path = "/v1/realtime"
		case cleaned == "/v1", strings.HasSuffix(cleaned, "/v1"):
			parsed.Path = cleaned + "/realtime"
		case strings.HasSuffix(cleaned, "/realtime"):
			parsed.Path = cleaned
		default:
			parsed.Path = cleaned + "/realtime"
		}
	}
	query := parsed.Query()
	if callID != "" && config.Version != VersionV3 {
		query.Set("call_id", callID)
	} else if callID == "" {
		if config.Version == VersionV1 {
			query.Set("intent", "quicksilver")
		}
		if config.Model != "" {
			query.Set("model", config.Model)
		}
	}
	for key, value := range backend.QueryParams {
		if key == "intent" || (key == "model" && config.Model != "") {
			continue
		}
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (s *realtimeTransportSession) writeJSON(value any) error {
	if s == nil || s.conn == nil {
		return fmt.Errorf("realtime transport is unavailable")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.Write(s.ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("write realtime transport: %w", err)
	}
	return nil
}

func (s *realtimeTransportSession) close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.closeOnce.Do(func() {
		s.writeMu.Lock()
		if s.config.Version == VersionV3 {
			_ = s.conn.Write(s.ctx, websocket.MessageText, []byte(`{"type":"session.close"}`))
		}
		closeErr = s.conn.Close(websocket.StatusNormalClosure, "")
		s.writeMu.Unlock()
		s.cancel()
	})
	return closeErr
}

func realtimeSessionUpdate(config *SessionConfig) map[string]any {
	instructions := config.Prompt
	switch config.Version {
	case VersionV1:
		return map[string]any{"type": "session.update", "session": map[string]any{
			"type": "quicksilver", "instructions": instructions,
			"audio": map[string]any{"input": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}}, "output": map[string]any{"voice": config.Voice}},
		}}
	case VersionV3:
		session := map[string]any{
			"instructions": instructions,
			"audio":        map[string]any{"output": map[string]any{"voice": config.Voice}},
			"delegation":   map[string]any{"type": "client"},
		}
		if config.Model != "" {
			session["model"] = config.Model
		}
		if len(config.InitialItems) > 0 {
			items := make([]any, 0, len(config.InitialItems))
			for _, item := range config.InitialItems {
				contentType := "input_text"
				if item.Role == RoleAssistant {
					contentType = "output_text"
				}
				items = append(items, map[string]any{"type": "message", "role": item.Role, "content": []any{map[string]any{"type": contentType, "text": item.Text}}})
			}
			session["initial_items"] = items
		}
		return map[string]any{"type": "session.update", "session": session}
	default:
		return map[string]any{"type": "session.update", "session": realtimeV2Session(instructions, config)}
	}
}

func realtimeV2Session(instructions string, config *SessionConfig) map[string]any {
	const backgroundAgentDescription = "Send a user request to the background agent. Use this as the default action. Do not rephrase the user's ask or rewrite it in your own words; pass along the user's own words. If the background agent is idle, this starts a new task and returns the final result to the user. If the background agent is already working on a task, this sends the request as guidance to steer that previous task. If the user asks to do something next, later, after this, or once current work finishes, call this tool so the work is actually queued instead of merely promising to do it later."
	const remainSilentDescription = "Call this when the best response is to say nothing. Use it instead of speaking after hidden system/control messages, after background agent updates in silent modes, or whenever acknowledging aloud would be distracting. This tool has no user-visible effect."
	if config.SessionMode == SessionModeTranscription {
		return map[string]any{
			"type": "transcription",
			"audio": map[string]any{
				"input": map[string]any{
					"format":        map[string]any{"type": "audio/pcm", "rate": 24000},
					"transcription": map[string]any{"model": "gpt-4o-mini-transcribe"},
				},
			},
		}
	}
	return map[string]any{
		"type": "realtime", "instructions": instructions, "output_modalities": []string{string(config.OutputModality)},
		"audio": map[string]any{
			"input": map[string]any{
				"format":          map[string]any{"type": "audio/pcm", "rate": 24000},
				"noise_reduction": map[string]any{"type": "near_field"},
				"transcription":   map[string]any{"model": "gpt-4o-mini-transcribe"},
				"turn_detection":  map[string]any{"type": "server_vad", "interrupt_response": true, "create_response": true, "silence_duration_ms": 500},
			},
			"output": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}, "voice": config.Voice},
		},
		"tools": []any{
			map[string]any{"type": "function", "name": "background_agent", "description": backgroundAgentDescription, "parameters": map[string]any{"type": "object", "properties": map[string]any{"prompt": map[string]any{"type": "string", "description": "The user request to delegate to the background agent."}}, "required": []string{"prompt"}, "additionalProperties": false}},
			map[string]any{"type": "function", "name": "remain_silent", "description": remainSilentDescription, "parameters": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
		},
		"tool_choice": "auto",
	}
}

func (s *realtimeTransportSession) sendAudio(audio AudioChunk) error {
	messageType := "input_audio_buffer.append"
	if s.config.Version == VersionV3 {
		messageType = "input_audio.append"
	}
	return s.writeJSON(map[string]any{"type": messageType, "audio": audio.Data})
}

func (s *realtimeTransportSession) sendText(text string, role TextRole) error {
	if role == "" {
		role = RoleUser
	}
	if s.config.Version == VersionV2 && role == RoleUser && text != "" && !strings.HasPrefix(text, "[USER] ") {
		text = "[USER] " + text
	}
	return s.sendConversationText(text, role)
}

func (s *realtimeTransportSession) sendConversationText(text string, role TextRole) error {
	if s.config.Version == VersionV3 {
		for _, chunk := range realtimeContextChunks(text) {
			if err := s.writeJSON(realtimeContextAppend("session.context.append", "", chunk, "")); err != nil {
				return err
			}
		}
		return nil
	}
	contentType := "input_text"
	if role == RoleAssistant {
		contentType = "output_text"
	}
	return s.writeJSON(map[string]any{"type": "conversation.item.create", "item": map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": contentType, "text": text}}}})
}

func (s *realtimeTransportSession) sendSpeech(text string) error {
	if s.config.Version == VersionV2 && text != "" && !strings.HasPrefix(text, "[BACKEND] ") {
		text = "[BACKEND] " + text
	}
	text = truncateRealtimeOutput(text)
	switch s.config.Version {
	case VersionV1:
		return s.writeJSON(map[string]any{"type": "conversation.handoff.append", "handoff_id": "codex", "output_text": text})
	case VersionV3:
		for _, chunk := range realtimeContextChunks(text) {
			if err := s.writeJSON(realtimeContextAppend("session.context.append", "", chunk, "speakable")); err != nil {
				return err
			}
		}
		return nil
	default:
		if err := s.sendConversationText(text, RoleUser); err != nil {
			return err
		}
		return s.requestResponseCreate()
	}
}

func (s *realtimeTransportSession) activeHandoff() string {
	if s == nil {
		return ""
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.activeHandoffID
}

func (s *realtimeTransportSession) sendCodexStreamChunk(text, phase string) error {
	if s == nil || s.config.Version != VersionV3 {
		return nil
	}
	handoffID := s.activeHandoff()
	if handoffID == "" {
		return nil
	}
	channel := s.codexOutputChannel(phase)
	for _, chunk := range realtimeContextChunks(text) {
		if err := s.writeJSON(realtimeContextAppend("delegation.context.append", handoffID, chunk, channel)); err != nil {
			return err
		}
	}
	return nil
}

func (s *realtimeTransportSession) sendCodexOutput(text, phase string) error {
	if s == nil || text == "" || s.config.ClientManagedHandoffs {
		return nil
	}
	if s.config.Version == VersionV3 && s.config.CodexResponseHandoffMode == HandoffModeBemTags {
		phase = bemMessagePhase(text, s.config.CodexResponseHandoffChannelPrefixes)
		if phase == "" {
			phase = "final_answer"
		}
	}
	if s.config.Version == VersionV2 && !strings.HasPrefix(text, "[BACKEND] ") {
		text = "[BACKEND] " + text
	}
	text = truncateRealtimeOutput(text)
	handoffID := s.activeHandoff()
	if s.config.CodexResponsesAsItems {
		if prefix := s.config.CodexResponseItemPrefix; prefix != "" {
			text = truncateRealtimeOutput(prefix + "\n\n" + text)
		}
		if handoffID != "" {
			s.stateMu.Lock()
			s.lastCodexOutput = text
			s.stateMu.Unlock()
		}
		return s.sendCodexConversationItem(text, RoleDeveloper, s.codexOutputChannel(phase))
	}
	if handoffID == "" {
		switch s.config.Version {
		case VersionV1:
			if phase != "commentary" {
				text = agentFinalMessagePrefix + text
			}
			return s.writeJSON(map[string]any{"type": "conversation.handoff.append", "handoff_id": "codex", "output_text": text})
		case VersionV3:
			return s.sendV3SessionContext(text, s.codexOutputChannel(phase))
		default:
			if err := s.sendConversationText(text, RoleUser); err != nil {
				return err
			}
			return s.requestResponseCreate()
		}
	}
	s.stateMu.Lock()
	s.lastCodexOutput = text
	s.stateMu.Unlock()
	switch s.config.Version {
	case VersionV1:
		if phase != "commentary" {
			text = agentFinalMessagePrefix + text
		}
		return s.writeJSON(map[string]any{"type": "conversation.handoff.append", "handoff_id": handoffID, "output_text": text})
	case VersionV3:
		for _, chunk := range realtimeContextChunks(text) {
			if err := s.writeJSON(realtimeContextAppend("delegation.context.append", handoffID, chunk, s.codexOutputChannel(phase))); err != nil {
				return err
			}
		}
		return nil
	default:
		return s.sendConversationText(text, RoleUser)
	}
}

func (s *realtimeTransportSession) completeHandoff() error {
	if s == nil {
		return nil
	}
	s.stateMu.Lock()
	handoffID := s.activeHandoffID
	lastOutput := s.lastCodexOutput
	s.activeHandoffID = ""
	s.lastCodexOutput = ""
	s.stateMu.Unlock()
	if s.config.ClientManagedHandoffs || s.config.Version != VersionV2 || handoffID == "" || lastOutput == "" {
		return nil
	}
	output := "Background agent finished. Use the preceding [BACKEND] messages as the result."
	if s.config.CodexResponsesAsItems {
		output = ""
	}
	if err := s.sendV2FunctionOutput(handoffID, output); err != nil {
		return err
	}
	if !s.config.CodexResponsesAsItems {
		return s.requestResponseCreate()
	}
	return nil
}

func (s *realtimeTransportSession) sendV2FunctionOutput(callID, output string) error {
	return s.writeJSON(map[string]any{"type": "conversation.item.create", "item": map[string]any{"type": "function_call_output", "call_id": callID, "output": output}})
}

func (s *realtimeTransportSession) requestResponseCreate() error {
	if s == nil {
		return nil
	}
	s.stateMu.Lock()
	if s.responseActive {
		s.responseCreatePending = true
		s.stateMu.Unlock()
		return nil
	}
	s.responseActive = true
	s.stateMu.Unlock()
	return s.writeJSON(map[string]any{"type": "response.create"})
}

func (s *realtimeTransportSession) sendCodexConversationItem(text string, role TextRole, channel string) error {
	if s.config.Version == VersionV3 {
		return s.sendV3SessionContext(text, channel)
	}
	return s.sendConversationText(text, role)
}

func (s *realtimeTransportSession) sendV3SessionContext(text, channel string) error {
	for _, chunk := range realtimeContextChunks(text) {
		if err := s.writeJSON(realtimeContextAppend("session.context.append", "", chunk, channel)); err != nil {
			return err
		}
	}
	return nil
}

func (s *realtimeTransportSession) codexOutputChannel(phase string) string {
	switch s.config.CodexResponseHandoffMode {
	case HandoffModeCommentary:
		return "commentary"
	case HandoffModeBemTags:
		if phase == "commentary" {
			return "commentary"
		}
		return "speakable"
	default:
		return ""
	}
}

func truncateRealtimeOutput(text string) string {
	const tokenBudget = 1000
	truncationBudget := tokenBudget
	for {
		candidate := truncateRealtimeMiddleTokens(text, truncationBudget)
		candidateTokens := (len(candidate) + 3) / 4
		if candidateTokens <= tokenBudget {
			return candidate
		}
		excess := candidateTokens - tokenBudget
		if excess < 1 {
			excess = 1
		}
		truncationBudget -= excess
		if truncationBudget <= 0 {
			candidate = truncateRealtimeMiddleTokens(text, 0)
			if (len(candidate)+3)/4 <= tokenBudget {
				return candidate
			}
			return ""
		}
	}
}

func truncateRealtimeMiddleTokens(text string, maxTokens int) string {
	if text == "" {
		return ""
	}
	maxBytes := maxTokens * 4
	if maxTokens > 0 && len(text) <= maxBytes {
		return text
	}
	if maxBytes == 0 {
		return fmt.Sprintf("\u2026%d tokens truncated\u2026", (len(text)+3)/4)
	}
	leftBudget, rightBudget := maxBytes/2, maxBytes-maxBytes/2
	prefixEnd := 0
	for index, value := range text {
		end := index + utf8.RuneLen(value)
		if end > leftBudget {
			break
		}
		prefixEnd = end
	}
	tailTarget := len(text) - rightBudget
	if tailTarget < 0 {
		tailTarget = 0
	}
	suffixStart := len(text)
	for index := range text {
		if index >= tailTarget {
			suffixStart = index
			break
		}
	}
	if suffixStart < prefixEnd {
		suffixStart = prefixEnd
	}
	removedTokens := (len(text) - maxBytes + 3) / 4
	return text[:prefixEnd] + fmt.Sprintf("\u2026%d tokens truncated\u2026", removedTokens) + text[suffixStart:]
}

func realtimeContextAppend(messageType, handoffID, text, channel string) map[string]any {
	message := map[string]any{"type": messageType, "content": []any{map[string]any{"type": "input_text", "text": text}}}
	if handoffID != "" {
		message["delegation_item_id"] = handoffID
	}
	if channel != "" {
		message["channel"] = channel
	}
	return message
}

func realtimeContextChunks(text string) []string {
	if len(text) <= realtimeContextAppendMaxBytes {
		return []string{text}
	}
	chunks := make([]string, 0, (len(text)+realtimeContextAppendMaxBytes-1)/realtimeContextAppendMaxBytes)
	for start := 0; start < len(text); {
		end := start + realtimeContextAppendMaxBytes
		if end > len(text) {
			end = len(text)
		}
		for end > start && !utf8Boundary(text, end) {
			end--
		}
		chunks = append(chunks, text[start:end])
		start = end
	}
	return chunks
}

func utf8Boundary(text string, index int) bool {
	return index == 0 || index == len(text) || (text[index]&0xc0) != 0x80
}

func parseRealtimeWireEvent(version Version, payload []byte) (Event, bool) {
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return Event{}, false
	}
	typeName, _ := value["type"].(string)
	if typeName == "" {
		return Event{}, false
	}
	if typeName == "error" {
		message, ok := realtimeErrorMessage(value)
		return Event{Type: "error", Message: message}, ok
	}
	if typeName == "session.updated" || (version == VersionV3 && typeName == "session.started") {
		sessionValue, _ := value["session"].(map[string]any)
		sessionID, ok := sessionValue["id"].(string)
		if !ok {
			return Event{}, false
		}
		instructions, _ := sessionValue["instructions"].(string)
		return Event{Type: "session.updated", RealtimeSessionID: sessionID, Instructions: optionalStringWhenPresent(sessionValue, "instructions", instructions)}, true
	}
	if version == VersionV3 {
		return parseRealtimeV3Event(typeName, value)
	}
	return parseRealtimeLegacyEvent(version, typeName, value)
}

func parseRealtimeV3Event(typeName string, value map[string]any) (Event, bool) {
	switch typeName {
	case "output_audio.delta":
		data, ok := value["audio"].(string)
		return Event{Type: "audio.out", Audio: &AudioChunk{Data: data, SampleRate: 24000, NumChannels: 1}}, ok
	case "input_transcript.added", "output_transcript.added":
		item, _ := value["item"].(map[string]any)
		delta, ok := item["text"].(string)
		if !ok {
			return Event{}, false
		}
		role := "input_transcript.delta"
		if typeName == "output_transcript.added" {
			role = "output_transcript.delta"
		}
		return Event{Type: role, Delta: delta}, true
	case "turn.done":
		turn, _ := value["turn"].(map[string]any)
		role, roleOK := turn["role"].(string)
		text, textOK := turn["transcript"].(string)
		if !roleOK || !textOK {
			return Event{}, false
		}
		if role == "user" {
			return Event{Type: "input_transcript.done", Text: text}, true
		}
		if role == "assistant" {
			return Event{Type: "output_transcript.done", Text: text}, true
		}
	case "delegation.created":
		item, _ := value["item"].(map[string]any)
		if item["type"] != "delegation" || item["target"] != "client" {
			return Event{}, false
		}
		id, _ := item["id"].(string)
		content, ok := item["content"].([]any)
		if !ok {
			return Event{}, false
		}
		return Event{Type: "handoff.requested", HandoffID: id, ItemID: id, InputTranscript: joinedInputText(content)}, true
	}
	return Event{}, false
}

func parseRealtimeLegacyEvent(version Version, typeName string, value map[string]any) (Event, bool) {
	switch typeName {
	case "conversation.output_audio.delta":
		if version != VersionV1 {
			return Event{}, false
		}
		data, dataOK := firstMapStringOK(value, "delta", "data")
		rateValue, rateOK := mapUintOK(value["sample_rate"], math.MaxUint32)
		channelsValue, channelsOK := aliasedMapUintOK(value, "channels", "num_channels", math.MaxUint16)
		if !dataOK || !rateOK || !channelsOK {
			return Event{}, false
		}
		return Event{Type: "audio.out", Audio: &AudioChunk{Data: data, SampleRate: uint32(rateValue), NumChannels: uint16(channelsValue), SamplesPerChannel: optionalUint32(value["samples_per_channel"])}}, true
	case "response.output_audio.delta", "response.audio.delta":
		if version != VersionV2 {
			return Event{}, false
		}
		data, dataOK := firstMapStringOK(value, "delta")
		if !dataOK {
			return Event{}, false
		}
		rateValue, rateOK := mapUintOK(value["sample_rate"], math.MaxUint32)
		channelsValue, channelsOK := aliasedMapUintOK(value, "channels", "num_channels", math.MaxUint16)
		if !rateOK {
			rateValue = 24000
		}
		if !channelsOK {
			channelsValue = 1
		}
		return Event{Type: "audio.out", Audio: &AudioChunk{Data: data, SampleRate: uint32(rateValue), NumChannels: uint16(channelsValue), SamplesPerChannel: optionalUint32(value["samples_per_channel"]), ItemID: optionalString(value["item_id"])}}, true
	case "conversation.input_transcript.delta", "conversation.item.input_audio_transcription.delta":
		delta, ok := firstMapStringOK(value, "delta")
		return Event{Type: "input_transcript.delta", Delta: delta}, ok
	case "conversation.input_transcript.turn_marked", "conversation.item.input_audio_transcription.completed":
		transcript, ok := firstMapStringOK(value, "transcript")
		return Event{Type: "input_transcript.done", Text: transcript}, ok
	case "conversation.output_transcript.delta", "response.output_text.delta", "response.output_audio_transcript.delta":
		if typeName == "conversation.output_transcript.delta" && version != VersionV1 {
			return Event{}, false
		}
		delta, ok := firstMapStringOK(value, "delta")
		return Event{Type: "output_transcript.delta", Delta: delta}, ok
	case "response.output_text.done":
		if version != VersionV2 {
			return Event{}, false
		}
		transcript, ok := firstMapStringOK(value, "text")
		return Event{Type: "output_transcript.done", Text: transcript}, ok
	case "response.output_audio_transcript.done":
		transcript, ok := firstMapStringOK(value, "transcript")
		return Event{Type: "output_transcript.done", Text: transcript}, ok
	case "input_audio_buffer.speech_started":
		if version != VersionV2 {
			return Event{}, false
		}
		return Event{Type: typeName, ItemID: firstMapString(value, "item_id")}, true
	case "conversation.item.added", "conversation.item.created":
		if version == VersionV1 && typeName != "conversation.item.added" {
			return Event{}, false
		}
		item, _ := value["item"].(map[string]any)
		return Event{Type: "conversation.item.added", Item: item}, item != nil
	case "conversation.handoff.requested":
		if version != VersionV1 {
			return Event{}, false
		}
		handoffID, handoffOK := firstMapStringOK(value, "handoff_id")
		itemID, itemOK := firstMapStringOK(value, "item_id")
		input, inputOK := firstMapStringOK(value, "input_transcript")
		return Event{Type: "handoff.requested", HandoffID: handoffID, ItemID: itemID, InputTranscript: input}, handoffOK && itemOK && inputOK
	case "conversation.item.done":
		item, _ := value["item"].(map[string]any)
		if version == VersionV2 {
			if event, ok := v2FunctionCallEvent(item); ok {
				return event, true
			}
		}
		itemID, ok := firstMapStringOK(item, "id")
		return Event{Type: "conversation.item.done", ItemID: itemID}, ok
	case "response.cancelled":
		if version != VersionV2 {
			return Event{}, false
		}
		return Event{Type: typeName, ResponseID: responseID(value)}, true
	case "response.created", "response.done":
		if version != VersionV2 {
			return Event{}, false
		}
		return Event{Type: typeName, ResponseID: responseID(value)}, true
	}
	return Event{}, false
}

func v2FunctionCallEvent(item map[string]any) (Event, bool) {
	if item == nil || item["type"] != "function_call" {
		return Event{}, false
	}
	name, _ := item["name"].(string)
	callID, callIDOK := firstMapStringOK(item, "call_id", "id")
	if !callIDOK {
		return Event{}, false
	}
	itemID, itemIDOK := firstMapStringOK(item, "id")
	if !itemIDOK {
		itemID = callID
	}
	if name == "remain_silent" {
		return Event{Type: "noop.requested", HandoffID: callID, ItemID: itemID}, true
	}
	if name != "background_agent" {
		return Event{}, false
	}
	return Event{Type: "handoff.requested", HandoffID: callID, ItemID: itemID, InputTranscript: extractToolInput(firstMapString(item, "arguments"))}, true
}

func extractToolInput(arguments string) string {
	var value map[string]any
	if json.Unmarshal([]byte(arguments), &value) == nil {
		for _, key := range []string{"input_transcript", "input", "text", "prompt", "query"} {
			if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return arguments
}

func joinedInputText(items []any) string {
	var result strings.Builder
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["type"] == "input_text" {
			if text, ok := item["text"].(string); ok {
				result.WriteString(text)
			}
		}
	}
	return result.String()
}

func realtimeErrorMessage(value map[string]any) (string, bool) {
	if message, ok := value["message"].(string); ok {
		return message, true
	}
	if nested, ok := value["error"].(map[string]any); ok {
		if message, ok := nested["message"].(string); ok {
			return message, true
		}
	}
	if nested, ok := value["error"]; ok {
		encoded, err := json.Marshal(nested)
		if err == nil {
			return string(encoded), true
		}
	}
	return "", false
}

func responseID(value map[string]any) string {
	if response, ok := value["response"].(map[string]any); ok {
		if id, ok := response["id"].(string); ok {
			return id
		}
	}
	return firstMapString(value, "response_id")
}

func firstMapString(value map[string]any, keys ...string) string {
	text, _ := firstMapStringOK(value, keys...)
	return text
}

func firstMapStringOK(value map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if text, ok := value[key].(string); ok {
			return text, true
		}
	}
	return "", false
}

func aliasedMapUintOK(value map[string]any, primary, fallback string, max uint64) (uint64, bool) {
	raw, present := value[primary]
	if !present {
		raw = value[fallback]
	}
	return mapUintOK(raw, max)
}

func mapUintOK(value any, max uint64) (uint64, bool) {
	number, ok := value.(float64)
	if !ok || number < 0 || number > float64(max) || math.Trunc(number) != number {
		return 0, false
	}
	return uint64(number), true
}

func optionalStringWhenPresent(value map[string]any, key string, text string) *string {
	if _, ok := value[key].(string); !ok {
		return nil
	}
	return &text
}

func optionalUint32(value any) *uint32 {
	number, ok := mapUintOK(value, math.MaxUint32)
	if !ok {
		return nil
	}
	converted := uint32(number)
	return &converted
}

func optionalString(value any) *string {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	return &text
}
