package app

// Local voice helper ownership for the interactive TUI. The TUI owns the media
// path: it starts the packaged helper, posts the helper's offer through the
// app-server, and applies the remote answer. The app-server never sees audio.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/config"
	"codex_go/realtime"
	codextea "codex_go/tui/tea"
	"codex_go/voicehost"
)

const (
	// voiceStartTimeout covers helper startup and its transport offer.
	voiceStartTimeout = 90 * time.Second
	// voiceStopTimeout bounds the best-effort stop RPC.
	voiceStopTimeout = 15 * time.Second
)

// voiceRuntimeOptions wires the helper to the TUI's transport.
type voiceRuntimeOptions struct {
	// packageDir overrides the resolved package root, primarily for tests.
	packageDir string
	// buildCommit overrides the stamped helper identity.
	buildCommit string
	// realtimeSettings reads the effective realtime model and voice preference.
	realtimeSettings func(ctx context.Context) VoiceSettings
	// listVoices asks the app-server which voices it supports.
	listVoices func(ctx context.Context) realtime.VoicesList
	// startSession asks the app-server to start the thread's realtime session
	// with the helper's offer.
	startSession func(ctx context.Context, params realtime.StartParams) error
	// stopSession ends the thread's realtime session.
	stopSession func(ctx context.Context, threadID string) error
}

// VoiceSettings is the effective realtime configuration the TUI applies when it
// starts a session.
type VoiceSettings struct {
	// Model overrides the configured realtime model when non-empty.
	Model string
	// Voice is the configured voice preference. VoiceSet distinguishes an
	// absent or null preference, which falls back to the default voice, from an
	// explicit name.
	Voice    string
	VoiceSet bool
}

// voiceRuntime owns at most one helper process for the TUI.
type voiceRuntime struct {
	options voiceRuntimeOptions

	mu       sync.Mutex
	session  *voicehost.StartedSession
	threadID string
}

func newVoiceRuntime(options voiceRuntimeOptions) *voiceRuntime {
	return &voiceRuntime{options: options}
}

// startVoiceSession launches the packaged helper. Tests replace it so the
// runtime can be exercised without a helper process.
var startVoiceSession = func(ctx context.Context, options voicehost.SessionOptions) (*voicehost.StartedSession, error) {
	return (voicehost.RealtimeSession{}).StartWithOptions(ctx, options)
}

// startCmd launches the helper and posts its offer for one startup attempt.
func (r *voiceRuntime) startCmd(threadID string, attemptID uint64) bubbletea.Cmd {
	return func() bubbletea.Msg {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: errors.New("start a conversation before using voice mode")}
		}
		if r == nil {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: errors.New("voice runtime is unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), voiceStartTimeout)
		defer cancel()
		started, err := startVoiceSession(ctx, voicehost.SessionOptions{
			PackageDir:  r.options.packageDir,
			BuildCommit: r.options.buildCommit,
		})
		if err != nil {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: err}
		}
		if err := r.options.startSession(ctx, r.startParams(ctx, threadID, started.OfferSDP)); err != nil {
			// The app-server never accepted the offer, so the helper is
			// retired before the caller sees the failure.
			started.Handle.Close()
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: err}
		}
		r.setSession(started, threadID)
		return codextea.VoiceHelperAttachedMsg{AttemptID: attemptID}
	}
}

// startParams builds the app-server start request the TUI sends for a voice
// session: a V3 WebRTC conversation with client-managed handoffs and no startup
// context, carrying the configured model and voice.
func (r *voiceRuntime) startParams(ctx context.Context, threadID string, offerSDP string) realtime.StartParams {
	clientManagedHandoffs := true
	includeStartupContext := false
	version := realtime.VersionV3
	params := realtime.StartParams{
		ThreadID:              threadID,
		ClientManagedHandoffs: &clientManagedHandoffs,
		OutputModality:        realtime.OutputAudio,
		IncludeStartupContext: &includeStartupContext,
		Transport:             realtime.WebRTCTransport(offerSDP),
		Version:               &version,
	}
	settings := VoiceSettings{}
	if r.options.realtimeSettings != nil {
		settings = r.options.realtimeSettings(ctx)
	}
	if model := strings.TrimSpace(settings.Model); model != "" {
		params.Model = &model
	}
	if voice := r.resolveVoice(ctx, settings); voice != nil {
		params.Voice = voice
	}
	return params
}

