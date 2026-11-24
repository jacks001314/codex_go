package codemode

import (
	"context"
	"testing"
	"time"

	"codex_go/tool"
)

// hostPIDForTest reports the current host process, or zero when the provider
// holds no process.
func (p *GrpcProcessProvider) hostPIDForTest() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.process == nil || p.process.Process == nil {
		return 0
	}
	return p.process.Process.Pid
}

// TestGrpcProcessProviderReapsIdleHostLikeRust covers Rust #63537: overlapping
// session leases keep one host alive, the host is reused during the grace
// period, metrics polling neither extends nor revives it, and the process is
// reaped after the last lease drains. The next session starts a fresh host.
func TestGrpcProcessProviderReapsIdleHostLikeRust(t *testing.T) {
	hostProgram := copyProcessHostTestBinary(t)
	t.Setenv(processHostHelperEnv, "cli")
	previousGrace := grpcHostIdleGrace
	grpcHostIdleGrace = 200 * time.Millisecond
	t.Cleanup(func() { grpcHostIdleGrace = previousGrace })

	provider := NewGrpcProcessProvider(hostProgram)
	t.Cleanup(func() { _ = provider.Close() })
	ctx := context.Background()

	open := func() *remoteSession {
		t.Helper()
		session := provider.NewSessionWithOptions(&recordingGrpcDelegate{}, tool.CodeModeSessionOptions{}).(*remoteSession)
		if err := session.establish(ctx); err != nil {
			t.Fatalf("establish session: %v", err)
		}
		return session
	}

	first := open()
	second := open()
	hostPID := provider.hostPIDForTest()
	if hostPID == 0 {
		t.Fatal("provider holds no host after opening sessions")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first session: %v", err)
	}
	time.Sleep(grpcHostIdleGrace / 2)
	if got := provider.hostPIDForTest(); got != hostPID {
		t.Fatalf("host pid = %d while another lease is open, want %d", got, hostPID)
	}

	// Metrics only observe an existing host: sampling must not retain it or
	// restart its idle timer.
	_, _ = provider.SampleProcessMetrics(ctx)
	if got := provider.hostPIDForTest(); got != hostPID {
		t.Fatalf("metrics sampling changed the host pid to %d", got)
	}

	if err := second.Close(); err != nil {
		t.Fatalf("close second session: %v", err)
	}
	// A session opened inside the grace period reuses the same process and
	// cancels retirement.
	time.Sleep(grpcHostIdleGrace / 2)
	third := open()
	if got := provider.hostPIDForTest(); got != hostPID {
		t.Fatalf("session within the grace period started a fresh host: pid %d, want %d", got, hostPID)
	}

	if err := third.Close(); err != nil {
		t.Fatalf("close third session: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for provider.hostPIDForTest() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("idle host was not reaped after its leases drained")
		}
		time.Sleep(10 * time.Millisecond)
	}

	fourth := open()
	if got := provider.hostPIDForTest(); got == 0 {
		t.Fatal("session after retirement did not start a fresh host")
	}
	if err := fourth.Close(); err != nil {
		t.Fatalf("close fourth session: %v", err)
	}
}
