package state

import "slices"

// ServingAuthorization is the authorization and model policy a serving
// assessment was produced under (Rust #58075's ScoreAuthorization). A retained
// assessment may only skip synchronous review while this snapshot still
// matches the invocation's current evidence.
type ServingAuthorization struct {
	// EnvironmentID is the environment the permission evidence belongs to.
	EnvironmentID *string
	// DeniedPaths and DeniedGlobs are the effective denied read roots of the
	// reviewed environment.
	DeniedPaths []string
	DeniedGlobs []string
	// RetainedContextComplete reports that the captured retained context is
	// whole; an incomplete snapshot forces a fresh review.
	RetainedContextComplete bool
	// PolicyFingerprint identifies the model, approval policy, and permission
	// mode in effect when the assessment was issued.
	PolicyFingerprint string
}

// Matches reports whether two authorization snapshots describe the same
// evidence and policy.
func (a ServingAuthorization) Matches(b ServingAuthorization) bool {
	return sameOptionalString(a.EnvironmentID, b.EnvironmentID) &&
		slices.Equal(a.DeniedPaths, b.DeniedPaths) &&
		slices.Equal(a.DeniedGlobs, b.DeniedGlobs) &&
		a.RetainedContextComplete == b.RetainedContextComplete &&
		a.PolicyFingerprint == b.PolicyFingerprint
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// ServingAssessment is one final serving value retained with the tool
// invocation (or code cell) that produced it (Rust #58075's ServingAssessment).
// A matching low-risk value lets adaptive review skip the synchronous review;
// an elevated-risk value still requires it.
type ServingAssessment struct {
	RequiresReview bool
	Authorization  ServingAuthorization
}

// AllowsSynchronousReviewSkip reports whether this retained assessment may
// replace the synchronous review for the invocation's current authorization.
func (a ServingAssessment) AllowsSynchronousReviewSkip(current ServingAuthorization) bool {
	return !a.RequiresReview && a.Authorization.Matches(current)
}
