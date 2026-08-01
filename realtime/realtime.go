package realtime

import (
	"context"
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
	VersionV3 Version = "v3"
)

type SessionMode string

const (
	SessionModeConversational SessionMode = "conversational"
	SessionModeTranscription  SessionMode = "transcription"
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
	HandoffModeThinking   CodexResponseHandoffMode = "thinking"
	HandoffModeCommentary CodexResponseHandoffMode = "commentary"
	HandoffModeBemTags    CodexResponseHandoffMode = "bemTags"
)

type InitialTextItem struct {
	Role TextRole `json:"role"`
	Text string   `json:"text"`
}

func (i *InitialTextItem) UnmarshalJSON(data []byte) error {
	if i == nil {
		return fmt.Errorf("initial item is required")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := requireJSONField(raw, "role"); err != nil {
		return err
	}
	if err := requireJSONField(raw, "text"); err != nil {
		return err
	}
	var decoded struct {
		Role TextRole `json:"role"`
		Text string   `json:"text"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Role != RoleUser && decoded.Role != RoleDeveloper && decoded.Role != RoleAssistant {
		return fmt.Errorf("unknown conversation text role %q", decoded.Role)
	}
	*i = InitialTextItem(decoded)
	return nil
}

type AudioChunk struct {
	Data              string  `json:"data"`
	SampleRate        uint32  `json:"sampleRate"`
	NumChannels       uint16  `json:"numChannels"`
	SamplesPerChannel *uint32 `json:"samplesPerChannel"`
	ItemID            *string `json:"itemId"`
}

func (c *AudioChunk) Validate() error {
	if c == nil {
		return fmt.Errorf("%w: audio is required", ErrInvalidRealtimeRequest)
	}
	return nil
}

func (c *AudioChunk) UnmarshalJSON(data []byte) error {
	if c == nil {
		return fmt.Errorf("audio is required")
	}
	var wire struct {
		Data              *string `json:"data"`
		SampleRate        *uint32 `json:"sampleRate"`
		NumChannels       *uint16 `json:"numChannels"`
		SamplesPerChannel *uint32 `json:"samplesPerChannel"`
		ItemID            *string `json:"itemId"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Data == nil {
		return fmt.Errorf("missing field data")
	}
	if wire.SampleRate == nil {
		return fmt.Errorf("missing field sampleRate")
	}
	if wire.NumChannels == nil {
		return fmt.Errorf("missing field numChannels")
	}
	*c = AudioChunk{
		Data:              *wire.Data,
		SampleRate:        *wire.SampleRate,
		NumChannels:       *wire.NumChannels,
		SamplesPerChannel: wire.SamplesPerChannel,
		ItemID:            wire.ItemID,
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
	case "websocket":
		return nil
	case "webrtc":
		return nil
	default:
		return fmt.Errorf("%w: unsupported transport %q", ErrInvalidRealtimeRequest, t.Type)
	}
}

func (t *StartTransport) UnmarshalJSON(data []byte) error {
	if t == nil {
		return fmt.Errorf("transport is required")
	}
	var wire struct {
		Type *string `json:"type"`
		SDP  *string `json:"sdp"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Type == nil {
		return fmt.Errorf("missing field type")
	}
	switch *wire.Type {
	case "websocket":
		*t = StartTransport{Type: "websocket"}
		return nil
	case "webrtc":
		if wire.SDP == nil {
			return fmt.Errorf("missing field sdp")
		}
		*t = StartTransport{Type: "webrtc", SDP: *wire.SDP}
		return nil
	default:
		return fmt.Errorf("unknown variant %s", *wire.Type)
	}
}

func (t StartTransport) MarshalJSON() ([]byte, error) {
	if t.Type == "webrtc" {
		return json.Marshal(struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		}{Type: t.Type, SDP: t.SDP})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
	}{Type: t.Type})
}

type StartParams struct {
	ThreadID                            string                    `json:"threadId"`
	ClientManagedHandoffs               *bool                     `json:"clientManagedHandoffs,omitempty"`
	FlushTranscriptTailOnEnd            *bool                     `json:"flushTranscriptTailOnSessionEnd,omitempty"`
	CodexResponsesAsItems               *bool                     `json:"codexResponsesAsItems,omitempty"`
	CodexResponseItemPrefix             *string                   `json:"codexResponseItemPrefix,omitempty"`
	CodexResponseHandoffMode            *CodexResponseHandoffMode `json:"codexResponseHandoffMode,omitempty"`
	CodexResponseHandoffChannelPrefixes map[string][]string       `json:"codexResponseHandoffChannelPrefixes,omitempty"`
	Model                               *string                   `json:"model,omitempty"`
	OutputModality                      OutputModality            `json:"outputModality"`
	IncludeStartupContext               *bool                     `json:"includeStartupContext,omitempty"`
	InitialItems                        []InitialTextItem         `json:"initialItems,omitempty"`
	Prompt                              OptionalString            `json:"prompt,omitempty"`
	RealtimeSessionID                   *string                   `json:"realtimeSessionId,omitempty"`
	Transport                           *StartTransport           `json:"transport,omitempty"`
	Version                             *Version                  `json:"version,omitempty"`
	Voice                               *Voice                    `json:"voice,omitempty"`
}

func (p StartParams) MarshalJSON() ([]byte, error) {
	var prompt *OptionalString
	if p.Prompt.Set {
		promptValue := p.Prompt
		prompt = &promptValue
	}
	return json.Marshal(struct {
		ThreadID                            string                    `json:"threadId"`
		ClientManagedHandoffs               *bool                     `json:"clientManagedHandoffs"`
		FlushTranscriptTailOnEnd            *bool                     `json:"flushTranscriptTailOnSessionEnd"`
		CodexResponsesAsItems               *bool                     `json:"codexResponsesAsItems"`
		CodexResponseItemPrefix             *string                   `json:"codexResponseItemPrefix"`
		CodexResponseHandoffMode            *CodexResponseHandoffMode `json:"codexResponseHandoffMode"`
		CodexResponseHandoffChannelPrefixes map[string][]string       `json:"codexResponseHandoffChannelPrefixes"`
		Model                               *string                   `json:"model"`
		OutputModality                      OutputModality            `json:"outputModality"`
		IncludeStartupContext               *bool                     `json:"includeStartupContext"`
		InitialItems                        []InitialTextItem         `json:"initialItems"`
		Prompt                              *OptionalString           `json:"prompt,omitempty"`
		RealtimeSessionID                   *string                   `json:"realtimeSessionId"`
		Transport                           *StartTransport           `json:"transport"`
		Version                             *Version                  `json:"version"`
		Voice                               *Voice                    `json:"voice"`
	}{
		ThreadID:                            p.ThreadID,
		ClientManagedHandoffs:               p.ClientManagedHandoffs,
		FlushTranscriptTailOnEnd:            p.FlushTranscriptTailOnEnd,
		CodexResponsesAsItems:               p.CodexResponsesAsItems,
		CodexResponseItemPrefix:             p.CodexResponseItemPrefix,
		CodexResponseHandoffMode:            p.CodexResponseHandoffMode,
		CodexResponseHandoffChannelPrefixes: p.CodexResponseHandoffChannelPrefixes,
		Model:                               p.Model,
		OutputModality:                      p.OutputModality,
		IncludeStartupContext:               p.IncludeStartupContext,
		InitialItems:                        p.InitialItems,
		Prompt:                              prompt,
		RealtimeSessionID:                   p.RealtimeSessionID,
		Transport:                           p.Transport,
		Version:                             p.Version,
		Voice:                               p.Voice,
	})
}

func (p *StartParams) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("start params are required")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := requireJSONField(raw, "threadId"); err != nil {
		return err
	}
	if err := requireJSONField(raw, "outputModality"); err != nil {
		return err
	}
	type startParamsAlias StartParams
	var decoded startParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.OutputModality != OutputText && decoded.OutputModality != OutputAudio {
		return fmt.Errorf("unknown realtime output modality %q", decoded.OutputModality)
	}
	if decoded.Version != nil && *decoded.Version != VersionV1 && *decoded.Version != VersionV2 && *decoded.Version != VersionV3 {
		return fmt.Errorf("unknown realtime version %q", *decoded.Version)
	}
	if decoded.CodexResponseHandoffMode != nil {
		switch *decoded.CodexResponseHandoffMode {
		case HandoffModeThinking, HandoffModeCommentary, HandoffModeBemTags:
		default:
			return fmt.Errorf("unknown Codex response handoff mode %q", *decoded.CodexResponseHandoffMode)
		}
	}
	if decoded.Voice != nil && !isKnownVoice(*decoded.Voice) {
		return fmt.Errorf("unknown realtime voice %q", *decoded.Voice)
	}
	*p = StartParams(decoded)
	return nil
}

func requireJSONField(raw map[string]json.RawMessage, name string) error {
	value, ok := raw[name]
	if !ok || strings.TrimSpace(string(value)) == "null" {
		return fmt.Errorf("missing field %s", name)
	}
	return nil
}

func isKnownVoice(voice Voice) bool {
	voices := BuiltinVoices()
	for _, candidate := range append(append([]Voice(nil), voices.V1...), voices.V2...) {
		if voice == candidate {
			return true
		}
	}
	return false
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
	if p.Version != nil && *p.Version != VersionV1 && *p.Version != VersionV2 && *p.Version != VersionV3 {
		return fmt.Errorf("%w: unsupported version %q", ErrInvalidRealtimeRequest, *p.Version)
	}
	if err := p.Transport.Validate(); err != nil {
		return err
	}
	if len(p.InitialItems) > 128 {
		return fmt.Errorf("%w: initial realtime items must contain no more than 128 items", ErrInvalidRealtimeRequest)
	}
	totalTokens := 0
	for _, item := range p.InitialItems {
		if item.Role != RoleUser && item.Role != RoleDeveloper && item.Role != RoleAssistant {
			return fmt.Errorf("%w: unsupported initial item role %q", ErrInvalidRealtimeRequest, item.Role)
		}
		itemTokens := approximateRealtimeTokens(item.Text)
		if itemTokens > 8192 {
			return fmt.Errorf("%w: each initial realtime item must not exceed 8192 estimated tokens", ErrInvalidRealtimeRequest)
		}
		totalTokens += itemTokens
	}
	if totalTokens > 8192 {
		return fmt.Errorf("%w: initial realtime items must not exceed 8192 estimated tokens in total", ErrInvalidRealtimeRequest)
	}
	if p.CodexResponseHandoffMode != nil {
		switch *p.CodexResponseHandoffMode {
		case HandoffModeThinking, HandoffModeCommentary, HandoffModeBemTags:
		default:
			return fmt.Errorf("%w: unsupported Codex response handoff mode %q", ErrInvalidRealtimeRequest, *p.CodexResponseHandoffMode)
		}
	}
	return nil
}

func (p *StartParams) validateVersion(version Version, transport *StartTransport) error {
	if version != VersionV3 && len(p.InitialItems) > 0 {
		return fmt.Errorf("%w: initial realtime items require realtime v3", ErrInvalidRealtimeRequest)
	}
	if version != VersionV2 && p.OutputModality == OutputText {
		return fmt.Errorf("%w: text realtime output modality requires realtime v2", ErrInvalidRealtimeRequest)
	}
	if transport != nil && transport.Type == "webrtc" && version == VersionV2 {
		return fmt.Errorf("%w: AVAS realtime calls require realtime v1 or v3", ErrInvalidRealtimeRequest)
	}
	return nil
}

func approximateRealtimeTokens(text string) int {
	return (len(text) + 3) / 4
}

func (p *StartParams) Normalized(defaultModel string, defaultVersion Version, defaultVoice Voice) (*SessionConfig, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	transport := p.Transport
	if transport == nil {
		transport = WebsocketTransport()
	}
	version := defaultVersion
	if version == "" {
		version = VersionV2
	}
	if p.Version != nil {
		version = *p.Version
	} else if transport.Type == "webrtc" {
		version = VersionV1
	}
	if err := p.validateVersion(version, transport); err != nil {
		return nil, err
	}
	model := defaultModel
	if model == "" {
		if version == VersionV3 {
			model = "gpt-live-1-boulder-alpha"
		} else {
			model = "gpt-realtime-1.5"
		}
	}
	if p.Model != nil && *p.Model != "" {
		model = *p.Model
	}
	voice := defaultVoice
	if voice == "" {
		voices := BuiltinVoices()
		voice = voices.DefaultForVersion(version)
	}
	if p.Voice != nil {
		voice = *p.Voice
	}
	voices := BuiltinVoices()
	if !voices.Supports(version, voice) {
		return nil, fmt.Errorf("%w: realtime voice %q is not supported for %s", ErrInvalidRealtimeRequest, voice, version)
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
	return &SessionConfig{
		ThreadID:                            p.ThreadID,
		RealtimeSessionID:                   firstNonEmptyString(stringValue(p.RealtimeSessionID), p.ThreadID),
		Model:                               model,
		OutputModality:                      p.OutputModality,
		Version:                             version,
		Voice:                               voice,
		Transport:                           *transport,
		IncludeStartupContext:               includeStartupContext,
		PromptSet:                           p.Prompt.Set,
		Prompt:                              stringValue(p.Prompt.Value),
		ClientManagedHandoffs:               clientManagedHandoffs,
		FlushTranscriptTailOnEnd:            boolValue(p.FlushTranscriptTailOnEnd),
		CodexResponsesAsItems:               codexResponsesAsItems,
		CodexResponseItemPrefix:             stringValue(p.CodexResponseItemPrefix),
		CodexResponseHandoffMode:            handoffModeValue(p.CodexResponseHandoffMode),
		CodexResponseHandoffChannelPrefixes: cloneStringSliceMap(p.CodexResponseHandoffChannelPrefixes),
		InitialItems:                        append([]InitialTextItem(nil), p.InitialItems...),
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

func (p *AppendAudioParams) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("append audio params are required")
	}
	var wire struct {
		ThreadID *string         `json:"threadId"`
		Audio    json.RawMessage `json:"audio"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.ThreadID == nil {
		return fmt.Errorf("missing field threadId")
	}
	if len(wire.Audio) == 0 || string(wire.Audio) == "null" {
		return fmt.Errorf("missing field audio")
	}
	var audio AudioChunk
	if err := json.Unmarshal(wire.Audio, &audio); err != nil {
		return err
	}
	*p = AppendAudioParams{ThreadID: *wire.ThreadID, Audio: audio}
	return nil
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
	Role     TextRole `json:"role"`
}

func (p *AppendTextParams) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("append text params are required")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := requireJSONField(raw, "threadId"); err != nil {
		return err
	}
	if err := requireJSONField(raw, "text"); err != nil {
		return err
	}
	var decoded struct {
		ThreadID string          `json:"threadId"`
		Text     string          `json:"text"`
		Role     json.RawMessage `json:"role"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	role := RoleUser
	if len(decoded.Role) > 0 {
		if err := json.Unmarshal(decoded.Role, &role); err != nil {
			return err
		}
		if role != RoleUser && role != RoleDeveloper && role != RoleAssistant {
			return fmt.Errorf("unknown conversation text role %q", role)
		}
	}
	*p = AppendTextParams{ThreadID: decoded.ThreadID, Text: decoded.Text, Role: role}
	return nil
}

func (p *AppendTextParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRealtimeRequest)
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

func (p *AppendSpeechParams) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("append speech params are required")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := requireJSONField(raw, "threadId"); err != nil {
		return err
	}
	if err := requireJSONField(raw, "text"); err != nil {
		return err
	}
	type appendSpeechAlias AppendSpeechParams
	var decoded appendSpeechAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = AppendSpeechParams(decoded)
	return nil
}

func (p *AppendSpeechParams) Validate() error {
	if p == nil || strings.TrimSpace(p.ThreadID) == "" {
		return fmt.Errorf("%w: threadId is required", ErrInvalidRealtimeRequest)
	}
	return nil
}

type AppendSpeechResponse struct{}

type StopParams struct {
	ThreadID string `json:"threadId"`
}

func (p *StopParams) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("stop params are required")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := requireJSONField(raw, "threadId"); err != nil {
		return err
	}
	type stopParamsAlias StopParams
	var decoded stopParamsAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = StopParams(decoded)
	return nil
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
	if version == VersionV1 || version == VersionV3 {
		return v.DefaultV1
	}
	return v.DefaultV2
}

func (v *VoicesList) Supports(version Version, voice Voice) bool {
	if v == nil {
		builtin := BuiltinVoices()
		return builtin.Supports(version, voice)
	}
	voices := v.V2
	if version == VersionV1 || version == VersionV3 {
		voices = v.V1
	}
	for _, candidate := range voices {
		if candidate == voice {
			return true
		}
	}
	return false
}

type ListVoicesResponse struct {
	Voices VoicesList `json:"voices"`
}

type SessionConfig struct {
	ThreadID                            string
	RealtimeSessionID                   string
	Model                               string
	OutputModality                      OutputModality
	Version                             Version
	Voice                               Voice
	SessionMode                         SessionMode
	Transport                           StartTransport
	IncludeStartupContext               bool
	PromptSet                           bool
	Prompt                              string
	ClientManagedHandoffs               bool
	FlushTranscriptTailOnEnd            bool
	CodexResponsesAsItems               bool
	CodexResponseItemPrefix             string
	CodexResponseHandoffMode            CodexResponseHandoffMode
	CodexResponseHandoffChannelPrefixes map[string][]string
	InitialItems                        []InitialTextItem
}

type StartOptions struct {
	Context        context.Context
	Backend        *TransportBackendConfig
	DefaultModel   string
	DefaultVersion Version
	DefaultVoice   Voice
	SessionMode    SessionMode
	Instructions   *string
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
	mu                 sync.Mutex
	startLocks         map[string]*sync.Mutex
	sessions           map[string]*SessionState
	streams            map[string]*codexOutputStream
	sidebands          map[string]*realtimeSideband
	connections        map[string]*realtimeTransportSession
	transport          *TransportBackendConfig
	notificationSink   func(Notification)
	eventSink          func(string, Event)
	now                func() time.Time
	shutdown           bool
	backgroundTaskWait sync.WaitGroup
}

const (
	codexOutputByteLimit        = 4000
	codexOutputTruncationMarker = "\n…output truncated…\n"
	codexOutputHeadLimit        = (codexOutputByteLimit - len(codexOutputTruncationMarker)) / 2
	codexOutputTailLimit        = codexOutputByteLimit - codexOutputHeadLimit - len(codexOutputTruncationMarker)
	agentFinalMessagePrefix     = "\"Agent Final Message\":\n\n"
	codexOutputFlushInterval    = 200 * time.Millisecond
)

const codexOutputTruncationText = codexOutputTruncationMarker

type codexOutputStream struct {
	ThreadID       string
	ItemID         string
	Phase          string
	Sent           int
	Buffered       string
	Tail           string
	Truncated      bool
	LastFlush      time.Time
	FlushScheduled bool
	Timer          *time.Timer
	BEM            *bemChannelParser
}

type bemChannelParser struct {
	prefixes map[string][]string
	buffered string
	phase    string
}

func newBEMChannelParser(prefixes map[string][]string) *bemChannelParser {
	return &bemChannelParser{prefixes: cloneStringSliceMap(prefixes)}
}

func (p *bemChannelParser) push(text string) (string, bool) {
	if p == nil {
		return text, true
	}
	if p.phase != "" {
		return text, true
	}
	p.buffered += text
	p.phase = bemMessagePhase(p.buffered, p.prefixes)
	if p.phase == "" {
		return "", false
	}
	text = p.buffered
	p.buffered = ""
	return text, true
}

func (p *bemChannelParser) finish() string {
	if p == nil {
		return ""
	}
	text := p.buffered
	p.buffered = ""
	if p.phase == "" && text != "" {
		p.phase = "final_answer"
	}
	return text
}

func bemMessagePhase(text string, prefixes map[string][]string) string {
	for _, candidate := range []struct {
		channel       string
		defaultPrefix string
		phase         string
	}{
		{channel: "analysis", defaultPrefix: "[ANALYSIS]", phase: "commentary"},
		{channel: "commentary", defaultPrefix: "[COMMENTARY]", phase: "commentary"},
		{channel: "final", defaultPrefix: "[FINAL]", phase: "final_answer"},
	} {
		configured, exists := prefixes[candidate.channel]
		if !exists {
			if strings.HasPrefix(text, candidate.defaultPrefix) {
				return candidate.phase
			}
			continue
		}
		for _, prefix := range configured {
			if prefix != "" && strings.HasPrefix(text, prefix) {
				return candidate.phase
			}
		}
	}
	return ""
}

func NewManager() *Manager {
	return &Manager{
		sessions:    map[string]*SessionState{},
		streams:     map[string]*codexOutputStream{},
		sidebands:   map[string]*realtimeSideband{},
		connections: map[string]*realtimeTransportSession{},
		startLocks:  map[string]*sync.Mutex{},
		now:         time.Now,
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
	if connection := m.connections[threadID]; connection != nil && (connection.config.Version != VersionV3 || connection.activeHandoff() == "") {
		return
	}
	key := threadID + "\x00" + itemID
	if previous := m.streams[key]; previous != nil && previous.Timer != nil {
		previous.Timer.Stop()
	}
	stream := &codexOutputStream{
		ThreadID:  threadID,
		ItemID:    itemID,
		Phase:     strings.TrimSpace(phase),
		LastFlush: time.Now(),
	}
	if state.Config.Version == VersionV3 && state.Config.CodexResponseHandoffMode == HandoffModeBemTags {
		stream.Phase = ""
		stream.BEM = newBEMChannelParser(state.Config.CodexResponseHandoffChannelPrefixes)
	}
	m.streams[key] = stream
}

func (m *Manager) StreamCodexOutput(threadID string, itemID string, delta string) []Notification {
	if m == nil || delta == "" {
		return nil
	}
	m.mu.Lock()
	m.ensureLocked()
	key := threadID + "\x00" + itemID
	stream := m.streams[key]
	if stream == nil {
		state, ok := m.sessions[threadID]
		if !ok || state.ClosedAt != nil || state.Config.ClientManagedHandoffs || state.Config.CodexResponsesAsItems {
			m.mu.Unlock()
			return nil
		}
		if connection := m.connections[threadID]; connection != nil && (connection.config.Version != VersionV3 || connection.activeHandoff() == "") {
			m.mu.Unlock()
			return nil
		}
		stream = &codexOutputStream{ThreadID: threadID, ItemID: itemID, LastFlush: time.Now()}
		if state.Config.Version == VersionV3 && state.Config.CodexResponseHandoffMode == HandoffModeBemTags {
			stream.BEM = newBEMChannelParser(state.Config.CodexResponseHandoffChannelPrefixes)
		}
		m.streams[key] = stream
	}
	connection := m.connections[threadID]
	if connection != nil {
		pushCodexStreamText(stream, delta)
		if stream.Buffered == "" || stream.FlushScheduled || codexStreamableBytes(stream) == 0 {
			m.mu.Unlock()
			return nil
		}
		delay := codexOutputFlushInterval - time.Since(stream.LastFlush)
		if delay < 0 {
			delay = 0
		}
		stream.FlushScheduled = true
		stream.Timer = time.AfterFunc(delay, func() {
			m.flushCodexOutputStream(key, stream)
		})
		m.mu.Unlock()
		return nil
	}
	if stream.Truncated {
		stream.Tail = takeLastUTF8Bytes(stream.Tail+delta, codexOutputTailLimit)
		m.mu.Unlock()
		return nil
	}
	prefix := ""
	if connection == nil && stream.Sent == 0 && stream.Phase != "commentary" {
		prefix = agentFinalMessagePrefix
	}
	remaining := codexOutputByteLimit - stream.Sent - len(prefix)
	if len(delta) <= remaining {
		text := prefix + delta
		stream.Sent += len(text)
		phase := stream.Phase
		m.mu.Unlock()
		if connection != nil {
			m.sendCodexStreamChunk(connection, phase, text)
			return nil
		}
		return []Notification{codexHandoffNotification(threadID, itemID, phase, text, false)}
	}
	headBudget := codexOutputHeadLimit - stream.Sent - len(prefix)
	if headBudget < 0 {
		headBudget = 0
	}
	head := takeFirstUTF8Bytes(delta, headBudget)
	stream.Tail = takeLastUTF8Bytes(delta[len(head):], codexOutputTailLimit)
	stream.Truncated = true
	if head == "" {
		m.mu.Unlock()
		return nil
	}
	text := prefix + head
	stream.Sent += len(text)
	phase := stream.Phase
	m.mu.Unlock()
	if connection != nil {
		m.sendCodexStreamChunk(connection, phase, text)
		return nil
	}
	return []Notification{codexHandoffNotification(threadID, itemID, phase, text, false)}
}

func (m *Manager) FinishCodexOutput(threadID string, itemID string) []Notification {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	key := threadID + "\x00" + itemID
	stream := m.streams[key]
	delete(m.streams, key)
	if stream != nil && stream.Timer != nil {
		stream.Timer.Stop()
	}
	if stream == nil || !stream.Truncated {
		m.mu.Unlock()
		return nil
	}
	text := codexOutputTruncationText + stream.Tail
	connection := m.connections[threadID]
	phase := stream.Phase
	m.mu.Unlock()
	if connection != nil {
		m.sendCodexStreamChunk(connection, phase, text)
		return nil
	}
	return []Notification{codexHandoffNotification(threadID, itemID, phase, text, true)}
}

func (m *Manager) CompleteCodexOutput(threadID, itemID, phase, text string) []Notification {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	key := threadID + "\x00" + itemID
	stream := m.streams[key]
	connection := m.connections[threadID]
	if connection == nil {
		m.mu.Unlock()
		return m.FinishCodexOutput(threadID, itemID)
	}
	delete(m.streams, key)
	if stream != nil && stream.Timer != nil {
		stream.Timer.Stop()
	}
	if stream != nil && stream.Phase != "" {
		phase = stream.Phase
	}
	finalStreamChunk := ""
	streamed := false
	if stream != nil && connection.config.Version == VersionV3 {
		if stream.BEM != nil {
			pushCodexStreamText(stream, stream.BEM.finish())
			if stream.BEM.phase != "" {
				stream.Phase = stream.BEM.phase
				phase = stream.Phase
			}
		}
		finalStreamChunk = drainCodexFinalChunk(stream)
		streamed = stream.Sent > 0
	}
	m.mu.Unlock()
	if finalStreamChunk != "" {
		m.sendCodexStreamChunk(connection, phase, finalStreamChunk)
	}
	if !streamed && text != "" {
		if err := connection.sendCodexOutput(text, phase); err != nil {
			m.finishRealtimeTransport(connection, "error", err)
		}
	}
	return nil
}

func (m *Manager) flushCodexOutputStream(key string, expected *codexOutputStream) {
	if m == nil || expected == nil {
		return
	}
	m.mu.Lock()
	stream := m.streams[key]
	if stream != expected {
		m.mu.Unlock()
		return
	}
	stream.FlushScheduled = false
	stream.Timer = nil
	connection := m.connections[stream.ThreadID]
	text := drainCodexStreamChunk(stream)
	phase := stream.Phase
	if text != "" {
		stream.LastFlush = time.Now()
	}
	m.mu.Unlock()
	if text != "" && connection != nil {
		m.sendCodexStreamChunk(connection, phase, text)
	}
}

func pushCodexStreamText(stream *codexOutputStream, text string) {
	if stream == nil || text == "" {
		return
	}
	if stream.BEM != nil {
		var ready bool
		text, ready = stream.BEM.push(text)
		if !ready {
			return
		}
		stream.Phase = stream.BEM.phase
	}
	if stream.Truncated {
		stream.Tail = takeLastUTF8Bytes(stream.Tail+text, codexOutputTailLimit)
		return
	}
	stream.Buffered += text
	remaining := codexOutputByteLimit - stream.Sent
	if remaining < 0 {
		remaining = 0
	}
	if len(stream.Buffered) <= remaining {
		return
	}
	headBudget := codexOutputHeadLimit - stream.Sent
	if headBudget < 0 {
		headBudget = 0
	}
	head := takeFirstUTF8Bytes(stream.Buffered, headBudget)
	stream.Tail = takeLastUTF8Bytes(stream.Buffered, codexOutputTailLimit)
	stream.Buffered = head
	stream.Truncated = true
}

func codexStreamableBytes(stream *codexOutputStream) int {
	if stream == nil {
		return 0
	}
	remaining := codexOutputHeadLimit - stream.Sent
	if remaining < 0 {
		return 0
	}
	return remaining
}

func drainCodexStreamChunk(stream *codexOutputStream) string {
	if stream == nil || stream.Buffered == "" {
		return ""
	}
	available := codexStreamableBytes(stream)
	if available == 0 {
		return ""
	}
	text := takeFirstUTF8Bytes(stream.Buffered, available)
	if text == "" {
		return ""
	}
	stream.Buffered = stream.Buffered[len(text):]
	stream.Sent += len(text)
	return text
}

func drainCodexFinalChunk(stream *codexOutputStream) string {
	if stream == nil {
		return ""
	}
	if !stream.Truncated {
		text := stream.Buffered
		stream.Buffered = ""
		stream.Sent += len(text)
		return text
	}
	text := stream.Buffered + codexOutputTruncationText + stream.Tail
	stream.Buffered = ""
	stream.Tail = ""
	stream.Sent += len(text)
	return text
}

func (m *Manager) CompleteHandoff(threadID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	connection := m.connections[threadID]
	m.mu.Unlock()
	if connection == nil {
		return
	}
	if err := connection.completeHandoff(); err != nil {
		m.finishRealtimeTransport(connection, "error", err)
	}
}

func (m *Manager) sendCodexStreamChunk(connection *realtimeTransportSession, phase, text string) {
	if connection == nil || text == "" {
		return
	}
	if err := connection.sendCodexStreamChunk(text, phase); err != nil {
		m.finishRealtimeTransport(connection, "error", err)
	}
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
	return m.StartWithOptions(params, nil)
}

func (m *Manager) StartWithOptions(params *StartParams, options *StartOptions) (*SessionState, []Notification, error) {
	startContext := context.Background()
	defaultModel := ""
	defaultVersion := VersionV2
	defaultVoice := Voice("")
	if options != nil {
		if options.Context != nil {
			startContext = options.Context
		}
		defaultModel = options.DefaultModel
		if options.DefaultVersion != "" {
			defaultVersion = options.DefaultVersion
		}
		defaultVoice = options.DefaultVoice
	}
	config, err := params.Normalized(defaultModel, defaultVersion, defaultVoice)
	if err != nil {
		return nil, nil, err
	}
	if options != nil && options.Instructions != nil {
		config.Prompt = *options.Instructions
	}
	config.SessionMode = SessionModeConversational
	if options != nil && options.SessionMode != "" {
		config.SessionMode = options.SessionMode
	}
	if config.SessionMode != SessionModeConversational && config.SessionMode != SessionModeTranscription {
		return nil, nil, fmt.Errorf("%w: unsupported realtime session type %q", ErrInvalidRealtimeRequest, config.SessionMode)
	}
	if config.Transport.Type == "webrtc" && config.SessionMode != SessionModeConversational {
		return nil, nil, fmt.Errorf("%w: AVAS realtime calls require conversational realtime", ErrInvalidRealtimeRequest)
	}
	if m == nil {
		return nil, nil, fmt.Errorf("%w: manager is nil", ErrInvalidRealtimeRequest)
	}
	startLock := m.startLock(config.ThreadID)
	startLock.Lock()
	defer startLock.Unlock()
	m.mu.Lock()
	m.ensureLocked()
	if m.shutdown {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: manager is shut down", ErrInvalidRealtimeRequest)
	}
	previousConnection := m.connections[config.ThreadID]
	previousSideband := m.sidebands[config.ThreadID]
	delete(m.connections, config.ThreadID)
	delete(m.sidebands, config.ThreadID)
	delete(m.sessions, config.ThreadID)
	for key, stream := range m.streams {
		if stream != nil && stream.ThreadID == config.ThreadID {
			if stream.Timer != nil {
				stream.Timer.Stop()
			}
			delete(m.streams, key)
		}
	}
	backend := cloneTransportBackendConfig(m.transport)
	if options != nil && options.Backend != nil {
		backend = cloneTransportBackendConfig(options.Backend)
	}
	m.mu.Unlock()
	if previousConnection != nil {
		m.flushRealtimeTranscriptTail(previousConnection)
		_ = previousConnection.close()
	}
	if previousSideband != nil {
		previousSideband.cancel()
	}

	sdpAnswer := ""
	var sideband *realtimeSideband
	var connection *realtimeTransportSession
	if config.Transport.Type == "websocket" && backend != nil && backend.WebsocketBaseURL != "" {
		connection, err = dialRealtimeTransport(startContext, config.ThreadID, backend, config, "", true)
		if err != nil {
			return nil, nil, err
		}
	}
	if config.Transport.Type == "webrtc" && backend != nil && backend.WebRTCCallBaseURL != "" && backend.SidebandBaseURL != "" {
		call, callErr := createRealtimeWebRTCCall(startContext, backend, config)
		if callErr != nil {
			return nil, nil, callErr
		}
		sdpAnswer = call.SDP
		sideband = newRealtimeSideband(config.ThreadID, call.CallID, backend, config)
	}

	m.mu.Lock()
	m.ensureLocked()
	if m.shutdown {
		m.mu.Unlock()
		if connection != nil {
			connection.closeNow()
		}
		if sideband != nil {
			sideband.cancel()
		}
		return nil, nil, fmt.Errorf("%w: manager is shut down", ErrInvalidRealtimeRequest)
	}
	now := m.now().UTC()
	state := &SessionState{Config: *config, StartedAt: now, LastActivity: now}
	m.sessions[config.ThreadID] = state
	if connection != nil {
		m.connections[config.ThreadID] = connection
		m.backgroundTaskWait.Add(1)
	}
	if sideband != nil {
		m.sidebands[config.ThreadID] = sideband
		m.backgroundTaskWait.Add(1)
	}
	stateSnapshot := cloneState(state)
	m.mu.Unlock()
	if connection != nil {
		m.startRealtimeConnection(connection)
	}
	if sideband != nil {
		m.startRealtimeSideband(sideband)
	}
	notifications := []Notification{
		NewStartedNotification(config.ThreadID, config.RealtimeSessionID, config.Version),
	}
	if config.Transport.Type == "webrtc" {
		if sdpAnswer == "" {
			sdpAnswer = "answer:" + config.Transport.SDP
		}
		notifications = append(notifications, NewSDPNotification(config.ThreadID, sdpAnswer))
	}
	return stateSnapshot, notifications, nil
}

func (m *Manager) AppendAudio(params *AppendAudioParams) (*SessionState, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	state, connection, err := m.updateWithConnection(params.ThreadID, func(state *SessionState) {
		state.AudioFrames++
	})
	if err != nil {
		return nil, err
	}
	if connection != nil {
		if err := connection.sendAudio(params.Audio); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func (m *Manager) AppendText(params *AppendTextParams) (*SessionState, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	state, connection, err := m.updateWithConnection(params.ThreadID, func(state *SessionState) {
		state.TextInputs++
	})
	if err != nil {
		return nil, err
	}
	if connection != nil {
		if err := connection.sendText(params.Text, params.Role); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func (m *Manager) AppendSpeech(params *AppendSpeechParams) (*SessionState, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	state, connection, err := m.updateWithConnection(params.ThreadID, func(state *SessionState) {
		if strings.TrimSpace(params.Text) != "" {
			state.SpeechInputs++
		}
	})
	if err != nil {
		return nil, err
	}
	if connection != nil && strings.TrimSpace(params.Text) != "" {
		if err := connection.sendSpeech(params.Text); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func (m *Manager) Stop(params *StopParams, reason string) (*SessionState, Notification, error) {
	if err := params.Validate(); err != nil {
		return nil, Notification{}, err
	}
	if m == nil {
		return nil, Notification{}, fmt.Errorf("%w: manager is nil", ErrInvalidRealtimeRequest)
	}
	m.mu.Lock()
	m.ensureLocked()
	state, ok := m.sessions[params.ThreadID]
	if !ok || state.ClosedAt != nil {
		m.mu.Unlock()
		return nil, Notification{}, fmt.Errorf("%w: %s", ErrRealtimeNotRunning, params.ThreadID)
	}
	now := m.now().UTC()
	state.LastActivity = now
	state.ClosedAt = &now
	state.CloseReason = reason
	if sideband := m.sidebands[params.ThreadID]; sideband != nil {
		sideband.cancel()
		delete(m.sidebands, params.ThreadID)
	}
	connection := m.connections[params.ThreadID]
	delete(m.connections, params.ThreadID)
	for key, stream := range m.streams {
		if stream != nil && stream.ThreadID == params.ThreadID {
			if stream.Timer != nil {
				stream.Timer.Stop()
			}
			delete(m.streams, key)
		}
	}
	snapshot := cloneState(state)
	m.mu.Unlock()
	if connection != nil {
		_ = connection.close()
		m.flushRealtimeTranscriptTail(connection)
	}
	return snapshot, NewClosedNotification(params.ThreadID, reason), nil
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
	state, _, err := m.updateWithConnection(threadID, apply)
	return state, err
}

func (m *Manager) updateWithConnection(threadID string, apply func(*SessionState)) (*SessionState, *realtimeTransportSession, error) {
	if m == nil {
		return nil, nil, fmt.Errorf("%w: manager is nil", ErrInvalidRealtimeRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	state, ok := m.sessions[threadID]
	if !ok || state.ClosedAt != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrRealtimeNotRunning, threadID)
	}
	apply(state)
	state.LastActivity = m.now().UTC()
	return cloneState(state), m.connections[threadID], nil
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
	if m.sidebands == nil {
		m.sidebands = map[string]*realtimeSideband{}
	}
	if m.connections == nil {
		m.connections = map[string]*realtimeTransportSession{}
	}
	if m.startLocks == nil {
		m.startLocks = map[string]*sync.Mutex{}
	}
}

func (m *Manager) startLock(threadID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	lock := m.startLocks[threadID]
	if lock == nil {
		lock = &sync.Mutex{}
		m.startLocks[threadID] = lock
	}
	return lock
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
	Type              string
	RealtimeSessionID string
	Instructions      *string
	Role              string
	Delta             string
	Text              string
	Message           string
	Audio             *AudioChunk
	Item              map[string]any
	ResponseID        string
	HandoffID         string
	ItemID            string
	InputTranscript   string
	ActiveTranscript  []TranscriptEntry
}

type TranscriptEntry struct {
	Role string `json:"role"`
	Text string `json:"text"`
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

func boolValue(value *bool) bool {
	return value != nil && *value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneStringSliceMap(value map[string][]string) map[string][]string {
	if value == nil {
		return nil
	}
	cloned := make(map[string][]string, len(value))
	for key, entries := range value {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
