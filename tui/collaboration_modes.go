package tui

type CollaborationModeKind string

const (
	CollaborationModeDefault CollaborationModeKind = "default"
	CollaborationModePlan    CollaborationModeKind = "plan"
)

func NormalizeCollaborationMode(value string) CollaborationModeKind {
	if value == string(CollaborationModePlan) {
		return CollaborationModePlan
	}
	return CollaborationModeDefault
}
