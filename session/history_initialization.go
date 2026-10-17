package session

import "strings"

// HistoryInitialization classifies how a runtime obtained its starting history.
// It describes reconstruction provenance, not whether tool metadata was present.
type HistoryInitialization string

const (
	HistoryInitializationNew             HistoryInitialization = "new"
	HistoryInitializationCleared         HistoryInitialization = "cleared"
	HistoryInitializationColdResume      HistoryInitialization = "cold_resume"
	HistoryInitializationWarmFork        HistoryInitialization = "warm_fork"
	HistoryInitializationColdFork        HistoryInitialization = "cold_fork"
	HistoryInitializationSuppliedHistory HistoryInitialization = "supplied_history"
)

// HistoryInitializationExtraKey persists the runtime classification on the
// thread record so every request in that runtime reports the same value.
const HistoryInitializationExtraKey = "history_initialization"

// HistoryInitializationFromExtra reads a persisted classification, defaulting
// to empty when the record predates this metadata.
func HistoryInitializationFromExtra(extra map[string]any) HistoryInitialization {
	value, _ := extra[HistoryInitializationExtraKey].(string)
	value = strings.TrimSpace(value)
	switch HistoryInitialization(value) {
	case HistoryInitializationNew,
		HistoryInitializationCleared,
		HistoryInitializationColdResume,
		HistoryInitializationWarmFork,
		HistoryInitializationColdFork,
		HistoryInitializationSuppliedHistory:
		return HistoryInitialization(value)
	default:
		return ""
	}
}
