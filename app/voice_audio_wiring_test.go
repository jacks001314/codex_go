package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/config"
	"codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
	"codex_go/utils"
	"codex_go/voicehost"
)

// Rust #49437 "Add local audio device selection to TUI voice settings"
// (upstream fc81a7154b) and #49836 "Allow microphone channel selection for
// voice conversations" (upstream e53e932dc8). Rust counterpart:
// codex-rs/tui/src/app/realtime_settings.rs — App::persist_realtime_device,
// App::persist_realtime_input_channel, App::persist_realtime_audio and
// App::list_realtime_devices, wired into the hosts by
// codex-rs/tui/src/app/event_dispatch.rs (OpenRealtimeDevicePicker /
// RealtimeDeviceChosen / RealtimeInputChannelChosen).

// TestInteractiveHostsInjectVoiceAudioHooksLikeRust covers the host wiring
// itself: both the embedded and the remote TUI must inject the three audio
// hooks, otherwise the "Sound devices" rows are dead in the shipping binary
// (grep on app/ was 0 before this change). Neither host has an extractable
// Options builder — the literal is built inline in runInteractiveTUI and
// runInteractiveRemoteTUI — so the wiring is guarded from the host sources,
// while the injected expressions' behaviour is covered by the tests below.
func TestInteractiveHostsInjectVoiceAudioHooksLikeRust(t *testing.T) {
	want := map[string]string{
		"OnVoiceListDevices:":      "voiceListDevicesCmd(voice)",
		"OnVoiceSaveDevice:":       "voiceSaveDeviceCmd(",
		"OnVoiceSaveInputChannel:": "voiceSaveInputChannelCmd(",
	}
	for _, name := range []string{"interactive.go", "remote_tui.go"} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		found := map[string]bool{}
		for _, line := range strings.Split(string(source), "\n") {
			// Only an uncommented field assignment counts: a commented or
			// deleted line leaves the rows unreachable.
			trimmed := strings.TrimSpace(line)
			for field, call := range want {
				if strings.HasPrefix(trimmed, field) && strings.Contains(trimmed, call) {
					found[field] = true
				}
			}
		}
		for field, call := range want {
			if !found[field] {
				t.Fatalf("%s does not inject %s %s: the audio device rows are unreachable from the TUI", name, field, call)
			}
		}
	}
}

// The notices the TUI renders for a saved vs an overridden audio setting
// (tui/tea/voice_settings.go voiceAudioSavedNotice / voiceAudioOverriddenNotice;
// Rust App::persist_realtime_audio copy). They are user-visible contract, so the
// app test spells them out instead of reaching across packages.
const (
	voiceAudioSavedNoticeLikeRust      = "Audio setting saved. Applies to your next voice conversation."
	voiceAudioOverriddenNoticeLikeRust = "Audio setting was saved but is overridden by another configuration layer."
)

// voiceAudioConfigRecorderLikeRust is a stub audio configuration surface.
type voiceAudioConfigRecorderLikeRust struct {
	edits     [][]config.ConfigEdit
	effective voicehost.AudioDeviceSelection
	writeErr  error
	readErr   error
}

func (r *voiceAudioConfigRecorderLikeRust) config() voiceAudioConfig {
	return voiceAudioConfig{
		write: func(_ context.Context, edits []config.ConfigEdit) error {
			r.edits = append(r.edits, edits)
			return r.writeErr
		},
		read: func(context.Context) (voicehost.AudioDeviceSelection, error) {
			return r.effective, r.readErr
		},
	}
}

