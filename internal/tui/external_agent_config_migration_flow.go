package tui

type ExternalAgentConfigMigrationStep struct {
	Name   string
	Status ExternalAgentConfigMigrationStatus
}

func MigrationFlowComplete(steps []ExternalAgentConfigMigrationStep) bool {
	for _, step := range steps {
		if step.Status != ExternalAgentMigrationComplete {
			return false
		}
	}
	return len(steps) > 0
}
