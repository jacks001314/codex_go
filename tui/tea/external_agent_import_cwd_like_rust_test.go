package tea

import (
	"testing"

	"codex_go/config"
	codextui "codex_go/tui"
)

// TestExternalAgentImportUsesRemoteCWDOverrideLikeRust pins #47317: before a
// remote thread starts, /import detects from the remote --cd override instead
// of the local composer cwd. With no override detection stays home-only.
func TestExternalAgentImportUsesRemoteCWDOverrideLikeRust(t *testing.T) {
	tests := []struct {
		name          string
		remote        bool
		override      string
		threadID      string
		stateCWD      string
		wantDetection string
	}{
		{
			name:          "remote without thread uses override",
			remote:        true,
			override:      "/remote/worktree",
			stateCWD:      `C:\local`,
			wantDetection: "/remote/worktree",
		},
		{
			name:          "remote with thread uses thread cwd",
			remote:        true,
			override:      "/remote/worktree",
			threadID:      "thread-1",
			stateCWD:      "/remote/thread",
			wantDetection: "/remote/thread",
		},
		{
			name:          "remote without override is home-only",
			remote:        true,
			stateCWD:      `C:\local`,
			wantDetection: "",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var detectedCWD string
			state := codextui.NewState(nil)
			state.CWD = testCase.stateCWD
			if testCase.threadID != "" {
				state.SetThreadID(testCase.threadID)
			}
			model := NewModel(state, Options{
				Width:             120,
				Height:            36,
				RemoteAppServer:   testCase.remote,
				RemoteCWDOverride: testCase.override,
				OnDetectExternalAgent: func(cwd string, _ string) (config.ExternalAgentConfigDetectResponse, error) {
					detectedCWD = cwd
					return config.ExternalAgentConfigDetectResponse{}, nil
				},
			})
			command := model.applyExternalAgentImportCommand()
			if command == nil {
				t.Fatal("applyExternalAgentImportCommand() returned no detection command")
			}
			command()
			if detectedCWD != testCase.wantDetection {
				t.Fatalf("detected cwd = %q, want %q", detectedCWD, testCase.wantDetection)
			}
		})
	}
}
