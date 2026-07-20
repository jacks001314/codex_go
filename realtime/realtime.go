package realtime

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	MethodStart        Method = "thread/realtime/start"
	MethodAppendAudio  Method = "thread/realtime/appendAudio"
	MethodAppendText   Method = "thread/realtime/appendText"
	MethodAppendSpeech Method = "thread/realtime/appendSpeech"
	MethodStop         Method = "thread/realtime/stop"
	MethodListVoices   Method = "thread/realtime/listVoices"

	NotificationStarted          NotificationMethod = "thread/realtime/started"
	NotificationItemAdded        NotificationMethod = "thread/realtime/itemAdded"
	NotificationTranscriptDelta  NotificationMethod = "thread/realtime/transcript/delta"
	NotificationTranscriptDone   NotificationMethod = "thread/realtime/transcript/done"
	NotificationOutputAudioDelta NotificationMethod = "thread/realtime/outputAudio/delta"
	NotificationSDP              NotificationMethod = "thread/realtime/sdp"
	NotificationError            NotificationMethod = "thread/realtime/error"
	NotificationClosed           NotificationMethod = "thread/realtime/closed"
)

var (
	ErrInvalidRealtimeRequest = errors.New("invalid realtime request")
	ErrRealtimeNotRunning     = errors.New("realtime session is not running")
	ErrRealtimeAlreadyRunning = errors.New("realtime session is already running")
)

type Method string

type NotificationMethod string

type OutputModality string

const (
	OutputText  OutputModality = "text"
	OutputAudio OutputModality = "audio"
)

type Version string

const (
	VersionV1 Version = "v1"
	VersionV2 Version = "v2"
)

type Voice string

const (
	VoiceAlloy   Voice = "alloy"
	VoiceArbor   Voice = "arbor"
	VoiceAsh     Voice = "ash"
	VoiceBallad  Voice = "ballad"
	VoiceBreeze  Voice = "breeze"
	VoiceCedar   Voice = "cedar"
	VoiceCoral   Voice = "coral"
	VoiceCove    Voice = "cove"
	VoiceEcho    Voice = "echo"
	VoiceEmber   Voice = "ember"
	VoiceJuniper Voice = "juniper"
	VoiceMaple   Voice = "maple"
	VoiceMarin   Voice = "marin"
	VoiceSage    Voice = "sage"
	VoiceShimmer Voice = "shimmer"
	VoiceSol     Voice = "sol"
	VoiceSpruce  Voice = "spruce"
	VoiceVale    Voice = "vale"
	VoiceVerse   Voice = "verse"
)

type TextRole string

const (
	RoleUser      TextRole = "user"
	RoleDeveloper TextRole = "developer"
	RoleAssistant TextRole = "assistant"
)

type CodexResponseHandoffMode string

const (
	HandoffModeThinking CodexResponseHandoffMode = "thinking"
	HandoffModeBemTags  CodexResponseHandoffMode = "bemTags"
)

type InitialTextItem struct {
	Role TextRole `json:"role"`
	Text string   `json:"text"`
}

type AudioChunk struct {
	Data              string  `json:"data"`
	SampleRate        uint32  `json:"sampleRate"`
	NumChannels       uint16  `json:"numChannels"`
	SamplesPerChannel *uint32 `json:"samplesPerChannel,omitempty"`
	ItemID            *string `json:"itemId,omitempty"`
}

func (c *AudioChunk) Validate() error {
	if c == nil {
		return fmt.Errorf("%w: audio is required", ErrInvalidRealtimeRequest)
	}
	if strings.TrimSpace(c.Data) == "" {
		return fmt.Errorf("%w: audio data is required", ErrInvalidRealtimeRequest)
	}
	if _, err := base64.StdEncoding.DecodeString(c.Data); err != nil {
		return fmt.Errorf("%w: audio data must be base64", ErrInvalidRealtimeRequest)
	}
	if c.SampleRate == 0 {
		return fmt.Errorf("%w: sampleRate is required", ErrInvalidRealtimeRequest)
	}
	if c.NumChannels == 0 {
		return fmt.Errorf("%w: numChannels is required", ErrInvalidRealtimeRequest)
	}
	return nil
}

type StartTransport struct {
	Type string `json:"type"`
	SDP  string `json:"sdp,omitempty"`
}

func WebsocketTransport() *StartTransport {
	return &StartTransport{Type: "websocket"}
}

