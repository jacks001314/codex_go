package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex_go/agent"
)

type permissionTestBoard struct {
	*InMemoryBoard
	page *ChannelPage
}

func (b *permissionTestBoard) ListChannels(context.Context, string, ChannelQuery) (*ChannelPage, error) {
	return b.page, nil
}

func (b *permissionTestBoard) Post(context.Context, string, PostRequest) (*PostMetadata, error) {
	return nil, permissionDenied("This board connection is read-only.", "channel.post", "board:board-1")
}

// TestChannelDiscoveryExposesEffectivePermissionsLikeRust covers #52568: board
// and channel permissions are passed through without local ACL evaluation and
// the legacy fields remain at the top level.
func TestChannelDiscoveryExposesEffectivePermissionsLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	board := &permissionTestBoard{
		InMemoryBoard: (&InMemoryMessageBoards{}).Open(root, host),
		page: &ChannelPage{
			Page: Page[ChannelSummary]{Results: []ChannelSummary{{
				Permissions: &ChannelPermissions{Read: true, Reply: true},
				ChannelName: "support",
			}}},
			BoardPermissions: &BoardPermissions{ManagePermissions: true},
		},
	}
	defer board.Close()
	executor := boardToolSet(t, board, root, agent.AgentPathRoot)["get_channels"]
	body, err := boardToolBody(t, executor, boardInvocation(t, "get_channels", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	results, ok := body["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %#v", body["results"])
	}
	channel, _ := results[0].(map[string]any)
	permissions, _ := channel["permissions"].(map[string]any)
	if permissions["read"] != true || permissions["reply"] != true || permissions["post"] != false {
		t.Fatalf("channel permissions = %#v", channel["permissions"])
	}
	boardPermissions, _ := body["board_permissions"].(map[string]any)
	if boardPermissions["manage_permissions"] != true || boardPermissions["create_channel"] != false {
		t.Fatalf("board permissions = %#v", body["board_permissions"])
	}
	if channel["channel_name"] != "support" {
		t.Fatalf("legacy channel fields = %#v", channel)
	}
}

func TestPermissionDeniedBecomesFailedStructuredToolOutputLikeRust(t *testing.T) {
	host, root, _, _ := testTree(t)
	board := &permissionTestBoard{
		InMemoryBoard: (&InMemoryMessageBoards{}).Open(root, host),
		page:          &ChannelPage{},
	}
	defer board.Close()
	executor := boardToolSet(t, board, root, agent.AgentPathRoot)["post"]
	output, err := executor.Execute(context.Background(), boardInvocation(t, "post", map[string]any{
		"text": "hello", "channel_name": "support",
	}))
	if err != nil {
		t.Fatalf("permission denial returned an executor error: %v", err)
	}
	if output == nil || output.Success {
		t.Fatalf("denied output = %#v, want Success=false", output)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(output.Body), &body); err != nil {
		t.Fatal(err)
	}
	failure, _ := body["error"].(map[string]any)
	if failure["code"] != "permission_denied" || failure["action"] != "channel.post" || failure["resource"] != "board:board-1" {
		t.Fatalf("structured denial = %#v", body)
	}
}

func TestPermissionDeniedModelResponseStaysBoundedLikeRust(t *testing.T) {
	action := strings.Repeat("a", 2_000)
	resource := strings.Repeat("r", 2_000)
	denied := &PermissionDenied{Message: strings.Repeat("m", 2_000), Action: &action, Resource: &resource}
	payload, err := denied.ModelResponse(400)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 400 {
		t.Fatalf("encoded denial = %d bytes, want <= 400", len(encoded))
	}
	failure, _ := payload["error"].(map[string]any)
	if failure["truncated"] != true || failure["code"] != "permission_denied" {
		t.Fatalf("bounded denial = %#v", payload)
	}
	var typed *PermissionDenied
	if !errors.As(error(denied), &typed) {
		t.Fatal("PermissionDenied does not satisfy errors.As")
	}
}
