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

func TestImageGenerationUsageLimitFailureDetectionLikeRust(t *testing.T) {
	failure := imageGenerationUsageLimitFailure([]byte(`{"error":{"message":"usage limit","limit_id":"image_gen","resets_at":1786000000}}`))
	if failure == nil {
		t.Fatal("failure = nil, want usageLimitExceeded")
	}
	if failure["type"] != "usageLimitExceeded" || failure["limitId"] != "image_gen" {
		t.Fatalf("failure = %#v", failure)
	}
	resetsAt, ok := failure["resetsAt"].(*int64)
	if !ok || resetsAt == nil || *resetsAt != 1786000000 {
		t.Fatalf("resetsAt = %#v", failure["resetsAt"])
	}
	// Non-image limits and malformed bodies are not treated as failures.
	if got := imageGenerationUsageLimitFailure([]byte(`{"error":{"limit_id":"other"}}`)); got != nil {
		t.Fatalf("other limit failure = %#v, want nil", got)
	}
	if got := imageGenerationUsageLimitFailure([]byte(`not json`)); got != nil {
		t.Fatalf("malformed failure = %#v, want nil", got)
	}
}

func TestImageGenerationErrorOutputCarriesUsageLimitFailure(t *testing.T) {
	invocation := &tool.Invocation{ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName)}
	output := imageGenerationErrorOutput(invocation, "draw x", "image generation failed: usage limit",
		map[string]any{"type": "usageLimitExceeded", "limitId": "image_gen", "resetsAt": int64(1786000000)})
	if output.Success {
		t.Fatal("output should be a failure")
	}
	failure, ok := output.Data["failure"].(map[string]any)
	if !ok || failure["type"] != "usageLimitExceeded" || failure["limitId"] != "image_gen" {
		t.Fatalf("failure = %#v", output.Data["failure"])
	}
	// Without a failure the data has no failure key.
	plain := imageGenerationErrorOutput(invocation, "draw x", "boom")
	if _, ok := plain.Data["failure"]; ok {
		t.Fatalf("unexpected failure in plain error output: %#v", plain.Data)
	}
}

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
		if got := r.Header.Get(imageTurnIDHeader); got != "turn-image-generate" {
			t.Fatalf("image turn header = %q", got)
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
		Context: map[string]any{"turn_id": "turn-image-generate"},
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
	if _, ok := output.Data["transparentBackground"]; ok {
		t.Fatalf("transparentBackground = %#v, want absent when background is auto", output.Data["transparentBackground"])
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
		if got := r.Header.Get(imageTurnIDHeader); got != "turn-image-edit" {
			t.Fatalf("image turn header = %q", got)
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
		Context: map[string]any{"turnId": "turn-image-edit"},
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

func TestImageGenerationHandlerPreservesTransparencyMetadata(t *testing.T) {
	cases := []struct {
		name       string
		background *codexapi.ImageBackground
		want       any // nil means the field must be absent
	}{
		{name: "transparent", background: ptrTo(codexapi.ImageBackgroundTransparent), want: true},
		{name: "opaque", background: ptrTo(codexapi.ImageBackgroundOpaque), want: false},
		{name: "auto", background: ptrTo(codexapi.ImageBackgroundAuto), want: nil},
		{name: "absent", background: nil, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := codexapi.ImageResponse{Data: []codexapi.ImageData{{B64JSON: base64.StdEncoding.EncodeToString([]byte("png"))}}}
				if tc.background != nil {
					response.Background = tc.background
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			handler := NewImageGenerationHandler(&ImageGenerationOptions{
				Provider:   model.APIProvider{BaseURL: server.URL + "/v1"},
				HTTPClient: server.Client(),
			})
			output, err := handler.Execute(context.Background(), &tool.Invocation{
				CallID:   "call-transparency",
				ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
				Payload: tool.Payload{
					Kind:      tool.PayloadFunction,
					Arguments: `{"prompt":"a logo"}`,
				},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if output == nil || !output.Success {
				t.Fatalf("output = %#v", output)
			}
			got, present := output.Data["transparentBackground"]
			if tc.want == nil {
				if present {
					t.Fatalf("transparentBackground = %#v, want absent", got)
				}
				return
			}
			if !present || got != tc.want {
				t.Fatalf("transparentBackground = %#v (present=%v), want %#v", got, present, tc.want)
			}
			if gotBacking, ok := output.Data["transparent_background"]; !ok || gotBacking != tc.want {
				t.Fatalf("transparent_background = %#v, want %#v", gotBacking, tc.want)
			}
		})
	}
}

func ptrTo[T any](value T) *T {
	return &value
}

func TestImageGenerationDescriptionMatchesRustBlob(t *testing.T) {
	// Mirrors Rust ext/image-generation/imagegen_description.md (include_str!
	// in ext/image-generation/src/tool.rs): the model-visible description must
	// match byte-for-byte. The golden is the exact Rust blob text so drift is
	// caught even when the Rust checkout is unavailable.
	want := "The `image_gen.imagegen` tool enables image generation from descriptions and editing of existing images based on specific instructions. Use it when:\n" +
		"\n" +
		"- The user requests an image based on a scene description, such as a diagram, portrait, comic, meme, or any other visual.\n" +
		"- The user wants to modify an attached or previously generated image with specific changes, including adding or removing elements, altering colors, improving quality/resolution, or transforming the style (e.g., cartoon, oil painting).\n" +
		"\n" +
		"Guidelines:\n" +
		"- imagegen needs a few minutes to finish. In code-mode, use the first-line @exec directive to give the initial call 120 seconds and the same yield for any waits that follow. Once it finishes, return the image with generatedImage(result).\n" +
		"- Omit both `referenced_image_paths` and `num_last_images_to_include` when generating a brand new image.\n" +
		"- For edits, use `referenced_image_paths` when every target image has a local file path.\n" +
		"- If you have not seen a local image yet, use `view_image` to inspect it before editing.\n" +
		"- Use `num_last_images_to_include` only when at least one target image has no local file path.\n" +
		"- Set `num_last_images_to_include` to the smallest number of recent conversation images that includes every target image, up to 5.\n" +
		"- Never provide both `referenced_image_paths` and `num_last_images_to_include`.\n" +
		"- If neither mechanism can include every target image, ask the user to attach the missing images again.\n" +
		"- Directly generate the image without reconfirmation or clarification unless required images must be attached again.\n" +
		"- Always use this tool for image editing unless the user explicitly requests otherwise. Do not use the `python` tool for image editing unless specifically instructed.\n"
	if imageGenerationDescription != want {
		t.Fatalf("imageGenerationDescription differs from Rust imagegen_description.md:\n--- go ---\n%s\n--- want ---\n%s", imageGenerationDescription, want)
	}
}