func WebRTCTransport(sdp string) *StartTransport {
	return &StartTransport{Type: "webrtc", SDP: sdp}
}

func (t *StartTransport) Validate() error {
	if t == nil {
		return nil
	}
	switch t.Type {
	case "", "websocket":
		return nil
	case "webrtc":
		if strings.TrimSpace(t.SDP) == "" {
			return fmt.Errorf("%w: webrtc sdp is required", ErrInvalidRealtimeRequest)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported transport %q", ErrInvalidRealtimeRequest, t.Type)
	}
}

type StartParams struct {
	ThreadID                   string                    `json:"threadId"`
	ClientManagedHandoffs      *bool                     `json:"clientManagedHandoffs,omitempty"`
	CodexResponsesAsItems      *bool                     `json:"codexResponsesAsItems,omitempty"`
	CodexResponseItemPrefix    *string                   `json:"codexResponseItemPrefix,omitempty"`
	CodexResponseHandoffPrefix *string                   `json:"codexResponseHandoffPrefix,omitempty"`
	CodexResponseHandoffMode   *CodexResponseHandoffMode `json:"codexResponseHandoffMode,omitempty"`
	Model                      *string                   `json:"model,omitempty"`
	OutputModality             OutputModality            `json:"outputModality"`
	IncludeStartupContext      *bool                     `json:"includeStartupContext,omitempty"`
	InitialItems               []InitialTextItem         `json:"initialItems,omitempty"`
	Prompt                     OptionalString            `json:"prompt,omitempty"`
	RealtimeSessionID          *string                   `json:"realtimeSessionId,omitempty"`
	Transport                  *StartTransport           `json:"transport,omitempty"`
	Version                    *Version                  `json:"version,omitempty"`
	Voice                      *Voice                    `json:"voice,omitempty"`
}

func (p *StartParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRealtimeRequest)
	}
	switch p.OutputModality {
	case OutputText, OutputAudio:
	case "":
		return fmt.Errorf("%w: outputModality is required", ErrInvalidRealtimeRequest)
	default:
		return fmt.Errorf("%w: unsupported outputModality %q", ErrInvalidRealtimeRequest, p.OutputModality)
	}
	if p.Version != nil && *p.Version != VersionV1 && *p.Version != VersionV2 {
		return fmt.Errorf("%w: unsupported version %q", ErrInvalidRealtimeRequest, *p.Version)
	}
	if err := p.Transport.Validate(); err != nil {
		return err
	}
	if len(p.InitialItems) > 128 {
		return fmt.Errorf("%w: initialItems must contain at most 128 items", ErrInvalidRealtimeRequest)
	}
	totalBytes := 0
	for _, item := range p.InitialItems {
		if item.Role != RoleUser && item.Role != RoleDeveloper && item.Role != RoleAssistant {
			return fmt.Errorf("%w: unsupported initial item role %q", ErrInvalidRealtimeRequest, item.Role)
		}
		if strings.TrimSpace(item.Text) == "" {
			return fmt.Errorf("%w: initial item text is required", ErrInvalidRealtimeRequest)
		}
		totalBytes += len(item.Text)
	}
	if totalBytes > 8192*4 {
		return fmt.Errorf("%w: initialItems text exceeds limit", ErrInvalidRealtimeRequest)
	}
	return nil
}

func (p *StartParams) Normalized(defaultModel string, defaultVersion Version, defaultVoice Voice) (*SessionConfig, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	model := defaultModel
	if p.Model != nil && *p.Model != "" {
		model = *p.Model
	}
	version := defaultVersion
	if version == "" {
		version = VersionV2
	}
	if p.Version != nil {
		version = *p.Version
	}
	voice := defaultVoice
	if voice == "" {
		voices := BuiltinVoices()
		voice = voices.DefaultForVersion(version)
	}
	if p.Voice != nil {
		voice = *p.Voice
	}
	includeStartupContext := true
	if p.IncludeStartupContext != nil {
		includeStartupContext = *p.IncludeStartupContext
	}
	clientManagedHandoffs := false
	if p.ClientManagedHandoffs != nil {
		clientManagedHandoffs = *p.ClientManagedHandoffs
	}
	codexResponsesAsItems := false
	if p.CodexResponsesAsItems != nil {
		codexResponsesAsItems = *p.CodexResponsesAsItems
	}
	transport := p.Transport
	if transport == nil {
		transport = WebsocketTransport()
	}
	return &SessionConfig{
		ThreadID:                   p.ThreadID,
		RealtimeSessionID:          stringValue(p.RealtimeSessionID),
		Model:                      model,
		OutputModality:             p.OutputModality,
		Version:                    version,
		Voice:                      voice,
		Transport:                  *transport,
		IncludeStartupContext:      includeStartupContext,
		PromptSet:                  p.Prompt.Set,
		Prompt:                     stringValue(p.Prompt.Value),
		ClientManagedHandoffs:      clientManagedHandoffs,
		CodexResponsesAsItems:      codexResponsesAsItems,
		CodexResponseItemPrefix:    stringValue(p.CodexResponseItemPrefix),
		CodexResponseHandoffPrefix: stringValue(p.CodexResponseHandoffPrefix),
		CodexResponseHandoffMode:   handoffModeValue(p.CodexResponseHandoffMode),
		InitialItems:               append([]InitialTextItem(nil), p.InitialItems...),
	}, nil
}