// TestVoiceAudioHookOptionsReachHelperAndConfigLikeRust builds the exact
// options the two hosts inject and drives them: the listing hook really goes
// through the local voice helper (never the app server), and the saving hooks
// write audio.microphone / audio.speaker and clear audio.microphone_channel
// while reporting whether the saved value is the effective one. Rust
// counterpart: realtime_requests.rs
// audio_devices_are_persisted_and_input_changes_clear_channels.
func TestVoiceAudioHookOptionsReachHelperAndConfigLikeRust(t *testing.T) {
	runtime := newVoiceRuntime(voiceRuntimeOptions{packageDir: "/nonexistent-voicesync-package"})
	recorder := &voiceAudioConfigRecorderLikeRust{}
	options := codextea.Options{
		OnVoiceListDevices:      voiceListDevicesCmd(runtime),
		OnVoiceSaveDevice:       voiceSaveDeviceCmd(recorder.config()),
		OnVoiceSaveInputChannel: voiceSaveInputChannelCmd(recorder.config()),
	}
	modelOptions := options
	modelOptions.Width = 100
	modelOptions.Height = 30
	if options.OnVoiceListDevices == nil || options.OnVoiceSaveDevice == nil || options.OnVoiceSaveInputChannel == nil {
		t.Fatal("the host options leave an audio hook nil")
	}

	// The listing really reaches the helper connection: an unusable package
	// reports an error instead of answering from a stub.
	listing, ok := options.OnVoiceListDevices(voicehost.AudioDeviceKindInput)().(codextea.VoiceDevicesMsg)
	if !ok || listing.Kind != voicehost.AudioDeviceKindInput || listing.Err == nil {
		t.Fatalf("device listing = %#v, want an input error from the helper path", listing)
	}
	if unknown, ok := options.OnVoiceListDevices(voicehost.AudioDeviceKind("sideways"))().(codextea.VoiceDevicesMsg); !ok || unknown.Err == nil {
		t.Fatalf("unknown direction listing = %#v", unknown)
	}

	// Saving an input device clears the saved channel (Rust #49836).
	mic := "Mono mic"
	recorder.effective = voicehost.AudioDeviceSelection{Microphone: &mic}
	micSaved, ok := options.OnVoiceSaveDevice(voicehost.AudioDeviceKindInput, &mic)().(codextea.VoiceDeviceSavedMsg)
	if !ok || micSaved.Err != nil || micSaved.Overridden || micSaved.ChannelOverridden {
		t.Fatalf("input device save = %#v", micSaved)
	}
	assertVoiceAudioEditsLikeRust(t, recorder.edits[0], []config.ConfigEdit{
		{KeyPath: "audio.microphone", Value: "Mono mic", MergeStrategy: config.MergeReplace},
		{KeyPath: "audio.microphone_channel", Value: nil, MergeStrategy: config.MergeReplace},
	})

	// Saving an output device leaves the channel alone.
	output := "Headphones"
	recorder.effective = voicehost.AudioDeviceSelection{Speaker: &output}
	if outputSaved, ok := options.OnVoiceSaveDevice(voicehost.AudioDeviceKindOutput, &output)().(codextea.VoiceDeviceSavedMsg); !ok || outputSaved.Err != nil {
		t.Fatalf("output device save = %#v", outputSaved)
	}
	if got := recorder.edits[len(recorder.edits)-1]; len(got) != 1 {
		t.Fatalf("output save edits = %#v, want only audio.speaker", got)
	}

	// A channel from another layer still overrides the cleared input channel.
	recorder.effective = voicehost.AudioDeviceSelection{Microphone: &mic, Channel: []uint16{3}}
	overridden, ok := options.OnVoiceSaveDevice(voicehost.AudioDeviceKindInput, &mic)().(codextea.VoiceDeviceSavedMsg)
	if !ok || overridden.Err != nil || !overridden.ChannelOverridden {
		t.Fatalf("channel-overridden save = %#v", overridden)
	}

	// A value another layer wins over is reported as overridden, not saved.
	other := "Layer mic"
	recorder.effective = voicehost.AudioDeviceSelection{Microphone: &other}
	shadowed, ok := options.OnVoiceSaveDevice(voicehost.AudioDeviceKindInput, &mic)().(codextea.VoiceDeviceSavedMsg)
	if !ok || shadowed.Err != nil || !shadowed.Overridden {
		t.Fatalf("overridden save = %#v", shadowed)
	}

	// The input-channel hook persists the one-based channel selection.
	channels := &chatwidget.MicrophoneChannels{Values: []uint16{1, 3}}
	recorder.effective = voicehost.AudioDeviceSelection{Channel: []uint16{1, 3}}
	channelSaved, ok := options.OnVoiceSaveInputChannel(channels)().(codextea.VoiceInputChannelSavedMsg)
	if !ok || channelSaved.Err != nil || channelSaved.Overridden {
		t.Fatalf("input channel save = %#v", channelSaved)
	}
	assertVoiceAudioEditsLikeRust(t, recorder.edits[len(recorder.edits)-1], []config.ConfigEdit{
		{KeyPath: "audio.microphone_channel", Value: []uint16{1, 3}, MergeStrategy: config.MergeReplace},
	})

	// The outcomes reach the real TUI: a saved device reports the saved notice,
	// an overridden one reports the overridden notice.
	model := codextea.NewModel(nil, modelOptions)
	model, _ = voiceAudioUpdateLikeRust(t, model, micSaved)
	if got := utils.StripANSI(model.View()); !strings.Contains(got, voiceAudioSavedNoticeLikeRust) {
		t.Fatalf("saved notice missing from the view:\n%s", got)
	}
	model, _ = voiceAudioUpdateLikeRust(t, model, shadowed)
	if got := utils.StripANSI(model.View()); !strings.Contains(got, voiceAudioOverriddenNoticeLikeRust) {
		t.Fatalf("overridden notice missing from the view:\n%s", got)
	}
}

