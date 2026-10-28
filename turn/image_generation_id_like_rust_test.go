package turn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/tool"
)

func TestImageGenerationHandlerExposesGenerationIDLikeRust(t *testing.T) {
	result := base64.StdEncoding.EncodeToString([]byte("fake-png"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codexapi.ImageResponse{Data: []codexapi.ImageData{{
			B64JSON:      result,
			GenerationID: stringPtrForTest("gen-image-123"),
		}}})
	}))
	defer server.Close()

	handler := NewImageGenerationHandler(&ImageGenerationOptions{
		SessionID: "thread-1",
		Provider: model.APIProvider{
			BaseURL: server.URL + "/api/codex",
		},
		Auth:       model.BearerAuthHeaders("image-token", "", false),
		HTTPClient: server.Client(),
	})
	invocation := &tool.Invocation{
		CallID:   "call-image",
		ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"prompt":"paint a red square"}`,
		},
	}
	output, err := handler.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	if got := output.Data["generationId"]; got != "gen-image-123" {
		t.Fatalf("generationId = %#v, want gen-image-123", got)
	}
	if got := output.Data["generation_id"]; got != "gen-image-123" {
		t.Fatalf("generation_id = %#v, want gen-image-123", got)
	}
}

func TestImageGenerationHandlerOmitsMissingGenerationIDLikeRust(t *testing.T) {
	result := base64.StdEncoding.EncodeToString([]byte("fake-png"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codexapi.ImageResponse{Data: []codexapi.ImageData{{B64JSON: result}}})
	}))
	defer server.Close()

	handler := NewImageGenerationHandler(&ImageGenerationOptions{
		SessionID: "thread-1",
		Provider: model.APIProvider{
			BaseURL: server.URL + "/api/codex",
		},
		Auth:       model.BearerAuthHeaders("image-token", "", false),
		HTTPClient: server.Client(),
	})
	invocation := &tool.Invocation{
		CallID:   "call-image",
		ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"prompt":"paint a red square"}`,
		},
	}
	output, err := handler.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, ok := output.Data["generationId"]; ok {
		t.Fatalf("generationId present without a backend id: %#v", output.Data["generationId"])
	}
}

func stringPtrForTest(value string) *string {
	return &value
}
