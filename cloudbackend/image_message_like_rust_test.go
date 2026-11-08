package cloudbackend

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/cloudclient"
	"codex_go/turn"
)

// TestCloudImageMessageForwardsTypedPartsLikeRust pins that a Cloud turn start
// carrying an image maps onto one typed image part instead of being rejected as
// plain text only, and that text and image parts keep their order.
func TestCloudImageMessageForwardsTypedPartsLikeRust(t *testing.T) {
	detail := "high"
	request, err := cloudMessage(turn.TurnStartParams{
		ThreadID:            "thread",
		ClientUserMessageID: "client",
		Input: []turn.TurnUserInput{
			{Type: "text", Text: "look at this"},
			{Type: "image", URL: "data:image/png;base64,AAA", Detail: &detail},
		},
	})
	if err != nil {
		t.Fatalf("cloudMessage() error = %v", err)
	}
	if len(request.Input) != 2 {
		t.Fatalf("input parts = %d, want the text and image parts", len(request.Input))
	}
	if request.Input[0].Kind != cloudclient.UserMessageText || request.Input[0].Text != "look at this" {
		t.Fatalf("first part = %#v, want the text part", request.Input[0])
	}
	image := request.Input[1]
	if image.Kind != cloudclient.UserMessageImage || image.Image.URL != "data:image/png;base64,AAA" {
		t.Fatalf("image part = %#v, want the data URL reference", image)
	}
	if image.Detail == nil || *image.Detail != cloudclient.ImageDetailHigh {
		t.Fatalf("image detail = %#v, want high", image.Detail)
	}
}

// TestCloudImageMessageForwardsUploadedFileLikeRust pins the fileId alternative.
func TestCloudImageMessageForwardsUploadedFileLikeRust(t *testing.T) {
	request, err := cloudMessage(turn.TurnStartParams{
		ThreadID:            "thread",
		ClientUserMessageID: "client",
		Input:               []turn.TurnUserInput{{Type: "image", FileID: "file-1"}},
	})
	if err != nil {
		t.Fatalf("cloudMessage() error = %v", err)
	}
	if len(request.Input) != 1 || request.Input[0].Kind != cloudclient.UserMessageImage ||
		request.Input[0].Image.FileID != "file-1" || request.Input[0].Detail != nil {
		t.Fatalf("uploaded image part = %#v, want the file reference without a detail", request.Input)
	}
}

// TestCloudMessageRejectsInputsWithoutNativePartsLikeRust pins that local
// images, audio and blank text stay rejected instead of being coerced to text.
func TestCloudMessageRejectsInputsWithoutNativePartsLikeRust(t *testing.T) {
	for name, input := range map[string][]turn.TurnUserInput{
		"local image": {{Type: "localImage", Path: "/tmp/a.png"}},
		"audio":       {{Type: "audio", URL: "data:audio/wav;base64,AAA"}},
		"blank text":  {{Type: "text", Text: "   "}},
		"empty":       {},
	} {
		if _, err := cloudMessage(turn.TurnStartParams{ThreadID: "thread", ClientUserMessageID: "client", Input: input}); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// TestCloudUserMessageRendersImageContentItemLikeRust pins the announced JSON:
// the image part keeps its {"type":"image"} shape with url/fileId and detail,
// and its bytes never appear inside a text item.
func TestCloudUserMessageRendersImageContentItemLikeRust(t *testing.T) {
	detail := cloudclient.ImageDetailHigh
	payload, ok := itemSummaryPayload(cloudclient.ItemSummary{
		Kind: cloudclient.ItemUserMessage,
		User: cloudclient.UserMessageSummary{
			ID: "item-1",
			Content: []cloudclient.UserMessageInput{
				{Kind: cloudclient.UserMessageText, Text: "look"},
				{Kind: cloudclient.UserMessageImage, Image: cloudclient.ImageReference{URL: "data:image/png;base64,AAA"}, Detail: &detail},
				{Kind: cloudclient.UserMessageImage, Image: cloudclient.ImageReference{FileID: "file-1"}},
			},
		},
	})
	if !ok {
		t.Fatal("itemSummaryPayload dropped the user message")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rendered := string(encoded)
	for _, want := range []string{`"type":"image"`, `"url":"data:image/png;base64,AAA"`, `"detail":"high"`, `"fileId":"file-1"`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("payload = %s, want %s", rendered, want)
		}
	}
	if strings.Contains(rendered, `"text":"data:image`) {
		t.Fatalf("payload = %s, want the image bytes kept out of the text items", rendered)
	}
}
