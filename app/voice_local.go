package app

// Embedded (local) TUI voice wiring. The remote TUI drives voice through a
// remote app-server endpoint; the embedded TUI owns an in-process runtime
// router. That router must outlive the start request, because the realtime
// session delivers its SDP answer and transcripts as notifications: a router
// created per request would be closed before the answer arrives and the TUI
// would wait forever in the connecting phase.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/config"
	"codex_go/realtime"
	codextea "codex_go/tui/tea"
)

// interactiveVoiceConnectionID keeps the voice relay on its own app-server
// connection so a media session never competes with the interactive loop.
const interactiveVoiceConnectionID = "local-tui-voice"

// interactiveVoiceRouter is the request surface the voice callbacks need. The
// router is owned by the session and is never closed per request.
type interactiveVoiceRouter interface {
	Handle(request *appserver.Request) *appserver.Response
}

// interactiveVoiceRouterFactory yields the persistent router.
type interactiveVoiceRouterFactory func() interactiveVoiceRouter

// localVoiceSession owns one long-lived in-process app-server runtime for the
// embedded TUI's voice sessions and forwards realtime notifications to the TUI.
type localVoiceSession struct {
	mu            sync.Mutex
	router        *appserver.RuntimeRouter
	notifications chan codextea.VoiceNotificationMsg
	closed        bool
	// initErr records a failed one-time connection initialization.
	initErr error
}

func newLocalVoiceSession() *localVoiceSession {
	return &localVoiceSession{notifications: make(chan codextea.VoiceNotificationMsg, 64)}
}

func (s *localVoiceSession) ensureRouter() interactiveVoiceRouter {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.router != nil {
		return s.router
	}
	router := appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
	router.SetNotificationSink(appserver.NotificationSinkFunc(func(notification *appserver.Notification) {
		message, ok := realtimeNotificationMessage(notification)
		if !ok {
			return
		}
		select {
		case s.notifications <- message:
		default:
			// The TUI is behind; drop rather than stall the runtime.
		}
	}))
	// A connection is initialized exactly once per router and later requests
	// reuse it; re-initializing fails with "Already initialized".
	if err := initializeLocalTUIConnection(router.Handle, interactiveVoiceConnectionID); err != nil {
		s.initErr = err
	}
	s.router = router
	return router
}

// realtimeNotificationMessage maps one runtime notification to the TUI's voice
// message, decoding the payload from its wire shape the way the remote TUI's
// notification reader does.
func realtimeNotificationMessage(notification *appserver.Notification) (codextea.VoiceNotificationMsg, bool) {
	if notification == nil {
		return codextea.VoiceNotificationMsg{}, false
	}
	raw, err := json.Marshal(notification.Params)
	if err != nil {
		return codextea.VoiceNotificationMsg{}, false
	}
	return DecodeThreadRealtimeNotification(notification.Method, raw)
}

// Notifications streams realtime voice messages for the TUI to consume.
func (s *localVoiceSession) Notifications() <-chan codextea.VoiceNotificationMsg {
	if s == nil {
		return nil
	}
	return s.notifications
}

// Close releases the runtime and stops the notification stream.
func (s *localVoiceSession) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	router := s.router
	s.router = nil
	closed := s.closed
	s.closed = true
	s.mu.Unlock()
	if router != nil {
		_ = router.Close()
	}
	if !closed {
		close(s.notifications)
	}
}

func localVoiceRequest(router interactiveVoiceRouter, id appserver.RequestID, method appserver.Method, params any) (any, error) {
	if router == nil {
		return nil, fmt.Errorf("%s failed in TUI: app-server is unavailable", method)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("%s failed in TUI: %w", method, err)
	}
	response := router.Handle(&appserver.Request{
		JSONRPC:      "2.0",
		ID:           id,
		Method:       method,
		Params:       raw,
		ConnectionID: interactiveVoiceConnectionID,
	})
	if response == nil {
		return nil, fmt.Errorf("%s failed in TUI: no response", method)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%s failed in TUI: %s", method, strings.TrimSpace(response.Error.Message))
	}
	return response.Result, nil
}

