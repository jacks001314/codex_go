package codemode

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func startMetricsHost(t *testing.T, host *grpcStdioHostServer) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	codemodev1.RegisterCodeModeHostServer(server, host)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// Rust #57841: a cumulative snapshot crosses the optional host RPC unchanged.
func TestProcessMetricsSnapshotOverGRPCLikeRust(t *testing.T) {
	want := ProcessMetrics{ProcessID: 42, ProcessStartTimeSeconds: 1_700_000_000, CPUSecondsTotal: 1.5, ResidentMemoryBytes: 4096}
	host := &grpcStdioHostServer{ctx: context.Background()}
	host.metrics = newProcessMetricsSampler(func() (ProcessMetrics, error) { return want, nil })
	conn := startMetricsHost(t, host)

	sample, err := sampleProcessMetrics(context.Background(), conn)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if sample.Kind != tool.CodeModeProcessMetricsSnapshot || sample.Metrics != want {
		t.Fatalf("sample = %+v, want snapshot %+v", sample, want)
	}
}

// Rust #57841: a host without a collector is UNIMPLEMENTED, not a failure.
func TestProcessMetricsUnimplementedHostIsUnsupportedLikeRust(t *testing.T) {
	conn := startMetricsHost(t, &grpcStdioHostServer{ctx: context.Background()})

	sample, err := sampleProcessMetrics(context.Background(), conn)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if sample.Kind != tool.CodeModeProcessMetricsUnsupported {
		t.Fatalf("kind = %v, want unsupported", sample.Kind)
	}
}

// Rust #57841: an impossible snapshot is a failed sample.
func TestProcessMetricsInvalidSnapshotFailsLikeRust(t *testing.T) {
	host := &grpcStdioHostServer{ctx: context.Background()}
	host.metrics = newProcessMetricsSampler(func() (ProcessMetrics, error) {
		return ProcessMetrics{ProcessID: 0, ProcessStartTimeSeconds: 1}, nil
	})
	conn := startMetricsHost(t, host)

	if _, err := sampleProcessMetrics(context.Background(), conn); err == nil || !strings.Contains(err.Error(), "invalid code-mode process metrics snapshot") {
		t.Fatalf("err = %v, want invalid snapshot", err)
	}
}

// Rust #57841: panics, collection errors, and a stuck collector stay contained;
// a blocked collector keeps its single permit so later samples fail fast.
func TestProcessMetricsPanicAndBusyAreContainedLikeRust(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	host := &grpcStdioHostServer{ctx: context.Background()}
	host.metrics = newProcessMetricsSampler(func() (ProcessMetrics, error) {
		switch calls.Add(1) {
		case 1:
			panic("collector panic")
		case 2:
			return ProcessMetrics{}, errors.New("metrics unavailable")
		case 3:
			<-release
			return ProcessMetrics{ProcessID: 7, ProcessStartTimeSeconds: 10}, nil
		default:
			return ProcessMetrics{ProcessID: 8, ProcessStartTimeSeconds: 10}, nil
		}
	})
	conn := startMetricsHost(t, host)

	if _, err := sampleProcessMetrics(context.Background(), conn); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("panic sample err = %v", err)
	}
	if _, err := sampleProcessMetrics(context.Background(), conn); err == nil || !strings.Contains(err.Error(), "metrics unavailable") {
		t.Fatalf("failed sample err = %v", err)
	}
	// The stuck collector outlives the 1s client deadline and keeps the permit.
	if _, err := sampleProcessMetrics(context.Background(), conn); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("stuck sample err = %v", err)
	}
	if _, err := sampleProcessMetrics(context.Background(), conn); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("busy sample err = %v", err)
	}
	close(release)
}

// Rust #57841: a process owner with no live host reports NotRunning and never
// spawns one just to answer a sample.
func TestProcessMetricsNotRunningWithoutHostLikeRust(t *testing.T) {
	provider := NewGrpcProcessProvider("/nonexistent/code-mode-host")

	sample, err := provider.SampleProcessMetrics(context.Background())
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if sample.Kind != tool.CodeModeProcessMetricsNotRunning {
		t.Fatalf("kind = %v, want NotRunning", sample.Kind)
	}
	if provider.connection != nil {
		t.Fatal("metrics sampling spawned a code-mode host")
	}
}

// Rust #57841: a failed sample never retires the shared execution connection.
func TestProcessMetricsFailureKeepsConnectionLikeRust(t *testing.T) {
	var calls atomic.Int32
	provider := &GrpcCodeModeSessionProvider{endpoint: "http://127.0.0.1:1"}
	host := &grpcStdioHostServer{ctx: context.Background()}
	host.metrics = newProcessMetricsSampler(func() (ProcessMetrics, error) {
		if calls.Add(1) == 1 {
			return ProcessMetrics{}, errors.New("transient")
		}
		return ProcessMetrics{ProcessID: 9, ProcessStartTimeSeconds: 10}, nil
	})
	conn := startMetricsHost(t, host)
	provider.connection = &remoteConnection{transport: &GrpcTransport{conn: conn}, alive: true}

	if _, err := provider.SampleProcessMetrics(t.Context()); err == nil {
		t.Fatal("expected first sample to fail")
	}
	if !provider.connection.Alive() {
		t.Fatal("failed sample retired the execution connection")
	}
	sample, err := provider.SampleProcessMetrics(t.Context())
	if err != nil {
		t.Fatalf("second sample: %v", err)
	}
	if sample.Kind != tool.CodeModeProcessMetricsSnapshot || sample.Metrics.ProcessID != 9 {
		t.Fatalf("sample = %+v", sample)
	}
}