func handoffModeValue(value *CodexResponseHandoffMode) CodexResponseHandoffMode {
	if value == nil || *value == "" {
		return HandoffModeThinking
	}
	return *value
}

type OptionalString struct {
	Set   bool
	Value *string
}

func (o *OptionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	data = []byte(strings.TrimSpace(string(data)))
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

func (o *OptionalString) MarshalJSON() ([]byte, error) {
	if o == nil || !o.Set || o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.Value)
}

func (o *OptionalString) IsZero() bool {
	return o == nil || !o.Set
}

type StartResponse struct{}

type AppendAudioParams struct {
	ThreadID string     `json:"threadId"`
	Audio    AudioChunk `json:"audio"`
}

func (p *AppendAudioParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRealtimeRequest)
	}
	return p.Audio.Validate()
}

type AppendAudioResponse struct{}

type AppendTextParams struct {
	ThreadID string   `json:"threadId"`
	Text     string   `json:"text"`
	Role     TextRole `json:"role,omitempty"`
}

func (p *AppendTextParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRealtimeRequest)
	}
	if p.Text == "" {
		return fmt.Errorf("%w: text is required", ErrInvalidRealtimeRequest)
	}
	switch p.Role {
	case "", RoleUser, RoleDeveloper, RoleAssistant:
		return nil
	default:
		return fmt.Errorf("%w: unsupported text role %q", ErrInvalidRealtimeRequest, p.Role)
	}
}

type AppendTextResponse struct{}

type AppendSpeechParams struct {
	ThreadID string `json:"threadId"`
	Text     string `json:"text"`
}

func (p *AppendSpeechParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRealtimeRequest)
	}
	if p.Text == "" {
		return fmt.Errorf("%w: text is required", ErrInvalidRealtimeRequest)
	}
	return nil
}

type AppendSpeechResponse struct{}

type StopParams struct {
	ThreadID string `json:"threadId"`
}

func (p *StopParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRealtimeRequest)
	}
	return nil
}

type StopResponse struct{}

type ListVoicesParams struct{}

type VoicesList struct {
	V1        []Voice `json:"v1"`
	V2        []Voice `json:"v2"`
	DefaultV1 Voice   `json:"defaultV1"`
	DefaultV2 Voice   `json:"defaultV2"`
}

func BuiltinVoices() VoicesList {
	return VoicesList{
		V1: []Voice{
			VoiceJuniper,
			VoiceMaple,
			VoiceSpruce,
			VoiceEmber,
			VoiceVale,
			VoiceBreeze,
			VoiceArbor,
			VoiceSol,
			VoiceCove,
		},
		V2: []Voice{
			VoiceAlloy,
			VoiceAsh,
			VoiceBallad,
			VoiceCoral,
			VoiceEcho,
			VoiceSage,
			VoiceShimmer,
			VoiceVerse,
			VoiceMarin,
			VoiceCedar,
		},
		DefaultV1: VoiceCove,
		DefaultV2: VoiceMarin,
	}
}

func (v *VoicesList) DefaultForVersion(version Version) Voice {
	if v == nil {
		builtin := BuiltinVoices()
		return builtin.DefaultForVersion(version)
	}
	if version == VersionV1 {
		return v.DefaultV1
	}
	return v.DefaultV2
}

type ListVoicesResponse struct {
	Voices VoicesList `json:"voices"`
}

