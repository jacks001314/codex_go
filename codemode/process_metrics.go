package codemode

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Rust parity: #57841 process observations are independent of sessions. A
// process-owned provider observes an existing child; a remote transport keeps
// its own connection policy, so a failed sample never retires the execution
// connection. Absence of a running host and an unsupported collector are
// distinct from a failed sample.
type ProcessMetrics = tool.CodeModeProcessMetrics

type ProcessMetricsSample = tool.CodeModeProcessMetricsSample

// CodeModeProcessMetricsCollector captures any resources it needs before the
// host enters its sandbox. It must be a bounded, read-only observation that does
// not acquire execution locks.
type CodeModeProcessMetricsCollector func() (ProcessMetrics, error)

var errInvalidProcStat = errors.New("invalid /proc/self/stat")

var (
	errProcessMetricsBusy     = errors.New("process metrics collector is busy")
	errProcessMetricsPanicked = errors.New("process metrics collector panicked")
)

// ValidateProcessMetrics mirrors the client-side snapshot checks of Rust #57841.
func ValidateProcessMetrics(metrics ProcessMetrics) error {
	if metrics.ProcessID == 0 ||
		math.IsNaN(metrics.ProcessStartTimeSeconds) || math.IsInf(metrics.ProcessStartTimeSeconds, 0) ||
		metrics.ProcessStartTimeSeconds <= 0 ||
		math.IsNaN(metrics.CPUSecondsTotal) || math.IsInf(metrics.CPUSecondsTotal, 0) ||
		metrics.CPUSecondsTotal < 0 {
		return errors.New("invalid code-mode process metrics snapshot")
	}
	return nil
}

// processMetricsSampler bounds collection to one worker. A collector that never
// returns keeps its permit, so later samples fail fast instead of starting more
// workers, and the detached worker is never joined.
type processMetricsSampler struct {
	collector CodeModeProcessMetricsCollector
	permit    chan struct{}
}

func newProcessMetricsSampler(collector CodeModeProcessMetricsCollector) *processMetricsSampler {
	return &processMetricsSampler{collector: collector, permit: make(chan struct{}, 1)}
}

type processMetricsResult struct {
	metrics ProcessMetrics
	err     error
}

func (s *processMetricsSampler) sample(ctx context.Context) (ProcessMetrics, error) {
	if s == nil || s.collector == nil {
		return ProcessMetrics{}, status.Error(codes.Unimplemented, "process metrics are unsupported")
	}
	select {
	case s.permit <- struct{}{}:
	default:
		return ProcessMetrics{}, status.Error(codes.ResourceExhausted, errProcessMetricsBusy.Error())
	}
	result := make(chan processMetricsResult, 1)
	go func() {
		defer func() { <-s.permit }()
		defer func() {
			if recovered := recover(); recovered != nil {
				result <- processMetricsResult{err: status.Error(codes.Internal, errProcessMetricsPanicked.Error())}
			}
		}()
		metrics, err := s.collector()
		if err != nil {
			err = status.Error(codes.Internal, "process metrics collection failed: "+err.Error())
		}
		result <- processMetricsResult{metrics: metrics, err: err}
	}()
	select {
	case <-ctx.Done():
		return ProcessMetrics{}, status.FromContextError(ctx.Err()).Err()
	case out := <-result:
		return out.metrics, out.err
	}
}

// sampleProcessMetrics over an existing connection. It never dials, retries, or
// resets the connection: failures affect only this RPC.
func sampleProcessMetrics(ctx context.Context, conn *grpc.ClientConn) (ProcessMetricsSample, error) {
	if conn == nil {
		return ProcessMetricsSample{Kind: tool.CodeModeProcessMetricsNotRunning}, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	response, err := codemodev1.NewCodeModeHostClient(conn).GetProcessMetrics(callCtx, &codemodev1.GetProcessMetricsRequest{})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return ProcessMetricsSample{Kind: tool.CodeModeProcessMetricsUnsupported}, nil
		}
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return ProcessMetricsSample{}, fmt.Errorf("code-mode process metrics timed out: %w", err)
		}
		return ProcessMetricsSample{}, fmt.Errorf("code-mode process metrics failed: %w", err)
	}
	snapshot := response
	metrics := ProcessMetrics{
		ProcessID:               snapshot.GetProcessId(),
		ProcessStartTimeSeconds: snapshot.GetProcessStartTimeSeconds(),
		CPUSecondsTotal:         snapshot.GetCpuSecondsTotal(),
		ResidentMemoryBytes:     snapshot.GetResidentMemoryBytes(),
	}
	if err := ValidateProcessMetrics(metrics); err != nil {
		return ProcessMetricsSample{}, err
	}
	return ProcessMetricsSample{Kind: tool.CodeModeProcessMetricsSnapshot, Metrics: metrics}, nil
}

// SampleProcessMetrics reports the host's cumulative process observation
// (Rust #57841). A process owner that knows no host is running reports
// NotRunning without spawning one.
func (p *GrpcProcessProvider) SampleProcessMetrics(ctx context.Context) (ProcessMetricsSample, error) {
	if p == nil {
		return ProcessMetricsSample{}, errors.New("code-mode gRPC process provider is nil")
	}
	p.mu.Lock()
	conn := p.grpcConn
	alive := p.connection != nil && p.connection.Alive()
	p.mu.Unlock()
	if !alive || conn == nil {
		return ProcessMetricsSample{Kind: tool.CodeModeProcessMetricsNotRunning}, nil
	}
	return sampleProcessMetrics(ctx, conn)
}

// grpcConnOf returns the gRPC connection backing a shared remote connection, if
// the transport is the gRPC one.
func grpcConnOf(connection *remoteConnection) *grpc.ClientConn {
	if connection == nil {
		return nil
	}
	if transport, ok := connection.transport.(*GrpcTransport); ok {
		return transport.conn
	}
	return nil
}

// SampleProcessMetrics follows the URL provider's transport policy: it may
// establish the network connection, but a failed sample never retires it
// (Rust #57841).
func (p *GrpcCodeModeSessionProvider) SampleProcessMetrics(ctx context.Context) (ProcessMetricsSample, error) {
	if p == nil {
		return ProcessMetricsSample{}, errors.New("code-mode gRPC provider is nil")
	}
	connection, err := p.connect(ctx)
	if err != nil {
		return ProcessMetricsSample{}, err
	}
	return sampleProcessMetrics(ctx, grpcConnOf(connection))
}
