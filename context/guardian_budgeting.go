package context

// GuardianContextBudgeting selects how Guardian request evidence is prepared
// (Rust guardian-context ContextBudgeting, #58454).
//
// Fixed keeps the per-section request caps: evidence is filtered and
// truncated while the request is prepared. Elastic preserves the captured
// candidates so a later elastic allocation decides which whole entries to
// keep.
type GuardianContextBudgeting int

const (
	// GuardianContextBudgetingFixed is the default per-section request policy.
	GuardianContextBudgetingFixed GuardianContextBudgeting = iota
	// GuardianContextBudgetingElastic preserves candidates without the fixed
	// request caps.
	GuardianContextBudgetingElastic
)
