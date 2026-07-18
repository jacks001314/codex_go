package turn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/codexapi"
	"codex_go/eventmap"
	"codex_go/model"
	"codex_go/tool"
)

func TestBuildToolRegistryRegistersStandaloneImageGenerationTool(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.ImageGeneration = &ImageGenerationOptions{}

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName))
	if !ok {
		t.Fatal("image_gen.imagegen missing")
	}
	spec := executor.Spec()
	if spec.Exposure != tool.ExposureModelVisible || spec.NamespaceDescription != "Tools in the image_gen namespace." {
		t.Fatalf("spec = %#v", spec)
	}
	properties := spec.InputSchema["properties"].(map[string]any)
	if _, ok := properties["prompt"]; !ok {
		t.Fatalf("schema properties = %#v", properties)
	}
}

func TestImageGenerationHandlerPostsGenerationAndReturnsRustOutputShape(t *testing.T) {
	result := base64.StdEncoding.EncodeToString([]byte("fake-png"))
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/images/generations" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer image-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Provider-Test"); got != "provider" {
			t.Fatalf("provider header = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode(body) error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codexapi.ImageResponse{Data: []codexapi.ImageData{{B64JSON: result}}})
	}))
	defer server.Close()

	codexHome := t.TempDir()
	handler := NewImageGenerationHandler(&ImageGenerationOptions{
		SessionID: "thread-1",
		CodexHome: codexHome,
		Provider: model.APIProvider{
			BaseURL: server.URL + "/api/codex",
			Headers: http.Header{
				"X-Provider-Test": []string{"provider"},
			},
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
	if output == nil || !output.Success || output.Body != "[generated image]" || output.LogPreview != "[generated image]" {
		t.Fatalf("output = %#v", output)
	}
	if requestBody["model"] != imageGenerationModel || requestBody["prompt"] != "paint a red square" ||
		requestBody["background"] != "auto" || requestBody["quality"] != "auto" || requestBody["size"] != "auto" {
		t.Fatalf("request body = %#v", requestBody)
	}
	items, ok := output.Data["content_items"].([]FunctionCallOutputContentItem)
	if !ok || len(items) != 2 {
		t.Fatalf("content_items = %#v", output.Data["content_items"])
	}
	if items[0].Type != "input_image" || items[0].ImageURL != imageGenerationOutputPrefix+result ||
		items[0].Detail == nil || *items[0].Detail != imageGenerationDefaultDetail {
		t.Fatalf("image content item = %#v", items[0])
	}
	if items[1].Type != "input_text" || !strings.Contains(items[1].Text, "Generated images are saved to") {
		t.Fatalf("hint content item = %#v", items[1])
	}
	savedPath := output.Data["savedPath"].(string)
	if savedPath != eventmap.ImageGenerationArtifactPath(codexHome, "thread-1", "call-image") {
		t.Fatalf("saved path = %q", savedPath)
	}
	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(savedPath) error = %v", err)
	}
	if string(data) != "fake-png" {
		t.Fatalf("saved data = %q", string(data))
	}
}

func TestImageGenerationHandlerPostsEditForReferencedImages(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode(body) error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(codexapi.ImageResponse{Data: []codexapi.ImageData{{B64JSON: base64.StdEncoding.EncodeToString([]byte("edited"))}}})
	}))
	defer server.Close()

	imagePath := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(imagePath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(image) error = %v", err)
	}
	handler := NewImageGenerationHandler(&ImageGenerationOptions{
		Provider:   model.APIProvider{BaseURL: server.URL + "/v1"},
		HTTPClient: server.Client(),
	})
	output, err := handler.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-edit",
		ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"prompt":"add a frame","referenced_image_paths":["` + filepath.ToSlash(imagePath) + `"]}`,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	images := requestBody["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("images = %#v", images)
	}
	image := images[0].(map[string]any)
	if !strings.HasPrefix(image["image_url"].(string), "data:image/png;base64,") {
		t.Fatalf("image url = %#v", image["image_url"])
	}
}
