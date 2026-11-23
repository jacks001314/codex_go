package cloudbackend

import (
	"encoding/json"
	"reflect"
	"testing"

	"codex_go/cloudclient"
)

// TestCloudMentionContentPreservesTypedPartLikeRust pins Rust #63237 on the
// app-server side: a decoded cloud mention becomes a typed mention content
// part with its name and app/plugin URI, in the announced order.
func TestCloudMentionContentPreservesTypedPartLikeRust(t *testing.T) {
	user := cloudclient.UserMessageSummary{
		ID: "item-1",
		Content: []cloudclient.UserMessageInput{
			{Kind: cloudclient.UserMessageMention, Mention: cloudclient.MentionReference{Name: "Example App", Path: "app://connector_example"}},
			{Kind: cloudclient.UserMessageText, Text: "hello"},
		},
	}
	encoded, err := json.Marshal(cloudUserContent(user))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got, want []map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := json.Unmarshal([]byte(`[{"type":"mention","name":"Example App","path":"app://connector_example"},{"type":"text","text":"hello","text_elements":[]}]`), &want); err != nil {
		t.Fatalf("Unmarshal(want) error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("content = %#v, want %#v", got, want)
	}
}
