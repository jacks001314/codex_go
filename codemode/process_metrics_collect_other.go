//go:build !linux

package codemode

import "runtime"

// defaultProcessMetricsCollector has no portable read-only source for the
// process start time, so hosts on other platforms answer UNIMPLEMENTED
// (Rust #57841 keeps the capability optional).
func defaultProcessMetricsCollector() (ProcessMetrics, error) {
	return ProcessMetrics{}, errProcessMetricsUnsupportedPlatform
}

var errProcessMetricsUnsupportedPlatform = runtimeError("process metrics are unsupported on " + runtime.GOOS)

type runtimeError string

func (e runtimeError) Error() string { return string(e) }