// resolveVoice mirrors the Rust resolver: an explicit preference is used when
// it names a known voice and is dropped when it does not parse; an absent or
// null preference falls back to the version's default voice.
func (r *voiceRuntime) resolveVoice(ctx context.Context, settings VoiceSettings) *realtime.Voice {
	if settings.VoiceSet {
		configured := realtime.Voice(strings.TrimSpace(settings.Voice))
		if !realtime.IsKnownVoice(configured) {
			return nil
		}
		return &configured
	}
	voices := realtime.BuiltinVoices()
	if r.options.listVoices != nil {
		voices = r.options.listVoices(ctx)
	}
	fallback := voices.DefaultForVersion(realtime.VersionV1)
	if fallback == "" {
		return nil
	}
	return &fallback
}

// applyAnswerCmd applies the remote answer and reports the outcome.
func (r *voiceRuntime) applyAnswerCmd(threadID string, attemptID uint64, answer string) bubbletea.Cmd {
	return func() bubbletea.Msg {
		session := r.currentSession()
		if session == nil {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: errors.New("voice helper is not running")}
		}
		if err := session.Handle.ApplyAnswerSDP(answer); err != nil {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: err}
		}
		return codextea.VoiceAnswerResultMsg{AttemptID: attemptID}
	}
}

// close retires the helper and asks the app-server to end the session.
func (r *voiceRuntime) close() {
	if r == nil {
		return
	}
	session, threadID := r.takeSession()
	if session != nil {
		session.Handle.Close()
	}
	if threadID == "" || r.options.stopSession == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), voiceStopTimeout)
	defer cancel()
	// The local session is already gone; a failed stop RPC must not surface as
	// a user-visible error.
	_ = r.options.stopSession(ctx, threadID)
}

// setMicrophoneMuted queues an ordered privacy transition.
func (r *voiceRuntime) setMicrophoneMuted(muted bool) error {
	session := r.currentSession()
	if session == nil {
		return errors.New("voice conversation is not running")
	}
	return session.Handle.SetMicrophoneMuted(muted)
}

// peaks returns and clears the accumulated audio levels.
func (r *voiceRuntime) peaks() (uint16, uint16) {
	session := r.currentSession()
	if session == nil {
		return 0, 0
	}
	return session.Handle.TakeMicrophonePeak(), session.Handle.TakeSpeakerPeak()
}

// running reports whether a helper is currently owned.
func (r *voiceRuntime) running() bool {
	return r.currentSession() != nil
}

func (r *voiceRuntime) currentSession() *voicehost.StartedSession {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

func (r *voiceRuntime) setSession(session *voicehost.StartedSession, threadID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.session = session
	r.threadID = threadID
}

func (r *voiceRuntime) takeSession() (*voicehost.StartedSession, string) {
	if r == nil {
		return nil, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.session
	threadID := r.threadID
	r.session = nil
	r.threadID = ""
	return session, threadID
}

// interactiveRemoteRealtimeSettings reads the effective realtime model and
// voice preference from the app-server. A read failure keeps the built-in
// defaults, which is what the Rust TUI does when the server cannot report them.
func interactiveRemoteRealtimeSettings(endpoint *appserverdaemon.RemoteAppServerEndpoint) func(context.Context) VoiceSettings {
	return func(ctx context.Context) VoiceSettings {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return VoiceSettings{}
		}
		defer client.close()
		var response config.ConfigReadResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, config.ConfigReadParams{}, &response); err != nil {
			return VoiceSettings{}
		}
		return RealtimeSettingsFromConfigValues(response.Config)
	}
}

// RealtimeSettingsFromConfigValues resolves the realtime model and voice
// preference from effective configuration values.
func RealtimeSettingsFromConfigValues(values map[string]any) VoiceSettings {
	settings := VoiceSettings{}
	if values == nil {
		return settings
	}
	if model, ok := values["experimental_realtime_ws_model"].(string); ok {
		settings.Model = model
	}
	table, _ := values["realtime"].(map[string]any)
	raw, present := table["voice"]
	if !present || raw == nil {
		return settings
	}
	if name, ok := raw.(string); ok {
		settings.Voice = name
		settings.VoiceSet = true
	}
	return settings
}