// interactiveLocalVoiceCallbacks builds the voiceRuntime callbacks against the
// persistent in-process router. The router is initialized once by the session;
// the callbacks only issue their own requests.
func interactiveLocalVoiceCallbacks(factory interactiveVoiceRouterFactory) (
	func(context.Context) VoiceSettings,
	func(context.Context) realtime.VoicesList,
	func(context.Context, realtime.StartParams) error,
	func(context.Context, string) error,
) {
	if factory == nil {
		factory = func() interactiveVoiceRouter { return nil }
	}
	settings := func(ctx context.Context) VoiceSettings {
		router := factory()
		if router == nil {
			return VoiceSettings{}
		}
		result, err := localVoiceRequest(router, appserver.IntID(2), appserver.MethodConfigRead, config.ConfigReadParams{})
		if err != nil {
			return VoiceSettings{}
		}
		response, ok := result.(*config.ConfigReadResponse)
		if !ok || response == nil {
			return VoiceSettings{}
		}
		return RealtimeSettingsFromConfigValues(response.Config)
	}
	voices := func(ctx context.Context) realtime.VoicesList {
		router := factory()
		if router == nil {
			return realtime.BuiltinVoices()
		}
		result, err := localVoiceRequest(router, appserver.IntID(2), appserver.MethodThreadRealtimeListVoices, realtime.ListVoicesParams{})
		if err != nil {
			return realtime.BuiltinVoices()
		}
		response, ok := result.(*realtime.ListVoicesResponse)
		if !ok || response == nil || (len(response.Voices.V1) == 0 && len(response.Voices.V2) == 0) {
			return realtime.BuiltinVoices()
		}
		return response.Voices
	}
	start := func(ctx context.Context, params realtime.StartParams) error {
		router := factory()
		if router == nil {
			return errors.New("thread/realtime/start failed in TUI: app-server is unavailable")
		}
		_, err := localVoiceRequest(router, appserver.IntID(2), appserver.MethodThreadRealtimeStart, params)
		return err
	}
	stop := func(ctx context.Context, threadID string) error {
		router := factory()
		if router == nil {
			return errors.New("thread/realtime/stop failed in TUI: app-server is unavailable")
		}
		_, err := localVoiceRequest(router, appserver.IntID(2), appserver.MethodThreadRealtimeStop, realtime.StopParams{
			ThreadID: strings.TrimSpace(threadID),
		})
		return err
	}
	return settings, voices, start, stop
}

// interactiveLocalVoiceSettings opens the voice picker with the local catalog.
func interactiveLocalVoiceSettings(factory interactiveVoiceRouterFactory) func() bubbletea.Cmd {
	settings, voices, _, _ := interactiveLocalVoiceCallbacks(factory)
	return func() bubbletea.Cmd {
		return func() bubbletea.Msg {
			ctx := context.Background()
			catalog := voices(ctx)
			current := settings(ctx)
			name := current.Voice
			if !current.VoiceSet {
				name = string(catalog.DefaultV1)
			}
			return codextea.VoiceSettingsMsg{
				Voices:  RealtimeVoiceNames(catalog.V1),
				Current: name,
			}
		}
	}
}

// interactiveLocalVoiceSaver persists the chosen voice through the local config
// write RPC and confirms it is the effective preference.
func interactiveLocalVoiceSaver(factory interactiveVoiceRouterFactory) func(voice string) bubbletea.Cmd {
	settings, _, _, _ := interactiveLocalVoiceCallbacks(factory)
	return func(voice string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			router := factory()
			if router == nil {
				return codextea.VoiceSavedMsg{Voice: voice, Err: errors.New("config write failed in TUI: app-server is unavailable")}
			}
			if _, err := localVoiceRequest(router, appserver.IntID(2), appserver.MethodConfigBatchWrite, config.ConfigBatchWriteParams{
				Edits: []config.ConfigEdit{{
					KeyPath:       "realtime.voice",
					Value:         voice,
					MergeStrategy: config.MergeReplace,
				}},
			}); err != nil {
				return codextea.VoiceSavedMsg{Voice: voice, Err: err}
			}
			ctx := context.Background()
			if effective := settings(ctx); effective.VoiceSet && strings.TrimSpace(effective.Voice) != voice {
				return codextea.VoiceSavedMsg{Voice: voice, Err: errors.New("the saved voice is overridden by a managed setting")}
			}
			return codextea.VoiceSavedMsg{Voice: voice}
		}
	}
}

// interactiveLocalSpeechSender speaks one delegated answer into the thread.
func interactiveLocalSpeechSender(factory interactiveVoiceRouterFactory, threadID func() string) func(itemID string, text string) bubbletea.Cmd {
	return func(itemID string, text string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			target := ""
			if threadID != nil {
				target = strings.TrimSpace(threadID())
			}
			if target == "" {
				return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: errors.New("no active thread to speak into")}
			}
			router := factory()
			if router == nil {
				return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: errors.New("thread/realtime/appendSpeech failed in TUI: app-server is unavailable")}
			}
			_, err := localVoiceRequest(router, appserver.IntID(2), appserver.MethodThreadRealtimeAppendSpeech, realtime.AppendSpeechParams{
				ThreadID: target,
				Text:     text,
			})
			return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: err}
		}
	}
}
