package metrics

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"
)

// Rust parity: codex-rs/otel/src/metrics/buffered.rs (#47619). Global operation
// pairs are buffered until a metrics provider is installed, then drained as a
// unit so a count and its duration stay on one exporter. Disabling metrics
// discards pending pairs and suppresses recording until another installation.

const (
	// MaxPendingGlobalOperations is Rust's MAX_PENDING_OPERATIONS.
	MaxPendingGlobalOperations = 256
	// MaxGlobalOperationMetadataBytes is Rust's MAX_METADATA_BYTES.
	MaxGlobalOperationMetadataBytes = 1024
	// MaxGlobalOperationTags is Rust's MAX_TAGS.
	MaxGlobalOperationTags = 16
)

var (
	// ErrGlobalOperationMetadataTooLarge reports an over-limit tag set.
	ErrGlobalOperationMetadataTooLarge = errors.New("global operation metadata exceeds the size limit")
	// ErrEmptyGlobalMetricName reports a missing count or duration instrument.
	ErrEmptyGlobalMetricName = errors.New("metric name must not be empty")
	// ErrInvalidGlobalMetricName reports an unsupported instrument name.
	ErrInvalidGlobalMetricName = errors.New("metric name must contain only alphanumeric characters, '.', '_' or '-'")
	// ErrEmptyGlobalMetricTag reports a missing tag key or value.
	ErrEmptyGlobalMetricTag = errors.New("metric tag component must not be empty")
	// ErrInvalidGlobalMetricTag reports an unsupported tag key or value.
	ErrInvalidGlobalMetricTag = errors.New("metric tag component must contain only alphanumeric characters, '.', '_', '-' or '/'")
)

type globalOperationTag struct {
	key   string
	value string
}

type pendingGlobalOperation struct {
	countName    string
	durationName string
	duration     time.Duration
	tags         []globalOperationTag
}

type globalOperationState int

const (
	globalOperationStartup globalOperationState = iota
	globalOperationReady
	globalOperationDisabled
)

type bufferedGlobalMetrics struct {
	mu       sync.Mutex
	state    globalOperationState
	recorder Recorder
	pending  []pendingGlobalOperation
}

var globalOperationBuffer = bufferedGlobalMetrics{state: globalOperationStartup}

// RecordGlobalOperation records one operation count and its duration in
// milliseconds. Before a provider is installed it retains at most
// MaxPendingGlobalOperations complete pairs. Validation happens before either
// half is accepted; overflow and disabled states are no-ops.
func RecordGlobalOperation(countName string, durationName string, duration time.Duration, tags map[string]string) error {
	operation, err := validateGlobalOperation(countName, durationName, duration, tags)
	if err != nil {
		return err
	}
	globalOperationBuffer.record(operation)
	return nil
}

func validateGlobalOperation(countName string, durationName string, duration time.Duration, tags map[string]string) (pendingGlobalOperation, error) {
	if len(tags) > MaxGlobalOperationTags {
		return pendingGlobalOperation{}, ErrGlobalOperationMetadataTooLarge
	}
	metadataBytes := len(countName) + len(durationName)
	for key, value := range tags {
		metadataBytes += len(key) + len(value)
	}
	if metadataBytes > MaxGlobalOperationMetadataBytes {
		return pendingGlobalOperation{}, ErrGlobalOperationMetadataTooLarge
	}
	for _, name := range []string{countName, durationName} {
		if err := validateGlobalMetricName(name); err != nil {
			return pendingGlobalOperation{}, err
		}
	}
	operation := pendingGlobalOperation{
		countName:    countName,
		durationName: durationName,
		duration:     duration,
		tags:         make([]globalOperationTag, 0, len(tags)),
	}
	for key, value := range tags {
		if err := validateGlobalMetricTag(key); err != nil {
			return pendingGlobalOperation{}, err
		}
		if err := validateGlobalMetricTag(value); err != nil {
			return pendingGlobalOperation{}, err
		}
		operation.tags = append(operation.tags, globalOperationTag{key: key, value: value})
	}
	return operation, nil
}

func validateGlobalMetricName(name string) error {
	if name == "" {
		return ErrEmptyGlobalMetricName
	}
	if len(name) > 255 || !isASCIIAlpha(name[0]) {
		return fmt.Errorf("%w: %s", ErrInvalidGlobalMetricName, name)
	}
	for _, character := range name {
		if isASCIIAlphanumeric(character) || character == '.' || character == '_' || character == '-' {
			continue
		}
		return fmt.Errorf("%w: %s", ErrInvalidGlobalMetricName, name)
	}
	return nil
}

func validateGlobalMetricTag(value string) error {
	if value == "" {
		return ErrEmptyGlobalMetricTag
	}
	for _, character := range value {
		if isASCIIAlphanumeric(character) || character == '.' || character == '_' || character == '-' || character == '/' {
			continue
		}
		return ErrInvalidGlobalMetricTag
	}
	return nil
}

func isASCIIAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isASCIIAlphanumeric(value rune) bool {
	return value >= '0' && value <= '9' ||
		value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z'
}

func (b *bufferedGlobalMetrics) record(operation pendingGlobalOperation) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case globalOperationStartup:
		if len(b.pending) < MaxPendingGlobalOperations {
			b.pending = append(b.pending, operation)
		}
	case globalOperationReady:
		recordGlobalOperation(b.recorder, operation)
	}
}

func (b *bufferedGlobalMetrics) enable(recorder Recorder) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == globalOperationStartup {
		for _, operation := range b.pending {
			recordGlobalOperation(recorder, operation)
		}
	}
	b.pending = nil
	b.recorder = recorder
	b.state = globalOperationReady
}

func (b *bufferedGlobalMetrics) disable() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = nil
	b.recorder = nil
	b.state = globalOperationDisabled
}

func (b *bufferedGlobalMetrics) suspend(recorder Recorder) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// A replaced provider may shut down after its successor is installed. It
	// must not detach that successor or undo an explicit opt-out.
	if b.state != globalOperationReady || !sameRecorder(b.recorder, recorder) {
		return
	}
	b.pending = nil
	b.recorder = nil
	b.state = globalOperationStartup
}

func recordGlobalOperation(recorder Recorder, operation pendingGlobalOperation) {
	if recorder == nil {
		return
	}
	tags := make(map[string]string, len(operation.tags))
	for _, tag := range operation.tags {
		tags[tag.key] = tag.value
	}
	recorder.Counter(operation.countName, 1, tags)
	recorder.RecordDuration(operation.durationName, operation.duration, tags)
}

// SuspendGlobal resumes buffering when the matching installed metrics client
// shuts down. A newer installation is left untouched.
func SuspendGlobal(recorder Recorder) {
	globalOperationBuffer.suspend(recorder)
}

func sameRecorder(left Recorder, right Recorder) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftType := reflect.TypeOf(left)
	if leftType != reflect.TypeOf(right) {
		return false
	}
	if leftType.Comparable() {
		return left == right
	}
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	switch leftValue.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return leftValue.Pointer() == rightValue.Pointer()
	default:
		return false
	}
}
