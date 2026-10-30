package tea

import (
	"strings"
	"testing"
)

func TestSidePromptsForbidManagedWorktreeCreationLikeRust(t *testing.T) {
	const instruction = "This thread is ephemeral and cannot retain worktree attachments. Do not call create_worktree here. Direct requests requiring a new worktree back to the main conversation."
	for name, prompt := range map[string]string{
		"boundary":              SideBoundaryPrompt,
		"developer instruction": SideDeveloperInstructionText,
	} {
		if !strings.Contains(prompt, instruction) {
			t.Fatalf("%s prompt is missing the managed-worktree instruction", name)
		}
	}
}
