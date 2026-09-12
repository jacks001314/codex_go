package app

// Embedded (local) TUI voice wiring. The remote TUI drives voice through a
// remote app-server endpoint; the embedded TUI owns an in-process runtime
// router, so the same callbacks are implemented against appserver.Request. The
// TUI still owns the helper and the media path; the app-server only relays the
// offer and drives the realtime session.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

func interactiveLocalVoiceRouterFactory() interactiveGoalRouter {
	return appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
}

func localVoiceRequest(router interactiveGoalRouter, id appserver.RequestID, method appserver.Method, params any) (any, error) {
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
// in-process runtime router, mirroring the remote helpers.
func interactiveLocalVoiceCallbacks(factory interactiveGoalRouterFactory) (
	func(context.Context) VoiceSettings,
	func(context.Context) realtime.VoicesList,
	func(context.Context, realtime.StartParams) error,
	func(context.Context, string) error,
) {
	if factory == nil {
		factory = interactiveLocalVoiceRouterFactory
	}
	settings := func(ctx context.Context) VoiceSettings {
		router := factory()
		if router == nil {
			return VoiceSettings{}
		}
		defer router.Close()
		if err := initializeLocalTUIConnection(router.Handle, interactiveVoiceConnectionID); err != nil {
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
		defer router.Close()
		if err := initializeLocalTUIConnection(router.Handle, interactiveVoiceConnectionID); err != nil {
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
		defer router.Close()
		if err := initializeLocalTUIConnection(router.Handle, interactiveVoiceConnectionID); err != nil {
			return err
		}
		_, err := localVoiceRequest(router, appserver.IntID(2), appserver.MethodThreadRealtimeStart, params)
		return err
	}
	stop := func(ctx context.Context, threadID string) error {
		router := factory()
		if router == nil {
			return errors.New("thread/realtime/stop failed in TUI: app-server is unavailable")
		}
		defer router.Close()
		if err := initializeLocalTUIConnection(router.Handle, interactiveVoiceConnectionID); err != nil {
			return err
		}
		_, err := localVoiceRequest(router, appserver.IntID(2), appserver.MethodThreadRealtimeStop, realtime.StopParams{
			ThreadID: strings.TrimSpace(threadID),
		})
		return err
	}
	return settings, voices, start, stop
}

// interactiveLocalVoiceSettings opens the voice picker with the local catalog.
func interactiveLocalVoiceSettings(factory interactiveGoalRouterFactory) func() bubbletea.Cmd {
	_, voices, _, _ := interactiveLocalVoiceCallbacks(factory)
	settings := func(ctx context.Context) VoiceSettings {
		settingsFn, _, _, _ := interactiveLocalVoiceCallbacks(factory)
		return settingsFn(ctx)
	}
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
func interactiveLocalVoiceSaver(factory interactiveGoalRouterFactory) func(voice string) bubbletea.Cmd {
	return func(voice string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			router := factory
			if router == nil {
				router = interactiveLocalVoiceRouterFactory
			}
			handle := router()
			if handle == nil {
				return codextea.VoiceSavedMsg{Voice: voice, Err: errors.New("config write failed in TUI: app-server is unavailable")}
			}
			defer handle.Close()
			if err := initializeLocalTUIConnection(handle.Handle, interactiveVoiceConnectionID); err != nil {
				return codextea.VoiceSavedMsg{Voice: voice, Err: err}
			}
			if _, err := localVoiceRequest(handle, appserver.IntID(2), appserver.MethodConfigBatchWrite, config.ConfigBatchWriteParams{
				Edits: []config.ConfigEdit{{
					KeyPath:       "realtime.voice",
					Value:         voice,
					MergeStrategy: config.MergeReplace,
				}},
			}); err != nil {
				return codextea.VoiceSavedMsg{Voice: voice, Err: err}
			}
			settingsFn, _, _, _ := interactiveLocalVoiceCallbacks(factory)
			ctx := context.Background()
			if effective := settingsFn(ctx); effective.VoiceSet && strings.TrimSpace(effective.Voice) != voice {
				return codextea.VoiceSavedMsg{Voice: voice, Err: errors.New("the saved voice is overridden by a managed setting")}
			}
			return codextea.VoiceSavedMsg{Voice: voice}
		}
	}
}

// interactiveLocalSpeechSender speaks one delegated answer into the thread.
func interactiveLocalSpeechSender(factory interactiveGoalRouterFactory, threadID func() string) func(itemID string, text string) bubbletea.Cmd {
	return func(itemID string, text string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			target := ""
			if threadID != nil {
				target = strings.TrimSpace(threadID())
			}
			if target == "" {
				return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: errors.New("no active thread to speak into")}
			}
			router := factory
			if router == nil {
				router = interactiveLocalVoiceRouterFactory
			}
			handle := router()
			if handle == nil {
				return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: errors.New("thread/realtime/appendSpeech failed in TUI: app-server is unavailable")}
			}
			defer handle.Close()
			if err := initializeLocalTUIConnection(handle.Handle, interactiveVoiceConnectionID); err != nil {
				return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: err}
			}
			_, err := localVoiceRequest(handle, appserver.IntID(2), appserver.MethodThreadRealtimeAppendSpeech, realtime.AppendSpeechParams{
				ThreadID: target,
				Text:     text,
			})
			return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: err}
		}
	}
}
