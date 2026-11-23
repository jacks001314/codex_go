package turn

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"codex_go/tool"
)

// Rust #62357: reasons are deduplicated per turn and keep their first
// observation order.
func TestTimingStateReportsDeduplicatedIncompleteReasonsLikeRust(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.MarkTurnStarted(start)
	timing.InvalidateCriticalPath(CriticalPathReasonCodeModeUnsupported)
	timing.InvalidateCriticalPath(CriticalPathReasonUnattributedUserWait)
	timing.InvalidateCriticalPath(CriticalPathReasonCodeModeUnsupported)

	profile := timing.CompleteProfile(start.Add(time.Second))
	if profile.CriticalPath.Status != CriticalPathStatusIncomplete {
		t.Fatalf("status = %q, want incomplete", profile.CriticalPath.Status)
	}
	want := []string{CriticalPathReasonCodeModeUnsupported, CriticalPathReasonUnattributedUserWait}
	if !reflect.DeepEqual(profile.CriticalPath.IncompleteReasons, want) {
		t.Fatalf("reasons = %#v, want %#v", profile.CriticalPath.IncompleteReasons, want)
	}
}

func TestTimingStateUnclassifiedReasonFallbackLikeRust(t *testing.T) {
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.MarkTurnStarted(time.Unix(1_700_000_000, 0))
	timing.InvalidateCriticalPath()
	profile := timing.CompleteProfile(time.Unix(1_700_000_001, 0))
	if !reflect.DeepEqual(profile.CriticalPath.IncompleteReasons, []string{CriticalPathReasonUnclassified}) {
		t.Fatalf("reasons = %#v, want unclassified", profile.CriticalPath.IncompleteReasons)
	}
}

func TestTimingStateMissingTurnStartReasonLikeRust(t *testing.T) {
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.InvalidateCriticalPath(CriticalPathReasonCodeModeUnsupported)
	profile := timing.CompleteProfile(time.Unix(1_700_000_001, 0))
	want := []string{CriticalPathReasonCodeModeUnsupported, CriticalPathReasonMissingTurnStart}
	if !reflect.DeepEqual(profile.CriticalPath.IncompleteReasons, want) {
		t.Fatalf("reasons = %#v, want %#v", profile.CriticalPath.IncompleteReasons, want)
	}
}

func TestTimingStateConcurrentReasonsDeduplicateLikeRust(t *testing.T) {
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.MarkTurnStarted(time.Unix(1_700_000_000, 0))
	reasons := []string{
		CriticalPathReasonCodeModeUnsupported,
		CriticalPathReasonUnsupportedOperation,
		CriticalPathReasonUnattributedReview,
		CriticalPathReasonUnattributedUserWait,
	}
	var wg sync.WaitGroup
	for iteration := 0; iteration < 8; iteration++ {
		for _, reason := range reasons {
			wg.Add(1)
			go func(reason string) {
				defer wg.Done()
				timing.InvalidateCriticalPath(reason)
			}(reason)
		}
	}
	wg.Wait()
	profile := timing.CompleteProfile(time.Unix(1_700_000_001, 0))
	if len(profile.CriticalPath.IncompleteReasons) != len(reasons) {
		t.Fatalf("reasons = %#v, want each reason once", profile.CriticalPath.IncompleteReasons)
	}
	seen := map[string]bool{}
	for _, reason := range profile.CriticalPath.IncompleteReasons {
		if seen[reason] {
			t.Fatalf("duplicate reason %q in %#v", reason, profile.CriticalPath.IncompleteReasons)
		}
		seen[reason] = true
	}
}

func TestTimingStateSnapshotReportsIncompleteReasonsLikeRust(t *testing.T) {
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.MarkTurnStarted(time.Unix(1_700_000_000, 0))
	timing.InvalidateCriticalPath(CriticalPathReasonUnattributedReview)
	report := timing.CriticalPathReportSnapshot()
	if report.Status != CriticalPathStatusIncomplete || len(report.IncompleteReasons) != 1 || report.IncompleteReasons[0] != CriticalPathReasonUnattributedReview {
		t.Fatalf("snapshot = %#v", report)
	}
}

// Rust #62357: external tools reusing a built-in name are not classified as
// that built-in operation.
func TestDirectPathIncompleteReasonClassifiesLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		invocation *tool.Invocation
		want       string
	}{
		{name: "nil", invocation: nil, want: CriticalPathReasonUnsupportedOperation},
		{name: "code mode source", invocation: &tool.Invocation{Source: ToolTraceSourceCodeMode}, want: CriticalPathReasonCodeModeUnsupported},
		{name: "code mode tool", invocation: &tool.Invocation{ToolName: tool.ToolName{Name: tool.CodeModeExecToolName}}, want: CriticalPathReasonCodeModeUnsupported},
		{name: "multi-agent name", invocation: &tool.Invocation{ToolName: tool.ToolName{Name: "send_input"}}, want: CriticalPathReasonUnsupportedOperation},
		{name: "namespaced collision", invocation: &tool.Invocation{ToolName: tool.ToolName{Namespace: "external", Name: "send_input"}}, want: CriticalPathReasonUnsupportedOperation},
	} {
		if got := directPathIncompleteReason(testCase.invocation); got != testCase.want {
			t.Fatalf("%s reason = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}
