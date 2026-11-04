package criticalpath

import (
	"testing"
	"time"
)

func reviewAt(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * time.Millisecond) }

func TestReviewExecutionOverheadExcludesObservedChildExecution(t *testing.T) {
	review := NewReviewExecution()
	review.Observe()
	review.Started(reviewAt(200))
	review.Completed(reviewAt(200), reviewAt(500))
	review.Started(reviewAt(400))
	review.Completed(reviewAt(400), reviewAt(700))
	review.Seal()
	got, ok := reviewOverhead([]*ReviewExecution{review}, reviewAt(100), reviewAt(800))
	if !ok || got != 200*time.Millisecond {
		t.Fatalf("overhead = %v ok=%v, want 200ms true", got, ok)
	}
	path := Path{}.AutoReview(reviewAt(100), reviewAt(800), []*ReviewExecution{review})
	totals := path.Durations(reviewAt(100), reviewAt(800))
	if totals.AutoReview != 700*time.Millisecond || totals.AutoReviewOverhead != 200*time.Millisecond || totals.AutoReviewOverheadUnknown {
		t.Fatalf("durations = %+v", totals)
	}
}

func TestReviewExecutionObservedWithoutChildrenIsAllOverhead(t *testing.T) {
	review := NewReviewExecution()
	review.Observe()
	review.Seal()
	got, ok := reviewOverhead([]*ReviewExecution{review}, reviewAt(0), reviewAt(250))
	if !ok || got != 250*time.Millisecond {
		t.Fatalf("overhead = %v ok=%v, want 250ms true", got, ok)
	}
}

func TestReviewExecutionPendingAttemptOrUnknownMakesOverheadUnknown(t *testing.T) {
	pending := NewReviewExecution()
	pending.Observe()
	attempt := pending.StartAttempt()
	if _, ok := reviewOverhead([]*ReviewExecution{pending}, reviewAt(0), reviewAt(100)); ok {
		t.Fatal("pending attempt reported complete overhead")
	}
	attempt.Finish(true)
	if got, ok := reviewOverhead([]*ReviewExecution{pending}, reviewAt(0), reviewAt(100)); !ok || got != 100*time.Millisecond {
		t.Fatalf("finished attempt overhead = %v ok=%v", got, ok)
	}

	unknown := NewReviewExecution()
	unknown.Observe()
	unknown.MarkUnknown()
	unknown.Seal()
	if _, ok := reviewOverhead([]*ReviewExecution{unknown}, reviewAt(0), reviewAt(100)); ok {
		t.Fatal("unknown attribution reported overhead")
	}
	if _, ok := reviewOverhead(nil, reviewAt(0), reviewAt(100)); ok {
		t.Fatal("missing reviews reported overhead")
	}
}

func TestReviewExecutionSealFreezesEvidenceAndDuplicateStartsAreUnknown(t *testing.T) {
	review := NewReviewExecution()
	review.Observe()
	review.Started(reviewAt(10))
	review.Started(reviewAt(10))
	review.Completed(reviewAt(10), reviewAt(20))
	review.Seal()
	review.Started(reviewAt(30))
	review.Completed(reviewAt(30), reviewAt(40))
	if _, ok := reviewOverhead([]*ReviewExecution{review}, reviewAt(0), reviewAt(50)); ok {
		t.Fatal("duplicate start time was reported as known")
	}

	late := NewReviewExecution()
	late.Observe()
	late.Seal()
	late.MarkUnknown()
	if got, ok := reviewOverhead([]*ReviewExecution{late}, reviewAt(0), reviewAt(50)); !ok || got != 50*time.Millisecond {
		t.Fatalf("post-seal callback changed evidence: %v ok=%v", got, ok)
	}
}