type SessionConfig struct {
	ThreadID                   string
	RealtimeSessionID          string
	Model                      string
	OutputModality             OutputModality
	Version                    Version
	Voice                      Voice
	Transport                  StartTransport
	IncludeStartupContext      bool
	PromptSet                  bool
	Prompt                     string
	ClientManagedHandoffs      bool
	CodexResponsesAsItems      bool
	CodexResponseItemPrefix    string
	CodexResponseHandoffPrefix string
	CodexResponseHandoffMode   CodexResponseHandoffMode
	InitialItems               []InitialTextItem
}

type SessionState struct {
	Config       SessionConfig
	StartedAt    time.Time
	LastActivity time.Time
	ClosedAt     *time.Time
	CloseReason  string
	AudioFrames  int
	TextInputs   int
	SpeechInputs int
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*SessionState
	streams  map[string]*codexOutputStream
	now      func() time.Time
}

const (
	codexOutputByteLimit        = 4000
	codexOutputHeadLimit        = 2000
	codexOutputTailLimit        = 1978
	codexOutputTruncationMarker = "\n…output truncated…\n"
	agentFinalMessagePrefix     = "\"Agent Final Message\":\n\n"
)

const codexOutputTruncationText = "\n…output truncated…\n"

type codexOutputStream struct {
	ThreadID  string
	ItemID    string
	Phase     string
	Sent      int
	Tail      string
	Truncated bool
}

func NewManager() *Manager {
	return &Manager{
		sessions: map[string]*SessionState{},
		streams:  map[string]*codexOutputStream{},
		now:      time.Now,
	}
}

func (m *Manager) BeginCodexOutput(threadID string, itemID string, phase string) {
	if m == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(itemID) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	state, ok := m.sessions[threadID]
	if !ok || state.ClosedAt != nil || state.Config.ClientManagedHandoffs || state.Config.CodexResponsesAsItems {
		return
	}
	m.streams[threadID+"\x00"+itemID] = &codexOutputStream{ThreadID: threadID, ItemID: itemID, Phase: strings.TrimSpace(phase)}
}

func (m *Manager) StreamCodexOutput(threadID string, itemID string, delta string) []Notification {
	if m == nil || delta == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	key := threadID + "\x00" + itemID
	stream := m.streams[key]
	if stream == nil {
		state, ok := m.sessions[threadID]
		if !ok || state.ClosedAt != nil || state.Config.ClientManagedHandoffs || state.Config.CodexResponsesAsItems {
			return nil
		}
		stream = &codexOutputStream{ThreadID: threadID, ItemID: itemID}
		m.streams[key] = stream
	}
	if stream.Truncated {
		stream.Tail = takeLastUTF8Bytes(stream.Tail+delta, codexOutputTailLimit)
		return nil
	}
	prefix := ""
	if stream.Sent == 0 && stream.Phase != "commentary" {
		prefix = agentFinalMessagePrefix
	}
	remaining := codexOutputByteLimit - stream.Sent - len(prefix)
	if len(delta) <= remaining {
		text := prefix + delta
		stream.Sent += len(text)
		return []Notification{codexHandoffNotification(threadID, itemID, stream.Phase, text, false)}
	}
	headBudget := codexOutputHeadLimit - stream.Sent - len(prefix)
	if headBudget < 0 {
		headBudget = 0
	}
	head := takeFirstUTF8Bytes(delta, headBudget)
	stream.Tail = takeLastUTF8Bytes(delta[len(head):], codexOutputTailLimit)
	stream.Truncated = true
	if head == "" {
		return nil
	}
	text := prefix + head
	stream.Sent += len(text)
	return []Notification{codexHandoffNotification(threadID, itemID, stream.Phase, text, false)}
}

func (m *Manager) FinishCodexOutput(threadID string, itemID string) []Notification {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := threadID + "\x00" + itemID
	stream := m.streams[key]
	delete(m.streams, key)
	if stream == nil || !stream.Truncated {
		return nil
	}
	return []Notification{codexHandoffNotification(threadID, itemID, stream.Phase, codexOutputTruncationText+stream.Tail, true)}
}

func codexHandoffNotification(threadID, itemID, phase, text string, final bool) Notification {
	channel := "thinking"
	if phase == "final_answer" {
		channel = "final"
	}
	return Notification{Method: NotificationItemAdded, Params: ItemAddedNotification{ThreadID: threadID, Item: map[string]any{
		"type": "handoff_append", "handoffId": "codex", "itemId": itemID, "phase": phase, "channel": channel, "text": text, "final": final,
	}}}
}