// TestVoiceAudioPreferencesFromSelectionLikeRust covers the settings read
// (Rust #49437 LocalSettings::audio): audio.microphone / audio.speaker name
// devices and audio.microphone_channel is an optional one-based selection.
func TestVoiceAudioPreferencesFromSelectionLikeRust(t *testing.T) {
	mic := "Mono mic"
	speaker := "Headphones"
	preferences := voiceAudioPreferencesFromSelection(voicehost.AudioDeviceSelection{
		Microphone: &mic,
		Speaker:    &speaker,
		Channel:    []uint16{1, 2},
	})
	if preferences.Microphone == nil || *preferences.Microphone != "Mono mic" {
		t.Fatalf("microphone = %#v", preferences.Microphone)
	}
	if preferences.Speaker == nil || *preferences.Speaker != "Headphones" {
		t.Fatalf("speaker = %#v", preferences.Speaker)
	}
	if preferences.MicrophoneChannel == nil || len(preferences.MicrophoneChannel.Values) != 2 {
		t.Fatalf("channels = %#v", preferences.MicrophoneChannel)
	}

	// An absent preference is nil, and the settings rows then say "System
	// default" instead of naming a stale device.
	empty := voiceAudioPreferencesFromSelection(voicehost.AudioDeviceSelection{})
	if empty.Microphone != nil || empty.Speaker != nil || empty.MicrophoneChannel != nil {
		t.Fatalf("empty selection = %#v", empty)
	}

	// The mapping must not alias the config's slice.
	selection := voicehost.AudioDeviceSelection{Channel: []uint16{1}}
	aliased := voiceAudioPreferencesFromSelection(selection)
	selection.Channel[0] = 9
	if aliased.MicrophoneChannel.Values[0] != 1 {
		t.Fatal("the preference aliases the configuration slice")
	}
}

