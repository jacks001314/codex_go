package agent

import (
	"context"
	"testing"

	"codex_go/tool"
)

type acknowledgementSendController struct {
	*MemoryToolController
	receiverPath string
}

func (c *acknowledgementSendController) SendMessage(_ context.Context, args *SendMessageArgs) error {
	args.ResolvedThreadID = "thread-receiver"
	args.ResolvedPath = c.receiverPath
	return nil
}

// TestSendMessageAcknowledgesDeliveryLikeRust pins Rust #61894: the v2
// send_message tool reports "Message accepted." and only adds the followup_task
// hint when the recipient is not the root agent.
func TestSendMessageAcknowledgesDeliveryLikeRust(t *testing.T) {
	cases := []struct {
		name         string
		receiverPath string
		want         string
	}{
		{
			name:         "root recipient",
			receiverPath: AgentPathRootValue,
			want:         "Message accepted.",
		},
		{
			name:         "child recipient",
			receiverPath: "/root/worker",
			want:         "Message accepted. If the recipient is idle and should act now, use followup_task to ask it to act on the message.",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			controller := &acknowledgementSendController{
				MemoryToolController: NewMemoryToolController(),
				receiverPath:         tt.receiverPath,
			}
			registry := tool.NewRegistry()
			if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
				Controller: controller, Version: VersionV2, Namespace: MultiAgentV2Namespace,
			}); err != nil {
				t.Fatal(err)
			}
			executor, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "send_message"))
			if !ok {
				t.Fatal("send_message was not registered")
			}
			output, err := executor.Execute(context.Background(), &tool.Invocation{
				Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"target":"worker","message":"context"}`},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !output.Success {
				t.Fatalf("Success = false, want true")
			}
			if output.Body != tt.want {
				t.Fatalf("Body = %q, want %q", output.Body, tt.want)
			}
		})
	}
}
