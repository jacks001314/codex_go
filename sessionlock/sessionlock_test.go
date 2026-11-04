package sessionlock

import (
	"errors"
	"testing"
	"time"
)

type scriptedDetector struct {
	results []struct {
		locked bool
		err    error
	}
	index int
}

func (d *scriptedDetector) Locked() (bool, error) {
	if d.index >= len(d.results) {
		return false, nil
	}
	result := d.results[d.index]
	d.index++
	return result.locked, result.err
}

func TestWaitResolvesImmediatelyWhenInitiallyLocked(t *testing.T) {
	detector := &scriptedDetector{}
	detector.results = append(detector.results, struct {
		locked bool
		err    error
	}{true, nil})
	result := Wait(detector, time.Millisecond, nil)
	if !result.Locked || result.Err != nil {
		t.Fatalf("Wait() = %+v, want a locked result", result)
	}
	if detector.index != 1 {
		t.Fatalf("detector calls = %d, want a single initial snapshot", detector.index)
	}
}

func TestWaitReportsPolledLockAndMonitoringFailure(t *testing.T) {
	detector := &scriptedDetector{}
	detector.results = append(detector.results, struct {
		locked bool
		err    error
	}{false, nil}, struct {
		locked bool
		err    error
	}{true, nil})
	result := Wait(detector, time.Millisecond, nil)
	if !result.Locked {
		t.Fatalf("Wait() = %+v, want a locked result after polling", result)
	}

	failing := &scriptedDetector{}
	failing.results = append(failing.results, struct {
		locked bool
		err    error
	}{false, errors.New("boom")})
	if result := Wait(failing, time.Millisecond, nil); result.Err == nil {
		t.Fatalf("Wait() = %+v, want the detector error", result)
	}
}

func TestWaitStopsWhenCancelled(t *testing.T) {
	detector := &scriptedDetector{}
	detector.results = append(detector.results, struct {
		locked bool
		err    error
	}{false, nil})
	stop := make(chan struct{})
	close(stop)
	result := Wait(detector, time.Millisecond, stop)
	if !result.Stopped || result.Locked || result.Err != nil {
		t.Fatalf("Wait() = %+v, want a stopped result", result)
	}
}

func TestWaitWithoutDetectorReportsUnavailable(t *testing.T) {
	if result := Wait(nil, time.Millisecond, nil); !errors.Is(result.Err, ErrUnavailable) {
		t.Fatalf("Wait(nil) = %+v, want ErrUnavailable", result)
	}
}