// TestInteractiveLocalVoiceSettingsCarriesAudioLikeRust covers the embedded
// host's settings production point: /voice settings must carry
// audio.microphone / audio.speaker / audio.microphone_channel out of the
// effective configuration, not just the voice catalog.
func TestInteractiveLocalVoiceSettingsCarriesAudioLikeRust(t *testing.T) {
	router := &voiceAudioLocalRouterLikeRust{config: map[string]any{
		"realtime": map[string]any{"voice": "juniper"},
		"audio": map[string]any{
			"microphone":         "Mono mic",
			"speaker":            "Headphones",
			"microphone_channel": float64(2),
		},
	}}
	message := interactiveLocalVoiceSettings(func() interactiveVoiceRouter { return router })()()
	settings, ok := message.(codextea.VoiceSettingsMsg)
	if !ok {
		t.Fatalf("settings message = %T", message)
	}
	if settings.Audio.Microphone == nil || *settings.Audio.Microphone != "Mono mic" {
		t.Fatalf("settings audio microphone = %#v", settings.Audio.Microphone)
	}
	if settings.Audio.Speaker == nil || *settings.Audio.Speaker != "Headphones" {
		t.Fatalf("settings audio speaker = %#v", settings.Audio.Speaker)
	}
	if settings.Audio.MicrophoneChannel == nil || len(settings.Audio.MicrophoneChannel.Values) != 1 || settings.Audio.MicrophoneChannel.Values[0] != 2 {
		t.Fatalf("settings audio channel = %#v", settings.Audio.MicrophoneChannel)
	}

	// A machine without audio keeps the three keys nil.
	router.config = map[string]any{"realtime": map[string]any{"voice": "juniper"}}
	message = interactiveLocalVoiceSettings(func() interactiveVoiceRouter { return router })()()
	settings, ok = message.(codextea.VoiceSettingsMsg)
	if !ok {
		t.Fatalf("settings message = %T", message)
	}
	if settings.Audio.Microphone != nil || settings.Audio.Speaker != nil || settings.Audio.MicrophoneChannel != nil {
		t.Fatalf("empty settings audio = %#v", settings.Audio)
	}
}

// TestLocalVoiceAudioConfigWritesAndReadsThroughRouterLikeRust covers the
// embedded host's configuration surface: audio.* is written with
// config/batchWrite + ReloadUserConfig and the effective values are re-read
// through config/read (Rust App::persist_realtime_audio).
func TestLocalVoiceAudioConfigWritesAndReadsThroughRouterLikeRust(t *testing.T) {
	router := &voiceAudioLocalRouterLikeRust{config: map[string]any{
		"audio": map[string]any{"microphone_channel": float64(3)},
	}}
	cfg := localVoiceAudioConfig(func() interactiveVoiceRouter { return router })

	name := "Mono mic"
	message := voiceSaveDeviceCmd(cfg)(voicehost.AudioDeviceKindInput, &name)()
	saved, ok := message.(codextea.VoiceDeviceSavedMsg)
	if !ok || saved.Err != nil {
		t.Fatalf("save = %#v", message)
	}
	// The saved write clears the channel, so the read-back reports no override.
	if saved.ChannelOverridden || saved.Overridden {
		t.Fatalf("saved = %#v, want a clean save", saved)
	}
	if len(router.methods) != 2 || router.methods[0] != appserver.MethodConfigBatchWrite || router.methods[1] != appserver.MethodConfigRead {
		t.Fatalf("methods = %#v", router.methods)
	}
	if !router.writes[0].ReloadUserConfig {
		t.Fatal("the audio write did not reload the user configuration")
	}
	assertVoiceAudioEditsLikeRust(t, router.writes[0].Edits, []config.ConfigEdit{
		{KeyPath: "audio.microphone", Value: "Mono mic", MergeStrategy: config.MergeReplace},
		{KeyPath: "audio.microphone_channel", Value: nil, MergeStrategy: config.MergeReplace},
	})
	if got, _ := router.config["audio"].(map[string]any); got["microphone"] != "Mono mic" {
		t.Fatalf("effective config = %#v", router.config)
	}
	if audio, _ := router.config["audio"].(map[string]any); audio["microphone_channel"] != nil {
		t.Fatalf("the input channel was not cleared: %#v", audio)
	}
}

