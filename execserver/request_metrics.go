package execserver

import (
	"time"

	"codex_go/metrics"
)

// Rust parity: codex-rs/exec-server/src/telemetry.rs request metrics. The
// dispatcher records one counter and three durations per completed request,
// tagged with the bounded method label and the outcome.
const (
	execServerRequestsTotalMetric             = "exec_server_requests_total"
	execServerRequestsTotalDescription        = "Total number of exec-server requests."
	execServerRequestDurationMetric           = "exec_server_request_duration_seconds"
	execServerRequestDurationDescription      = "Duration of exec-server requests in seconds."
	execServerRequestTotalDurationMetric      = "exec_server_request_total_duration_seconds"
	execServerRequestTotalDurationDescription = "Total exec-server request duration in seconds, including queueing, from decoded receipt until response enqueue or disconnection."
	execServerRequestQueueDurationMetric      = "exec_server_request_queue_duration_seconds"
	execServerRequestQueueDurationDescription = "Time exec-server requests spend queued before execution in seconds."
)

// execServerRequestMethods bounds the method tag to the routes this server
// implements; anything else is reported as "unknown" (Rust labels unrouted
// requests the same way).
var execServerRequestMethods = []string{
	MethodInitialize,
	MethodEnvironmentInfo,
	MethodEnvironmentStatus,
	MethodEnvironmentConfigRead,
	MethodCapabilityRootsDiscover,
	MethodCapabilitiesDiscoverV2,
	MethodProcessStart,
	MethodProcessRead,
	MethodProcessWrite,
	MethodProcessTerminate,
	MethodProcessSignal,
	MethodProcessOutput,
	MethodProcessExited,
	MethodProcessClosed,
	MethodFSReadFile,
	MethodFSOpen,
	MethodFSReadBlock,
	MethodFSWriteBlock,
	MethodFSClose,
	MethodFSWriteFile,
	MethodFSCreateDirectory,
	MethodFSGetMetadata,
	MethodFSWatchMissing,
	MethodFSMissingChanged,
	MethodFSCanonicalize,
	MethodFSReadDirectory,
	MethodFSWalk,
	MethodFSRemove,
	MethodFSCopy,
	MethodHTTPRequest,
	MethodHTTPRequestBodyDelta,
}

var execServerRequestMethodSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(execServerRequestMethods))
	for _, method := range execServerRequestMethods {
		set[method] = struct{}{}
	}
	return set
}()

func execServerRequestMethodLabel(method string) string {
	if _, ok := execServerRequestMethodSet[method]; ok {
		return method
	}
	return "unknown"
}

// execServerWriteResult maps a failed response write to "disconnected"; a
// successful write keeps the dispatch outcome.
func execServerWriteResult(writeErr error) string {
	if writeErr != nil {
		return "disconnected"
	}
	return ""
}

// execServerRequestTiming carries the dispatcher timings for one JSON-RPC
// request. The caller records them once the response has been enqueued, or
// with result "disconnected" when delivery failed.
type execServerRequestTiming struct {
	method     string
	result     string
	receivedAt time.Time
	startedAt  time.Time
}

// record emits the request metrics. An empty result keeps the dispatch
// outcome; "disconnected" replaces it when the response could not be written.
func (t *execServerRequestTiming) record(result string, completedAt time.Time) {
	if t == nil {
		return
	}
	if result == "" {
		result = t.result
	}
	dispatch := completedAt.Sub(t.startedAt)
	total := completedAt.Sub(t.receivedAt)
	queued := t.startedAt.Sub(t.receivedAt)
	if dispatch < 0 {
		dispatch = 0
	}
	if total < dispatch {
		total = dispatch
	}
	if queued < 0 {
		queued = 0
	}
	recordExecServerRequestCompleted(t.method, result, dispatch, total, queued)
}

type describedCounterRecorder interface {
	CounterWithDescription(name string, description string, inc int, tags map[string]string)
}

type secondsDurationRecorder interface {
	RecordDurationSecondsWithDescription(name string, description string, duration time.Duration, tags map[string]string)
}

// recordExecServerRequestCompleted records the request counter plus dispatch,
// total, and queue durations. The global recorder is the process MetricsClient
// installed by otelinit, which implements both described interfaces; recorders
// that do not cannot express these seconds instruments and are skipped for the
// durations.
func recordExecServerRequestCompleted(method string, result string, dispatch time.Duration, total time.Duration, queued time.Duration) {
	recorder := metrics.Global()
	if recorder == nil {
		return
	}
	tags := map[string]string{"method": method, "result": result}
	if counter, ok := recorder.(describedCounterRecorder); ok {
		counter.CounterWithDescription(execServerRequestsTotalMetric, execServerRequestsTotalDescription, 1, tags)
	} else {
		recorder.Counter(execServerRequestsTotalMetric, 1, tags)
	}
	durations, ok := recorder.(secondsDurationRecorder)
	if !ok {
		return
	}
	durations.RecordDurationSecondsWithDescription(execServerRequestDurationMetric, execServerRequestDurationDescription, dispatch, tags)
	durations.RecordDurationSecondsWithDescription(execServerRequestTotalDurationMetric, execServerRequestTotalDurationDescription, total, tags)
	durations.RecordDurationSecondsWithDescription(execServerRequestQueueDurationMetric, execServerRequestQueueDurationDescription, queued, tags)
}
