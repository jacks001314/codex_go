package integrity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/sandbox"
)

type runnerMetricRecord struct {
	name string
	tags map[string]string
}

type runnerTestMetrics struct {
	mu        sync.Mutex
	counters  []runnerMetricRecord
	durations []runnerMetricRecord
}

func (m *runnerTestMetrics) Counter(name string, _ int, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters = append(m.counters, runnerMetricRecord{name: name, tags: cloneTags(tags)})
}

func (m *runnerTestMetrics) RecordDuration(name string, _ time.Duration, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.durations = append(m.durations, runnerMetricRecord{name: name, tags: cloneTags(tags)})
}

func TestRunIntegrityChecksRecordsDependencyOutcomesWithoutPaths(t *testing.T) {
	root := t.TempDir()
	writable := filepath.Join(root, "writable")
	protected := filepath.Join(root, "protected")
	if err := os.WriteFile(writable, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write writable fixture: %v", err)
	}
	if err := os.WriteFile(protected, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write protected fixture: %v", err)
	}
	entries := []sandbox.FileSystemSandboxEntry{
		{Path: sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "root"}}, Access: sandbox.FileSystemAccessWrite},
		{Path: sandbox.FileSystemPath{Type: "path", Path: protected}, Access: sandbox.FileSystemAccessRead},
	}
	recorder := &runnerTestMetrics{}
	runIntegrityChecks(context.Background(), &RunnerRequest{
		SandboxType: platformSandboxType(),
		Entries:     entries,
		CWD:         root,
		Command:     []string{writable},
	}, recorder)

	var foundFinding bool
	for _, counter := range recorder.counters {
		if counter.name != RunnerCheckMetric {
			t.Fatalf("counter name = %q", counter.name)
		}
		if counter.tags["dependency"] == "sandbox_launcher" && counter.tags["outcome"] == "finding" {
			foundFinding = true
		}
		for _, value := range counter.tags {
			if strings.Contains(value, root) {
				t.Fatalf("metric tag leaked path: %#v", counter.tags)
			}
		}
	}
	if !foundFinding {
		t.Fatalf("sandbox launcher finding missing: %#v", recorder.counters)
	}
	names := map[string]bool{}
	for _, duration := range recorder.durations {
		names[duration.name] = true
		for _, value := range duration.tags {
			if strings.Contains(value, root) {
				t.Fatalf("duration tag leaked path: %#v", duration.tags)
			}
		}
	}
	if !names[RunnerPreparationMetric] || !names[RunnerDurationMetric] {
		t.Fatalf("duration metrics = %#v", names)
	}
}

func TestRunIntegrityChecksSkipsEscalationAndFullDiskWrite(t *testing.T) {
	root := t.TempDir()
	full := []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "root"}},
		Access: sandbox.FileSystemAccessWrite,
	}}
	for _, request := range []*RunnerRequest{
		{SandboxType: platformSandboxType(), CWD: root, Entries: full, SandboxOverride: "escalatedSandboxWithRestrictions"},
		{SandboxType: platformSandboxType(), CWD: root, Entries: full},
		{SandboxType: sandbox.SandboxTypeNone, CWD: root, Entries: full},
	} {
		recorder := &runnerTestMetrics{}
		runIntegrityChecks(context.Background(), request, recorder)
		if len(recorder.counters) != 0 || len(recorder.durations) != 0 {
			t.Fatalf("ineligible request emitted metrics: %#v %#v", recorder.counters, recorder.durations)
		}
	}
}

func TestRunIntegrityChecksTimesOutWithoutLateMetrics(t *testing.T) {
	root := t.TempDir()
	entries := []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "root"}},
		Access: sandbox.FileSystemAccessWrite,
	}, {
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: filepath.Join(root, "*.lock")},
		Access: sandbox.FileSystemAccessDeny,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	previousWork := integrityRunnerWork
	integrityRunnerWork = func(*RunnerRequest) runnerResult {
		<-ctx.Done()
		return runnerResult{}
	}
	defer func() { integrityRunnerWork = previousWork }()
	recorder := &runnerTestMetrics{}
	runIntegrityChecks(ctx, &RunnerRequest{
		SandboxType: platformSandboxType(),
		Entries:     entries,
		CWD:         root,
	}, recorder)
	var sawTimeout bool
	for _, counter := range recorder.counters {
		if counter.tags["error_kind"] == "timeout" {
			sawTimeout = true
		}
	}
	if !sawTimeout {
		t.Fatalf("timeout outcome missing: %#v", recorder.counters)
	}
}
