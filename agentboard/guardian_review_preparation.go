package agentboard

import (
	"context"
	"errors"
	"fmt"
)

// ErrGuardianReviewCancelled reports that review preparation stopped because
// the review, the harness lifetime or a history reset was cancelled.
var ErrGuardianReviewCancelled = errors.New("Guardian review cancelled")

// ErrGuardianEvidenceUnavailable reports that the root could not supply the
// evidence position or that routing stopped before reaching it.
var ErrGuardianEvidenceUnavailable = errors.New("Guardian evidence unavailable")

// GuardianPreparedReview binds one action's request-time evidence to the root's
// routing watermark (Rust #63363 BoundActorReview). Preparing the review takes
// the cutoff for this attempt and waits until routing reaches it, so the
// assessment that follows sees every original through that position.
type GuardianPreparedReview struct {
	evidence *GuardianReviewEvidence
	routed   *GuardianRoutedEvidence
}

// NewGuardianPreparedReview binds the request-time evidence to the watermark of
// the same root. Evidence and watermark from different roots, or a missing
// piece, are refused rather than acknowledged across histories.
func NewGuardianPreparedReview(evidence *GuardianReviewEvidence, routed *GuardianRoutedEvidence) (*GuardianPreparedReview, error) {
	if evidence == nil || routed == nil {
		return nil, errors.New("Guardian review preparation requires evidence and routing")
	}
	if root := routed.Root(); root == "" || root != evidence.Position().ThreadID {
		return nil, errors.New("Guardian cutoff belongs to another root")
	}
	return &GuardianPreparedReview{evidence: evidence, routed: routed}, nil
}

// Prepare returns the cutoff this attempt routes through once routing reached
// it (Rust actor.rs request_through plus the wait before reserving the
// reviewer). revisions are the root authorization revisions sampled now; when
// they no longer match what binding captured, capture supplies a fresh
// position. Cancellation while waiting is reported as ErrGuardianReviewCancelled;
// a stopped watermark or a failed capture is ErrGuardianEvidenceUnavailable.
func (p *GuardianPreparedReview) Prepare(ctx context.Context, revisions uint64, capture func() (GuardianHistoryPosition, uint64, error)) (GuardianHistoryPosition, error) {
	if p == nil || p.evidence == nil || p.routed == nil {
		return GuardianHistoryPosition{}, fmt.Errorf("%w: review preparation is unavailable", ErrGuardianEvidenceUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return GuardianHistoryPosition{}, fmt.Errorf("%w: %v", ErrGuardianReviewCancelled, err)
	}
	cutoff, err := p.evidence.RequestCutoff(revisions, capture)
	if err != nil {
		return GuardianHistoryPosition{}, fmt.Errorf("%w: %v", ErrGuardianEvidenceUnavailable, err)
	}
	routed, err := p.routed.WaitUntilRouted(ctx, cutoff)
	if err != nil {
		if ctx.Err() != nil {
			return routed, fmt.Errorf("%w: %v", ErrGuardianReviewCancelled, err)
		}
		return routed, fmt.Errorf("%w: %v", ErrGuardianEvidenceUnavailable, err)
	}
	return routed, nil
}
