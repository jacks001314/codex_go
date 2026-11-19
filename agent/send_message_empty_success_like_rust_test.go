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

// TestSendMessageReturnsEmptySuccessLikeRust pins Rust #62093: a successful v2
// send_message reports success with no output text, for the root agent and for
// ordinary recipients alike (the followup_task hint is gone).
func TestSendMessageReturnsEmptySuccessLikeRust(t *testing.T) {
	for _, receiverPath := range []string{AgentPathRootValue, "/root/worker"} {
		t.Run(receiverPath, func(t *testing.T) {
			controller := &acknowledgementSendController{
				MemoryToolController: NewMemoryToolController(),
				receiverPath:         receiverPath,
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
			if output.Body != "" {
				t.Fatalf("Body = %q, want empty success output", output.Body)
			}
		})
	}
}
