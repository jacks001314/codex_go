package chatwidget

import "testing"

func TestInputFlowSubmissionDecisionMatchesRustQueueGuards(t *testing.T) {
	options := SubmissionOptions{SessionConfigured: true, CurrentModelHasImages: true}
	message := NewUserMessage("next")

	cases := []struct {
		name  string
		state InputFlowState
		want  InputFlowAction
	}{
		{
			name:  "idle submits",
			state: InputFlowState{SessionConfigured: true},
			want:  InputFlowSubmitNow,
		},
		{
			name:  "task running queues",
			state: InputFlowState{SessionConfigured: true, TaskRunning: true},
			want:  InputFlowQueue,
		},
		{
			name:  "plan streaming queues",
			state: InputFlowState{SessionConfigured: true, PlanStreamingInTUI: true},
			want:  InputFlowQueue,
		},
		{
			name:  "suppressed autosend queues",
			state: InputFlowState{SessionConfigured: true, Queue: InputQueueState{SuppressQueueAutosend: true}},
			want:  InputFlowQueue,
		},
		{
			name:  "only user shell commands running queues regular text",
			state: InputFlowState{SessionConfigured: true, OnlyUserShellCommandsRunning: true},
			want:  InputFlowQueue,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.Decide(message, options); got != tc.want {
				t.Fatalf("Decide = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInputFlowAllowsShellEscapeDuringUserShellCommandsMatchRust(t *testing.T) {
	state := InputFlowState{SessionConfigured: true, OnlyUserShellCommandsRunning: true}
	options := SubmissionOptions{SessionConfigured: true, CurrentModelHasImages: true}
	if got := state.Decide(NewUserMessage("!pwd"), options); got != InputFlowSubmitNow {
		t.Fatalf("shell escape Decide = %q, want %q", got, InputFlowSubmitNow)
	}
}
