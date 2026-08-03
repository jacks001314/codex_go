package historycell

import (
	"strings"
	"testing"

	"codex_go/protocol"
)

func TestCollabAgentToolCallRendersRustLifecycleWithoutRawPayloads(t *testing.T) {
	receivers := []string{"/root/worker"}
	plainPrompt := "calculate the checksum"
	encrypted := "gAAAA-secret-payload"
	completed := "completed"
	states := map[string]protocol.CollabAgentState{
		"/root/worker": {Status: completed, Message: &encrypted},
	}

	tests := []struct {
		name      string
		item      protocol.ThreadItem
		completed bool
		want      string
	}{
		{
			name:      "spawn",
			item:      protocol.CollabToolCallItem("spawn", "spawn_agent", "/root", receivers, &plainPrompt, nil, "completed"),
			completed: true,
			want:      "• Spawned /root/worker\n  └ calculate the checksum",
		},
		{
			name:      "send",
			item:      protocol.CollabToolCallItem("send", "send_input", "/root", receivers, &plainPrompt, nil, "completed"),
			completed: true,
			want:      "• Sent input to /root/worker\n  └ calculate the checksum",
		},
		{
			name:      "resume",
			item:      protocol.CollabToolCallItem("resume", "resume_agent", "/root", receivers, nil, nil, "completed"),
			completed: true,
			want:      "• Resumed /root/worker",
		},
		{
			name: "wait-started",
			item: protocol.CollabToolCallItem("wait", "wait", "/root", receivers, nil, nil, "in_progress"),
			want: "• Waiting for /root/worker",
		},
		{
			name:      "wait-completed",
			item:      protocol.CollabToolCallItem("wait", "wait", "/root", receivers, nil, states, "completed"),
			completed: true,
			want:      "• Finished waiting\n  └ /root/worker: completed",
		},
		{
			name:      "wait-completed-empty",
			item:      protocol.CollabToolCallItem("wait-empty", "wait", "/root", nil, nil, nil, "completed"),
			completed: true,
			want:      "• Finished waiting\n  └ No agents completed yet",
		},
		{
			name:      "close",
			item:      protocol.CollabToolCallItem("close", "close_agent", "/root", receivers, nil, nil, "completed"),
			completed: true,
			want:      "• Closed /root/worker",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cell, ok := NewCollabAgentToolCall(&test.item, test.completed)
			if !ok {
				t.Fatal("collaboration item did not render")
			}
			got := strings.Join(cell.RawLines(), "\n")
			if got != test.want {
				t.Fatalf("rendered item = %q, want %q", got, test.want)
			}
			for _, forbidden := range []string{"collaboration.", "gAAAA", "{\""} {
				if strings.Contains(got, forbidden) {
					t.Fatalf("rendered item leaked %q: %q", forbidden, got)
				}
			}
		})
	}

	spawnWithCiphertext := protocol.CollabToolCallItem("spawn", "spawn_agent", "/root", receivers, &encrypted, nil, "completed")
	cell, ok := NewCollabAgentToolCall(&spawnWithCiphertext, true)
	if !ok || strings.Join(cell.RawLines(), "\n") != "• Spawned /root/worker" {
		t.Fatalf("ciphertext prompt was not hidden: ok=%v lines=%q", ok, cell.RawLines())
	}
}

func TestSubAgentActivityRendersCanonicalPath(t *testing.T) {
	tests := map[string]string{
		"started":     "• Started `/root/worker`",
		"interacted":  "• Interacted with `/root/worker`",
		"interrupted": "• Interrupted `/root/worker`",
	}
	for kind, want := range tests {
		cell, ok := NewSubAgentActivity(kind, "/root/worker")
		if !ok {
			t.Fatalf("activity %q did not render", kind)
		}
		if got := strings.Join(cell.RawLines(), "\n"); got != want {
			t.Fatalf("activity %q = %q, want %q", kind, got, want)
		}
	}
}