// TestRemoteVoiceAudioSettingsAndWritesLikeRust covers the remote host against
// a real websocket app server: the settings read carries the machine-local
// audio preferences and the saving hooks reach config/batchWrite with
// ReloadUserConfig, re-reading config/read for the saved/overridden outcome.
// Rust counterpart: realtime_requests.rs
// microphone_channel_is_persisted_locally_and_reloaded.
func TestRemoteVoiceAudioSettingsAndWritesLikeRust(t *testing.T) {
	server := newVoiceAudioRemoteAppServerLikeRust(t, map[string]any{
		"realtime": map[string]any{"voice": "juniper"},
		"audio": map[string]any{
			"microphone":         "Mono mic",
			"speaker":            "Headphones",
			"microphone_channel": float64(2),
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	message := interactiveRemoteVoiceSettings(server.endpoint)()()
	settings, ok := message.(codextea.VoiceSettingsMsg)
	if !ok {
		t.Fatalf("settings message = %T", message)
	}
	if settings.Current != "juniper" {
		t.Fatalf("current voice = %q", settings.Current)
	}
	if settings.Audio.Microphone == nil || *settings.Audio.Microphone != "Mono mic" ||
		settings.Audio.Speaker == nil || *settings.Audio.Speaker != "Headphones" ||
		settings.Audio.MicrophoneChannel == nil || settings.Audio.MicrophoneChannel.Values[0] != 2 {
		t.Fatalf("remote settings audio = %#v", settings.Audio)
	}

	// The real hook path writes through the endpoint and reads the effective
	// values back.
	cfg := remoteVoiceAudioConfig(server.endpoint)
	name := "Audio interface"
	saved, ok := voiceSaveDeviceCmd(cfg)(voicehost.AudioDeviceKindInput, &name)().(codextea.VoiceDeviceSavedMsg)
	if !ok || saved.Err != nil {
		t.Fatalf("remote save = %#v", saved)
	}
	if saved.ChannelOverridden || saved.Overridden {
		t.Fatalf("remote save = %#v, want a clean save", saved)
	}
	if len(server.writes) != 1 || !server.writes[0].ReloadUserConfig {
		t.Fatalf("writes = %#v", server.writes)
	}
	assertVoiceAudioEditsLikeRust(t, server.writes[0].Edits, []config.ConfigEdit{
		{KeyPath: "audio.microphone", Value: "Audio interface", MergeStrategy: config.MergeReplace},
		{KeyPath: "audio.microphone_channel", Value: nil, MergeStrategy: config.MergeReplace},
	})

	// A server whose write does not become the effective value (another layer
	// wins) is reported as overridden, not saved.
	server.ignoreWrites = true
	shadowed, ok := voiceSaveDeviceCmd(remoteVoiceAudioConfig(server.endpoint))(voicehost.AudioDeviceKindOutput, &name)().(codextea.VoiceDeviceSavedMsg)
	if !ok || shadowed.Err != nil || !shadowed.Overridden {
		t.Fatalf("overridden remote save = %#v", shadowed)
	}
	if ctx.Err() != nil {
		t.Fatalf("context expired: %v", ctx.Err())
	}
}

// voiceAudioLocalRouterLikeRust serves the two configuration methods the audio
// settings use out of one in-memory configuration.
type voiceAudioLocalRouterLikeRust struct {
	config  map[string]any
	methods []appserver.Method
	writes  []config.ConfigBatchWriteParams
}

func (r *voiceAudioLocalRouterLikeRust) Handle(request *appserver.Request) *appserver.Response {
	r.methods = append(r.methods, request.Method)
	response := &appserver.Response{JSONRPC: "2.0", ID: request.ID}
	switch request.Method {
	case appserver.MethodConfigRead:
		response.Result = &config.ConfigReadResponse{Config: r.config}
	case appserver.MethodConfigBatchWrite:
		var params config.ConfigBatchWriteParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			response.Error = &appserver.ResponseError{Code: -32602, Message: err.Error()}
			break
		}
		r.writes = append(r.writes, params)
		applyVoiceAudioConfigEditsLikeRust(r.config, params.Edits)
		response.Result = &config.ConfigWriteResponse{}
	default:
		response.Error = &appserver.ResponseError{Code: -32601, Message: "unexpected method"}
	}
	return response
}

// voiceAudioRemoteAppServerLikeRust serves initialize, config/read,
// config/batchWrite and thread/realtime/listVoices over a real websocket.
type voiceAudioRemoteAppServerLikeRust struct {
	endpoint     *appserverdaemon.RemoteAppServerEndpoint
	config       map[string]any
	ignoreWrites bool
	writes       []config.ConfigBatchWriteParams
}

func newVoiceAudioRemoteAppServerLikeRust(t *testing.T, initial map[string]any) *voiceAudioRemoteAppServerLikeRust {
	t.Helper()
	server := &voiceAudioRemoteAppServerLikeRust{config: initial}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			request, err := remoteTUITestReadRequest(r.Context(), conn)
			if err != nil {
				return
			}
			envelope := map[string]any{"jsonrpc": "2.0", "id": request.ID}
			switch appserver.Method(request.Method) {
			case appserver.MethodInitialize:
				envelope["result"] = map[string]any{}
			case appserver.MethodConfigRead:
				envelope["result"] = map[string]any{"config": server.config, "origins": map[string]any{}}
			case appserver.MethodConfigBatchWrite:
				var params config.ConfigBatchWriteParams
				if err := json.Unmarshal(request.Params, &params); err != nil {
					envelope["error"] = map[string]any{"code": -32602, "message": err.Error()}
					break
				}
				server.writes = append(server.writes, params)
				if !server.ignoreWrites {
					applyVoiceAudioConfigEditsLikeRust(server.config, params.Edits)
				}
				envelope["result"] = map[string]any{}
			case appserver.MethodThreadRealtimeListVoices:
				envelope["result"] = map[string]any{
					"voices": map[string]any{"v1": []string{"maple", "juniper"}, "defaultV1": "maple"},
				}
			default:
				envelope["error"] = map[string]any{"code": -32601, "message": "unexpected method " + request.Method}
			}
			remoteTUITestWrite(r.Context(), conn, envelope)
		}
	}))
	t.Cleanup(httpServer.Close)
	server.endpoint = appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	return server
}

