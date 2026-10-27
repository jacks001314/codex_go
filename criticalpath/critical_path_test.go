package criticalpath

import (
	"testing"
	"time"
)

func at(start time.Time, ms int) time.Time {
	return start.Add(time.Duration(ms) * time.Millisecond)
}

func TestJoinReplacesOverlappingWorkWithTheFullWinningBranch(t *testing.T) {
	turnStart := time.Unix(0, 0)
	shared := Path{}.Work(at(turnStart, 0), at(turnStart, 100), WorkInference)
	reviewedTool := shared.Work(at(turnStart, 100), at(turnStart, 300), WorkAutoReview).
		Work(at(turnStart, 300), at(turnStart, 400), WorkTools)
	parallelTool := shared.Work(at(turnStart, 100), at(turnStart, 700), WorkTools)
	path := shared.Work(at(turnStart, 100), at(turnStart, 200), WorkInference).
		Join(at(turnStart, 200), at(turnStart, 700), reviewedTool.Latest(parallelTool))
	got := path.Durations(at(turnStart, 0), at(turnStart, 700))
	want := Durations{Inference: 100 * time.Millisecond, Tools: 600 * time.Millisecond}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
}

func TestNestedSerialDependenciesPreserveExclusiveCategories(t *testing.T) {
	turnStart := time.Unix(0, 0)
	shared := Path{}.Work(at(turnStart, 0), at(turnStart, 100), WorkInference)
	child := shared.Work(at(turnStart, 100), at(turnStart, 300), WorkAutoReview).
		Work(at(turnStart, 300), at(turnStart, 400), WorkTools).
		Work(at(turnStart, 400), at(turnStart, 500), WorkInference)
	path := shared.Work(at(turnStart, 100), at(turnStart, 200), WorkInference).
		Join(at(turnStart, 200), at(turnStart, 600), child)
	got := path.Durations(at(turnStart, 0), at(turnStart, 600))
	want := Durations{
		Inference:  200 * time.Millisecond,
		AutoReview: 200 * time.Millisecond,
		Tools:      200 * time.Millisecond,
	}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
}

func TestEqualFinishTimesSelectOnePathWithoutDoubleCounting(t *testing.T) {
	turnStart := time.Unix(0, 0)
	turnEnd := at(turnStart, 100)
	reviewed := Path{}.Work(turnStart, turnEnd, WorkAutoReview)
	inference := Path{}.Work(turnStart, turnEnd, WorkInference)
	got := reviewed.Latest(inference).Durations(turnStart, turnEnd)
	want := reviewed.Durations(turnStart, turnEnd)
	if got != want {
		t.Fatalf("tie Durations() = %+v, want %+v", got, want)
	}
}

func TestPathRetentionExhaustionMarksUnknownInsteadOfClaimingZero(t *testing.T) {
	turnStart := time.Unix(0, 0)
	var path Path
	for i := 0; i <= MaxPathDepth; i++ {
		path = path.Work(
			turnStart.Add(time.Duration(i)*time.Microsecond),
			turnStart.Add(time.Duration(i+1)*time.Microsecond),
			WorkInference,
		)
	}
	turnEnd := turnStart.Add(time.Duration(MaxPathDepth+1) * time.Microsecond)
	got := path.Durations(turnStart, turnEnd)
	want := Durations{Unknown: turnEnd.Sub(turnStart)}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
}

func TestOverlappingWorkOnlyAttributesTimeAfterTheExistingPath(t *testing.T) {
	turnStart := time.Unix(0, 0)
	path := Path{}.
		Work(at(turnStart, 0), at(turnStart, 10), WorkInference).
		Work(at(turnStart, 5), at(turnStart, 20), WorkTools)
	got := path.Durations(at(turnStart, 0), at(turnStart, 20))
	want := Durations{Inference: 10 * time.Millisecond, Tools: 10 * time.Millisecond}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
	if clipped := path.Work(at(turnStart, 5), at(turnStart, 15), WorkAutoReview); clipped != path {
		t.Fatalf("fully overlapping work changed the path")
	}
	if empty := path.Work(at(turnStart, 20), at(turnStart, 20), WorkUnknown); empty != path {
		t.Fatalf("empty work changed the path")
	}
}

func TestHumanWaitIsRetainedOnlyOnTheSelectedParallelPath(t *testing.T) {
	start := time.Unix(0, 0)
	prefix := Path{}.Work(start, at(start, 100), WorkInference)
	reviewedTool := prefix.Work(at(start, 100), at(start, 200), WorkAutoReview).
		Work(at(start, 200), at(start, 500), WorkUserWait).
		Work(at(start, 500), at(start, 600), WorkTools)
	parallelTool := prefix.Work(at(start, 100), at(start, 400), WorkTools)
	path := prefix.Join(at(start, 100), at(start, 600), parallelTool.Latest(reviewedTool))
	got := path.Durations(start, at(start, 600))
	want := Durations{
		Inference:  100 * time.Millisecond,
		AutoReview: 100 * time.Millisecond,
		UserWait:   300 * time.Millisecond,
		Tools:      100 * time.Millisecond,
	}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
}

func TestDurationsCountsUnrecordedGapsAsOverhead(t *testing.T) {
	start := time.Unix(0, 0)
	path := Path{}.Work(at(start, 10), at(start, 20), WorkInference)
	got := path.Durations(start, at(start, 30))
	want := Durations{
		Inference: 10 * time.Millisecond,
		Overhead:  20 * time.Millisecond,
	}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
}