// interactiveRemoteRealtimeVoices asks the app-server which voices it supports.
// A failure falls back to the builtin list, matching the Rust TUI.
func interactiveRemoteRealtimeVoices(endpoint *appserverdaemon.RemoteAppServerEndpoint) func(context.Context) realtime.VoicesList {
	return func(ctx context.Context) realtime.VoicesList {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return realtime.BuiltinVoices()
		}
		defer client.close()
		var response realtime.ListVoicesResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadRealtimeListVoices, realtime.ListVoicesParams{}, &response); err != nil {
			return realtime.BuiltinVoices()
		}
		if len(response.Voices.V1) == 0 && len(response.Voices.V2) == 0 {
			return realtime.BuiltinVoices()
		}
		return response.Voices
	}
}

// voiceSettingsTimeout bounds one settings read or write.
const voiceSettingsTimeout = 15 * time.Second

// interactiveRemoteVoiceSettings fetches the voice catalog and the effective
// preference for the settings picker. The TUI uses V3, which shares the V1
// voice catalog.
func interactiveRemoteVoiceSettings(endpoint *appserverdaemon.RemoteAppServerEndpoint) func() bubbletea.Cmd {
	return func() bubbletea.Cmd {
		return func() bubbletea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), voiceSettingsTimeout)
			defer cancel()
			voices := interactiveRemoteRealtimeVoices(endpoint)(ctx)
			settings := interactiveRemoteRealtimeSettings(endpoint)(ctx)
			current := settings.Voice
			if !settings.VoiceSet {
				current = string(voices.DefaultV1)
			}
			return codextea.VoiceSettingsMsg{
				Voices:  RealtimeVoiceNames(voices.V1),
				Current: current,
			}
		}
	}
}

// RealtimeVoiceNames renders a voice list for the picker.
func RealtimeVoiceNames(voices []realtime.Voice) []string {
	names := make([]string, 0, len(voices))
	for _, voice := range voices {
		names = append(names, string(voice))
	}
	return names
}

// interactiveRemoteVoiceSaver persists the chosen voice and confirms it is the
// effective preference, mirroring the Rust save-and-confirm flow.
func interactiveRemoteVoiceSaver(endpoint *appserverdaemon.RemoteAppServerEndpoint) func(voice string) bubbletea.Cmd {
	return func(voice string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), voiceSettingsTimeout)
			defer cancel()
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				return codextea.VoiceSavedMsg{Voice: voice, Err: err}
			}
			defer client.close()
			var response config.ConfigWriteResponse
			err = remoteSessionRequest(ctx, client, appserver.MethodConfigBatchWrite, config.ConfigBatchWriteParams{
				Edits: []config.ConfigEdit{{
					KeyPath:       "realtime.voice",
					Value:         voice,
					MergeStrategy: config.MergeReplace,
				}},
			}, &response)
			if err != nil {
				return codextea.VoiceSavedMsg{Voice: voice, Err: err}
			}
			// A managed layer can override the saved value, in which case the
			// preference is stored but not applied.
			settings := interactiveRemoteRealtimeSettings(endpoint)(ctx)
			if settings.VoiceSet && strings.TrimSpace(settings.Voice) != voice {
				return codextea.VoiceSavedMsg{
					Voice: voice,
					Err:   errors.New("the saved voice is overridden by a managed setting"),
				}
			}
			return codextea.VoiceSavedMsg{Voice: voice}
		}
	}
}

// interactiveRemoteSpeechSender speaks one delegated answer into the thread's
// realtime conversation. The thread is resolved at delivery time so a resumed
// or switched thread is never addressed by mistake.
func interactiveRemoteSpeechSender(endpoint *appserverdaemon.RemoteAppServerEndpoint, threadID func() string) func(itemID string, text string) bubbletea.Cmd {
	return func(itemID string, text string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			target := ""
			if threadID != nil {
				target = strings.TrimSpace(threadID())
			}
			if target == "" {
				return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: errors.New("no active thread to speak into")}
			}
			ctx, cancel := context.WithTimeout(context.Background(), voiceSettingsTimeout)
			defer cancel()
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: err}
			}
			defer client.close()
			var response realtime.AppendSpeechResponse
			err = remoteSessionRequest(ctx, client, appserver.MethodThreadRealtimeAppendSpeech, realtime.AppendSpeechParams{
				ThreadID: target,
				Text:     text,
			}, &response)
			return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: err}
		}
	}
}
