package appserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/realtime"

	"github.com/coder/websocket"
)

// recordingVoiceMetrics captures the voice lifecycle counters the app-server
// emits.
type recordingVoiceMetrics struct {
	mu        sync.Mutex
	records   []string
	durations []time.Duration
}

func (m *recordingVoiceMetrics) Counter(name string, inc int, tags map[string]string) {
	if inc <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := 0; index < inc; index++ {
		m.records = append(m.records, name)
	}
}

func (m *recordingVoiceMetrics) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, name)
	m.durations = append(m.durations, duration)
}

func (m *recordingVoiceMetrics) durationSnapshot() []time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]time.Duration(nil), m.durations...)
}

func (m *recordingVoiceMetrics) snapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.records...)
}

func (m *recordingVoiceMetrics) count(name string) int {
	total := 0
	for _, record := range m.snapshot() {
		if record == name {
			total++
		}
	}
	return total
}

// hasRealtimeNotification reports whether any queued notification belongs to
// the realtime conversation surface.
func hasRealtimeNotification(sink *NotificationBuffer) bool {
	if sink == nil {
		return false
	}
	for _, notification := range sink.List() {
		switch notification.Method {
		case NotificationThreadRealtimeStarted,
			NotificationThreadRealtimeError,
			NotificationThreadRealtimeClosed,
			NotificationThreadRealtimeSDP:
			return true
		default:
		}
	}
	return false
}

// writeVoiceTestConfig enables the realtime conversation feature.
func writeVoiceTestConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nrealtime_conversation = true\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	return home
}

func TestRealtimeVoiceMetricsRecordLifecycle(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "test-key")

	// A local websocket backend keeps the start off the network, so the test
	// exercises the metric wiring rather than transport reachability.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			if _, _, err := conn.Read(request.Context()); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	metrics := &recordingVoiceMetrics{}
	manager := realtime.NewManager()
	manager.SetTransportBackend(&realtime.TransportBackendConfig{WebsocketBaseURL: server.URL})
	router := NewRuntimeRouter(RuntimeServices{Realtime: manager, VoiceMetrics: metrics})
	t.Cleanup(func() { _ = router.Close() })
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	const threadID = "thread-voice-metrics"

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadRealtimeStart, map[string]any{
		"threadId":       threadID,
		"outputModality": "text",
		"version":        "v2",
	}))
	if response.Error != nil {
		t.Fatalf("thread/realtime/start returned JSON-RPC error: %+v", response.Error)
	}
	// The start runs on the realtime operation queue.
	deadline := time.Now().Add(5 * time.Second)
	for !hasRealtimeNotification(sink) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := metrics.count(voiceSessionStartMetric); got != 1 {
		methods := make([]string, 0, len(sink.List()))
		for _, notification := range sink.List() {
			methods = append(methods, string(notification.Method))
		}
		t.Fatalf("start metric = %d, want 1 (records %#v, notifications %v)", got, metrics.snapshot(), methods)
	}

	response = router.Handle(requestWithParams(t, IntID(2), MethodThreadRealtimeStop, map[string]any{
		"threadId": threadID,
	}))
	if response.Error != nil {
		t.Fatalf("thread/realtime/stop returned JSON-RPC error: %+v", response.Error)
	}
	deadline = time.Now().Add(5 * time.Second)
	for metrics.count(voiceSessionEndedMetric) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := metrics.count(voiceSessionEndedMetric); got != 1 {
		t.Fatalf("ended metric = %d, want 1 (records %#v)", got, metrics.snapshot())
	}
	// The session duration is reported once, on the closed notification.
	deadline = time.Now().Add(5 * time.Second)
	for len(metrics.durationSnapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	durations := metrics.durationSnapshot()
	if len(durations) != 1 {
		t.Fatalf("duration records = %#v, want 1", durations)
	}
	if durations[0] < 0 {
		t.Fatalf("duration = %v", durations[0])
	}
	if count := metrics.count(voiceSessionDurationMetric); count != 1 {
		t.Fatalf("duration metric = %d, want 1 (records %#v)", count, metrics.snapshot())
	}
}

func TestRealtimeVoiceMetricsRecordFailure(t *testing.T) {
	home := writeVoiceTestConfig(t)
	// Without credentials the start is refused before any session exists.
	t.Setenv(auth.OpenAIAPIKeyEnv, "")

	metrics := &recordingVoiceMetrics{}
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		VoiceMetrics: metrics,
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(NewNotificationBuffer())

	router.Handle(requestWithParams(t, IntID(1), MethodThreadRealtimeStart, map[string]any{
		"threadId":       "thread-voice-failure",
		"outputModality": "audio",
	}))
	deadline := time.Now().Add(5 * time.Second)
	for metrics.count(voiceSessionFailureMetric) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := metrics.count(voiceSessionFailureMetric); got != 1 {
		t.Fatalf("failure metric = %d, want 1 (records %#v)", got, metrics.snapshot())
	}
	if got := metrics.count(voiceSessionStartMetric); got != 0 {
		t.Fatalf("a refused start counted as started: %#v", metrics.snapshot())
	}
}

func TestRealtimeVoiceMetricsAreOptional(t *testing.T) {
	home := writeVoiceTestConfig(t)
	t.Setenv(auth.OpenAIAPIKeyEnv, "test-key")
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(NewNotificationBuffer())

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadRealtimeStart, map[string]any{
		"threadId":       "thread-voice-nometrics",
		"outputModality": "audio",
	}))
	if response.Error != nil {
		t.Fatalf("thread/realtime/start without a metric sink returned %+v", response.Error)
	}
}
