package appserver

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"codex_go/chatgptapi"
	"codex_go/telemetry"
)

type turnCostMetricSample struct {
	name string
	inc  int
	tags map[string]string
}

type turnCostRecordingMetrics struct {
	mu      sync.Mutex
	samples []turnCostMetricSample
	notify  chan struct{}
	once    sync.Once
}

func newTurnCostRecordingMetrics() *turnCostRecordingMetrics {
	return &turnCostRecordingMetrics{notify: make(chan struct{})}
}

func (m *turnCostRecordingMetrics) Counter(name string, inc int, tags map[string]string) {
	m.mu.Lock()
	m.samples = append(m.samples, turnCostMetricSample{name: name, inc: inc, tags: cloneTurnCostTags(tags)})
	m.mu.Unlock()
	m.once.Do(func() { close(m.notify) })
}

func (m *turnCostRecordingMetrics) Histogram(string, int, map[string]string) {}

func (m *turnCostRecordingMetrics) HistogramWithBounds(string, int, []float64, map[string]string) {}

func (m *turnCostRecordingMetrics) RecordDuration(string, time.Duration, map[string]string) {}

func (m *turnCostRecordingMetrics) snapshot() []turnCostMetricSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]turnCostMetricSample(nil), m.samples...)
}

func cloneTurnCostTags(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func newTurnCostTestRouter(metrics *turnCostRecordingMetrics) *RuntimeRouter {
	router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})
	tracker := router.turnCostTracker()
	tracker.pollInterval = time.Millisecond
	tracker.maxAttempts = 1
	return router
}

func TestSettledTurnCostRequiresVisibleEstimateAndAllResponsesLikeRust(t *testing.T) {
	makeCost := func(micros *int64, settled []string) []chatgptapi.ChatgptThreadTurnCosts {
		return []chatgptapi.ChatgptThreadTurnCosts{{
			ThreadID: "thread-1",
			Turns: []chatgptapi.ChatgptTurnCost{{
				TurnID:                  "turn-1",
				EstimatedUsageUSDMicros: micros,
				SettledResponseIDs:      settled,
			}},
		}}
	}
	value := func(micros int64) *int64 { return &micros }
	for _, tc := range []struct {
		name        string
		costs       []chatgptapi.ChatgptThreadTurnCosts
		wantEmitted bool
	}{
		{name: "missing estimate", costs: makeCost(nil, []string{"resp-1"})},
		{name: "negative estimate", costs: makeCost(value(-1), []string{"resp-1"})},
		{name: "missing settled id", costs: makeCost(value(1200), []string{"resp-other"})},
		{name: "empty settled ids", costs: makeCost(value(1200), []string{})},
		{name: "settled", costs: makeCost(value(1250001), []string{"resp-1", "resp-extra"}), wantEmitted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metrics := newTurnCostRecordingMetrics()
			router := newTurnCostTestRouter(metrics)
			router.turnCostQuery = func(context.Context, string, []string) ([]chatgptapi.ChatgptThreadTurnCosts, error) {
				return tc.costs, nil
			}
			router.runTurnCostObservation(&turnCostObservation{
				ThreadID:    "thread-1",
				TurnID:      "turn-1",
				ResponseIDs: []string{"resp-1"},
				Interrupted: true,
			})
			samples := metrics.snapshot()
			if !tc.wantEmitted {
				if len(samples) != 0 {
					t.Fatalf("unexpected metric samples = %#v", samples)
				}
				return
			}
			if len(samples) != 1 {
				t.Fatalf("metric samples = %#v, want one", samples)
			}
			sample := samples[0]
			if sample.name != telemetry.TurnCostMicroUSDMetric || sample.inc != 1250001 {
				t.Fatalf("metric = %#v", sample)
			}
			if sample.tags["turn.id"] != "turn-1" || sample.tags["conversation.id"] != "thread-1" || sample.tags["turn.interrupted"] != "true" || sample.tags["auth_mode"] != "Chatgpt" {
				t.Fatalf("metric tags = %#v", sample.tags)
			}
		})
	}
}

func TestTurnCostTrackerTracksEachCompletedResponseOnceLikeRust(t *testing.T) {
	tracker := newTurnCostTracker()
	tracker.recordResponse("thread-1", "turn-1", "resp-1")
	tracker.recordResponse("thread-1", "turn-1", "resp-1")
	tracker.recordResponse("thread-1", "turn-1", "resp-2")
	observation := tracker.finish("thread-1", "turn-1", false)
	if observation == nil {
		t.Fatal("finish() = nil, want a tracked observation")
	}
	if len(observation.ResponseIDs) != 2 || observation.ResponseIDs[0] != "resp-1" || observation.ResponseIDs[1] != "resp-2" {
		t.Fatalf("response ids = %#v", observation.ResponseIDs)
	}
	if again := tracker.finish("thread-1", "turn-1", false); again != nil {
		t.Fatalf("second finish() = %#v, want nil", again)
	}
}

func TestTurnCostTrackerDropsTurnAtResponseCapLikeRust(t *testing.T) {
	tracker := newTurnCostTracker()
	for index := 0; index <= maxTrackedTurnCostResponses; index++ {
		tracker.recordResponse("thread-1", "turn-1", "resp-"+strconv.Itoa(index))
	}
	if observation := tracker.finish("thread-1", "turn-1", false); observation != nil {
		t.Fatalf("finish() = %#v, want the oversized turn dropped", observation)
	}
}

func TestTurnCompletionStartsOneCostQueryLikeRust(t *testing.T) {
	metrics := newTurnCostRecordingMetrics()
	router := newTurnCostTestRouter(metrics)
	queryCalls := make(chan []string, 2)
	router.turnCostQuery = func(_ context.Context, threadID string, turnIDs []string) ([]chatgptapi.ChatgptThreadTurnCosts, error) {
		queryCalls <- append([]string(nil), turnIDs...)
		micros := int64(42)
		return []chatgptapi.ChatgptThreadTurnCosts{{
			ThreadID: threadID,
			Turns: []chatgptapi.ChatgptTurnCost{{
				TurnID:                  "turn-1",
				EstimatedUsageUSDMicros: &micros,
				SettledResponseIDs:      []string{"resp-1"},
			}},
		}}, nil
	}
	router.recordTurnCostResponse("thread-1", "turn-1", "resp-1")
	notification := &TurnCompletedNotification{
		ThreadID: "thread-1",
		Turn:     Turn{ID: "turn-1", Status: TurnStatusCompleted},
	}
	if !router.notifyTurnCompletedOnce(notification) {
		t.Fatal("first turn completion was not claimed")
	}
	if router.notifyTurnCompletedOnce(notification) {
		t.Fatal("second turn completion was unexpectedly claimed")
	}
	select {
	case turnIDs := <-queryCalls:
		if len(turnIDs) != 1 || turnIDs[0] != "turn-1" {
			t.Fatalf("query turn ids = %#v", turnIDs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("turn completion did not start a cost query")
	}
	select {
	case <-metrics.notify:
	case <-time.After(2 * time.Second):
		t.Fatal("settled turn cost did not reach the metric sink")
	}
	select {
	case turnIDs := <-queryCalls:
		t.Fatalf("duplicate completion queried again: %#v", turnIDs)
	default:
	}
}