func takeFirstUTF8Bytes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(text) <= max {
		return text
	}
	end := max
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return text[:end]
}

func takeLastUTF8Bytes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(text) <= max {
		return text
	}
	start := len(text) - max
	for start < len(text) && !utf8.ValidString(text[start:]) {
		start++
	}
	return text[start:]
}

func (m *Manager) SetClock(clock func() time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if clock == nil {
		m.now = time.Now
		return
	}
	m.now = clock
}

func (m *Manager) Start(params *StartParams) (*SessionState, []Notification, error) {
	config, err := params.Normalized("", VersionV2, "")
	if err != nil {
		return nil, nil, err
	}
	if m == nil {
		return nil, nil, fmt.Errorf("%w: manager is nil", ErrInvalidRealtimeRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if state, ok := m.sessions[config.ThreadID]; ok && state.ClosedAt == nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrRealtimeAlreadyRunning, config.ThreadID)
	}
	now := m.now().UTC()
	state := &SessionState{Config: *config, StartedAt: now, LastActivity: now}
	m.sessions[config.ThreadID] = state
	notifications := []Notification{
		NewStartedNotification(config.ThreadID, config.RealtimeSessionID, config.Version),
	}
	for index, item := range config.InitialItems {
		notifications = append(notifications, Notification{
			Method: NotificationItemAdded,
			Params: ItemAddedNotification{ThreadID: config.ThreadID, Item: map[string]any{
				"type": "message", "itemId": fmt.Sprintf("initial-%d", index+1), "role": string(item.Role), "text": item.Text,
			}},
		})
	}
	if config.Transport.Type == "webrtc" {
		notifications = append(notifications, NewSDPNotification(config.ThreadID, "answer:"+config.Transport.SDP))
	}
	return cloneState(state), notifications, nil
}

func (m *Manager) AppendAudio(params *AppendAudioParams) (*SessionState, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	return m.update(params.ThreadID, func(state *SessionState) {
		state.AudioFrames++
	})
}

func (m *Manager) AppendText(params *AppendTextParams) (*SessionState, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	return m.update(params.ThreadID, func(state *SessionState) {
		state.TextInputs++
	})
}

func (m *Manager) AppendSpeech(params *AppendSpeechParams) (*SessionState, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	return m.update(params.ThreadID, func(state *SessionState) {
		state.SpeechInputs++
	})
}

