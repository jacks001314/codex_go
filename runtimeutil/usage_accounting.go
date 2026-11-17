package runtimeutil

// AgentUsageReport is one completed response's bill handed to a host's usage
// accounting (Rust AgentUsageReport, core/src/agent/host/usage.rs, #61958).
// Usage is nil when the response reported no provider totals; the hook
// contribution then travels on AdditionalBudgetUnits.
type AgentUsageReport struct {
	ThreadID string
	TurnID   string
	Usage    *TokenUsage
	// AdditionalBudgetUnits is the synchronous post-hook price settled with the
	// response. Overflowed reports that summing the individual hook prices
	// exceeded f64 range, so the value is a real lower bound.
	AdditionalBudgetUnits           float64
	AdditionalBudgetUnitsOverflowed bool
}

// UsageAccountingDecision is a host's answer to one AgentUsageReport. Every
// decision means the host recorded the reported spending.
type UsageAccountingDecision int

const (
	// UsageAccountingContinue leaves the turn and its rollout tree eligible.
	UsageAccountingContinue UsageAccountingDecision = iota
	// UsageAccountingExhausted stops new tool handlers in this local rollout
	// tree. Already-running work continues.
	UsageAccountingExhausted
	// UsageAccountingExhaustedForTurn fails the reporting response with
	// SessionBudgetExceeded after recording its output and usage. Background
	// compaction fails only its job; later turns and peer threads stay
	// eligible (Rust UsageAccountingDecision::ExhaustedForTurn, #61958).
	UsageAccountingExhaustedForTurn
)

// UsageAccounting lets an embedding host account a response's spending. The
// native rollout budget is used when the host installs no accounting.
type UsageAccounting interface {
	ReportUsage(report AgentUsageReport) (UsageAccountingDecision, error)
}