// applyVoiceAudioConfigEditsLikeRust applies dotted-key-path edits the way the
// app server's config service does: a nil value clears the key.
func applyVoiceAudioConfigEditsLikeRust(values map[string]any, edits []config.ConfigEdit) {
	for _, edit := range edits {
		segments := strings.Split(edit.KeyPath, ".")
		table := values
		for _, segment := range segments[:len(segments)-1] {
			child, ok := table[segment].(map[string]any)
			if !ok {
				child = map[string]any{}
				table[segment] = child
			}
			table = child
		}
		leaf := segments[len(segments)-1]
		if edit.Value == nil {
			delete(table, leaf)
			continue
		}
		table[leaf] = edit.Value
	}
}

func assertVoiceAudioEditsLikeRust(t *testing.T, got []config.ConfigEdit, want []config.ConfigEdit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("edits = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index].KeyPath != want[index].KeyPath || got[index].MergeStrategy != want[index].MergeStrategy {
			t.Fatalf("edit[%d] = %#v, want %#v", index, got[index], want[index])
		}
		if !voiceAudioEditValueEqualLikeRust(got[index].Value, want[index].Value) {
			t.Fatalf("edit[%d] value = %#v, want %#v", index, got[index].Value, want[index].Value)
		}
	}
}

func voiceAudioEditValueEqualLikeRust(got any, want any) bool {
	switch wantValue := want.(type) {
	case []uint16:
		gotValues, ok := got.([]uint16)
		if !ok || len(gotValues) != len(wantValue) {
			return false
		}
		for index := range wantValue {
			if gotValues[index] != wantValue[index] {
				return false
			}
		}
		return true
	default:
		return got == want
	}
}

func voiceAudioUpdateLikeRust(t *testing.T, model *codextea.Model, message any) (*codextea.Model, any) {
	t.Helper()
	updated, command := model.Update(message)
	next, ok := updated.(*codextea.Model)
	if !ok {
		t.Fatalf("updated model = %T", updated)
	}
	return next, command
}