func (m *Manager) Stop(params *StopParams, reason string) (*SessionState, Notification, error) {
	if err := params.Validate(); err != nil {
		return nil, Notification{}, err
	}
	if m == nil {
		return nil, Notification{}, fmt.Errorf("%w: manager is nil", ErrInvalidRealtimeRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	state, ok := m.sessions[params.ThreadID]
	if !ok || state.ClosedAt != nil {
		return nil, Notification{}, fmt.Errorf("%w: %s", ErrRealtimeNotRunning, params.ThreadID)
	}
	now := m.now().UTC()
	state.LastActivity = now
	state.ClosedAt = &now
	state.CloseReason = reason
	return cloneState(state), NewClosedNotification(params.ThreadID, reason), nil
}

func (m *Manager) State(threadID string) (*SessionState, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	state, ok := m.sessions[threadID]
	if !ok {
		return nil, false
	}
	return cloneState(state), true
}

func (m *Manager) ListVoices(*ListVoicesParams) *ListVoicesResponse {
	return &ListVoicesResponse{Voices: BuiltinVoices()}
}

func (m *Manager) update(threadID string, apply func(*SessionState)) (*SessionState, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: manager is nil", ErrInvalidRealtimeRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	state, ok := m.sessions[threadID]
	if !ok || state.ClosedAt != nil {
		return nil, fmt.Errorf("%w: %s", ErrRealtimeNotRunning, threadID)
	}
	apply(state)
	state.LastActivity = m.now().UTC()
	return cloneState(state), nil
}

func (m *Manager) ensureLocked() {
	if m.sessions == nil {
		m.sessions = map[string]*SessionState{}
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.streams == nil {
		m.streams = map[string]*codexOutputStream{}
	}
}

type Notification struct {
	Method NotificationMethod `json:"method"`
	Params any                `json:"params"`
}

type StartedNotification struct {
	ThreadID          string  `json:"threadId"`
	RealtimeSessionID *string `json:"realtimeSessionId"`
	Version           Version `json:"version"`
}

type ItemAddedNotification struct {
	ThreadID string         `json:"threadId"`
	Item     map[string]any `json:"item"`
}

type TranscriptDeltaNotification struct {
	ThreadID string `json:"threadId"`
	Role     string `json:"role"`
	Delta    string `json:"delta"`
}

type TranscriptDoneNotification struct {
	ThreadID string `json:"threadId"`
	Role     string `json:"role"`
	Text     string `json:"text"`
}

type OutputAudioDeltaNotification struct {
	ThreadID string     `json:"threadId"`
	Audio    AudioChunk `json:"audio"`
}

type SDPNotification struct {
	ThreadID string `json:"threadId"`
	SDP      string `json:"sdp"`
}

type ErrorNotification struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

type ClosedNotification struct {
	ThreadID string  `json:"threadId"`
	Reason   *string `json:"reason"`
}

func NewStartedNotification(threadID string, realtimeSessionID string, version Version) Notification {
	return Notification{
		Method: NotificationStarted,
		Params: StartedNotification{
			ThreadID:          threadID,
			RealtimeSessionID: stringPtrIfNotEmpty(realtimeSessionID),
			Version:           version,
		},
	}
}

func NewSDPNotification(threadID string, sdp string) Notification {
	return Notification{Method: NotificationSDP, Params: SDPNotification{ThreadID: threadID, SDP: sdp}}
}

func NewClosedNotification(threadID string, reason string) Notification {
	return Notification{Method: NotificationClosed, Params: ClosedNotification{ThreadID: threadID, Reason: stringPtrIfNotEmpty(reason)}}
}

type Event struct {
	Type             string
	Role             string
	Delta            string
	Text             string
	Message          string
	Audio            *AudioChunk
	Item             map[string]any
	ResponseID       string
	HandoffID        string
	ItemID           string
	InputTranscript  string
	ActiveTranscript string
}

func NotificationFromEvent(threadID string, event Event) (Notification, bool) {
	switch event.Type {
	case "input_audio_buffer.speech_started":
		itemID := event.ItemID
		return Notification{
			Method: NotificationItemAdded,
			Params: ItemAddedNotification{
				ThreadID: threadID,
				Item: map[string]any{
					"type":    "input_audio_buffer.speech_started",
					"item_id": itemID,
				},
			},
		}, true
	case "input_transcript.delta":
		return Notification{Method: NotificationTranscriptDelta, Params: TranscriptDeltaNotification{ThreadID: threadID, Role: "user", Delta: event.Delta}}, true
	case "input_transcript.done":
		return Notification{Method: NotificationTranscriptDone, Params: TranscriptDoneNotification{ThreadID: threadID, Role: "user", Text: event.Text}}, true
	case "output_transcript.delta":
		return Notification{Method: NotificationTranscriptDelta, Params: TranscriptDeltaNotification{ThreadID: threadID, Role: "assistant", Delta: event.Delta}}, true
	case "output_transcript.done":
		return Notification{Method: NotificationTranscriptDone, Params: TranscriptDoneNotification{ThreadID: threadID, Role: "assistant", Text: event.Text}}, true
	case "audio.out":
		if event.Audio == nil {
			return Notification{}, false
		}
		return Notification{Method: NotificationOutputAudioDelta, Params: OutputAudioDeltaNotification{ThreadID: threadID, Audio: *event.Audio}}, true
	case "response.cancelled":
		return Notification{
			Method: NotificationItemAdded,
			Params: ItemAddedNotification{
				ThreadID: threadID,
				Item: map[string]any{
					"type":        "response.cancelled",
					"response_id": event.ResponseID,
				},
			},
		}, true
	case "conversation.item.added":
		return Notification{Method: NotificationItemAdded, Params: ItemAddedNotification{ThreadID: threadID, Item: event.Item}}, true
	case "handoff.requested":
		return Notification{
			Method: NotificationItemAdded,
			Params: ItemAddedNotification{
				ThreadID: threadID,
				Item: map[string]any{
					"type":              "handoff_request",
					"handoff_id":        event.HandoffID,
					"item_id":           event.ItemID,
					"input_transcript":  event.InputTranscript,
					"active_transcript": event.ActiveTranscript,
				},
			},
		}, true
	case "error":
		return Notification{Method: NotificationError, Params: ErrorNotification{ThreadID: threadID, Message: event.Message}}, true
	default:
		return Notification{}, false
	}
}

func cloneState(state *SessionState) *SessionState {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
