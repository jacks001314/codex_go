package turn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Rust #62871: the completed image-generation item carries the backend's
// per-image metadata, while the model-facing tool output stays free of it.
func TestImageGenerationHandlerExposesImageMetadataLikeRust(t *testing.T) {
	result := base64.StdEncoding.EncodeToString([]byte("fake-png"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"` + result + `","generation_id":"gen-image-123","image_metadata":{"entries":[{"name":"first","value":1.5}],"futureKey":{"nestedKey":[false,0,null]}}}]}`))
	}))
	defer server.Close()

	output := executeImageGenerationForMetadataTest(t, server, `{"prompt":"paint a red square"}`)
	if output == nil || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	metadata, ok := output.Data["imageMetadata"].(map[string]any)
	if !ok {
		t.Fatalf("imageMetadata = %#v", output.Data["imageMetadata"])
	}
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal(imageMetadata) error = %v", err)
	}
	if got, want := string(encodedMetadata), `{"entries":[{"name":"first","value":1.5}],"futureKey":{"nestedKey":[false,0,null]}}`; got != want {
		t.Fatalf("imageMetadata = %s, want %s", got, want)
	}
	if alias, ok := output.Data["image_metadata"].(map[string]any); !ok || len(alias) != 2 {
		t.Fatalf("image_metadata = %#v", output.Data["image_metadata"])
	}
	content, err := json.Marshal(output.Data["content_items"])
	if err != nil {
		t.Fatalf("Marshal(content_items) error = %v", err)
	}
	if strings.Contains(string(content), "futureKey") {
		t.Fatalf("surface metadata leaked into the model tool output: %s", content)
	}
	if strings.Contains(output.Body, "futureKey") || strings.Contains(output.LogPreview, "futureKey") {
		t.Fatalf("surface metadata leaked into the tool body: %q / %q", output.Body, output.LogPreview)
	}
}

// Invalid or missing metadata never drops the generated image, and empty
// metadata stays off the item entirely.
func TestImageGenerationHandlerDropsInvalidImageMetadataLikeRust(t *testing.T) {
	result := base64.StdEncoding.EncodeToString([]byte("fake-png"))
	for _, metadata := range []string{`[]`, `0`, `null`, `{}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"` + result + `","image_metadata":` + metadata + `}]}`))
		}))
		output := executeImageGenerationForMetadataTest(t, server, `{"prompt":"paint a red square"}`)
		server.Close()
		if output == nil || !output.Success {
			t.Fatalf("metadata %s: output = %#v", metadata, output)
		}
		if _, ok := output.Data["imageMetadata"]; ok {
			t.Fatalf("metadata %s: imageMetadata = %#v, want absent", metadata, output.Data["imageMetadata"])
		}
		if _, ok := output.Data["image_metadata"]; ok {
			t.Fatalf("metadata %s: image_metadata = %#v, want absent", metadata, output.Data["image_metadata"])
		}
		if got := output.Data["result"]; got != result {
			t.Fatalf("metadata %s: result = %#v", metadata, got)
		}
	}
}

func executeImageGenerationForMetadataTest(t *testing.T, server *httptest.Server, arguments string) *tool.Output {
	t.Helper()
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
			Arguments: arguments,
		},
	}
	output, err := handler.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}
