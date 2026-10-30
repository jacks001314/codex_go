package turn

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/codemode"
	"codex_go/codexapi"
	"codex_go/eventmap"
	"codex_go/model"
	"codex_go/tool"
)

func TestImageGenerationUsageLimitFailureDetectionLikeRust(t *testing.T) {
	failure := imageGenerationUsageLimitFailure([]byte(`{"error":{"message":"usage limit","limit_id":"image_gen","resets_at":1786000000}}`), nil)
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
	if got := imageGenerationUsageLimitFailure([]byte(`{"error":{"limit_id":"other"}}`), nil); got != nil {
		t.Fatalf("other limit failure = %#v, want nil", got)
	}
	if got := imageGenerationUsageLimitFailure([]byte(`not json`), nil); got != nil {
		t.Fatalf("malformed failure = %#v, want nil", got)
	}
}

func TestImageGenerationDeclaresCodeModeResultWithoutChangingDirectAPILikeRust(t *testing.T) {
	spec := NewImageGenerationHandler(nil).Spec()
	augmented := codemode.AugmentToolSpec(spec)
	if want := "): Promise<{ image_url: string; output_hint?: string; }>;"; !strings.Contains(augmented.Description, want) {
		t.Fatalf("code-mode declaration = %q, want return type %q", augmented.Description, want)
	}
	payload, err := json.Marshal(model.ResponsesToolsFromSpecs([]tool.Spec{spec}))
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	if strings.Contains(string(payload), "output_schema") {
		t.Fatalf("direct API payload gained an output schema: %s", payload)
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
		requestBody["background"] != "opaque" || requestBody["quality"] != "auto" || requestBody["size"] != "auto" {
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
		t.Fatalf("transparentBackground = %#v, want absent when the response omits background", output.Data["transparentBackground"])
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

// TestImageGenerationBackgroundArgumentMatchesRust pins Rust #47484: the
// boolean `transparent_background` argument replaces the previous `auto`
// background, so generation and editing always send an explicit background.
func TestImageGenerationBackgroundArgumentMatchesRust(t *testing.T) {
	handler := NewImageGenerationHandler(&ImageGenerationOptions{
		InputItems: []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAA"}}}},
	})
	oneImage := 1
	for _, tc := range []struct {
		name      string
		args      imageGenerationArgs
		want      codexapi.ImageBackground
		wantEdit  bool
		expectErr bool
	}{
		{name: "generate omitted", args: imageGenerationArgs{Prompt: "draw"}, want: codexapi.ImageBackgroundOpaque},
		{name: "generate transparent", args: imageGenerationArgs{Prompt: "draw", TransparentBackground: true}, want: codexapi.ImageBackgroundTransparent},
		{name: "edit omitted", args: imageGenerationArgs{Prompt: "draw", NumLastImagesToInclude: &oneImage}, want: codexapi.ImageBackgroundOpaque, wantEdit: true},
		{name: "edit transparent", args: imageGenerationArgs{Prompt: "draw", NumLastImagesToInclude: &oneImage, TransparentBackground: true}, want: codexapi.ImageBackgroundTransparent, wantEdit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, _, err := handler.requestForArgs(context.Background(), &tc.args)
			if tc.expectErr {
				if err == nil {
					t.Fatal("requestForArgs() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("requestForArgs() error = %v", err)
			}
			var background *codexapi.ImageBackground
			switch request.kind {
			case imageGenerationRequestGenerate:
				background = request.generate.Background
			case imageGenerationRequestEdit:
				background = request.edit.Background
			}
			if request.kind == imageGenerationRequestEdit != tc.wantEdit {
				t.Fatalf("request kind = %q, want edit=%v", request.kind, tc.wantEdit)
			}
			if background == nil || *background != tc.want {
				t.Fatalf("background = %v, want %q", background, tc.want)
			}
		})
	}

	// The advertised schema carries the new boolean property.
	schema := imageGenerationSchema()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", schema["properties"])
	}
	transparent, ok := properties["transparent_background"].(map[string]any)
	if !ok || transparent["type"] != "boolean" {
		t.Fatalf("schema transparent_background = %#v, want a boolean property", properties["transparent_background"])
	}
}

// TestRecentImageReferencesPassesFileBackedWindowsLikeRust mirrors Rust #47956:
// the window still counts file-backed images (so numbering matches), a short
// window reports how many were available, and file references pass through to
// the edit request in selection order.
func TestRecentImageReferencesPassesFileBackedWindowsLikeRust(t *testing.T) {
	inline := func(url string) map[string]any {
		return map[string]any{"type": "message", "content": []any{map[string]any{"type": "input_image", "image_url": url}}}
	}
	fileBacked := map[string]any{"type": "message", "content": []any{map[string]any{"type": "input_image", "file_id": "file_1"}}}

	// All-inline windows come back in chronological order.
	images, err := recentImageReferences([]any{inline("a"), inline("b")}, 2)
	if err != nil {
		t.Fatalf("recentImageReferences() error = %v", err)
	}
	if len(images) != 2 || images[0].ImageURL != "a" || images[1].ImageURL != "b" {
		t.Fatalf("recentImageReferences() = %#v", images)
	}

	// A file-backed image inside the window is preserved after the inline image.
	images, err = recentImageReferences([]any{inline("a"), fileBacked}, 2)
	if err != nil {
		t.Fatalf("recentImageReferences(file-backed) error = %v", err)
	}
	if len(images) != 2 || images[0].ImageURL != "a" || images[0].FileID != "" ||
		images[1].ImageURL != "" || images[1].FileID != "file_1" {
		t.Fatalf("recentImageReferences(file-backed) = %#v", images)
	}

	// A window shorter than requested reports the available count.
	_, err = recentImageReferences([]any{inline("a")}, 3)
	if err == nil || err.Error() != "requested the last 3 conversation images, but only 1 were available" {
		t.Fatalf("recentImageReferences(short) error = %v", err)
	}

	// The camelCase spelling of the file reference is recognized too, and tool
	// output content items count the same way.
	images, err = recentImageReferences([]any{inline("a"), map[string]any{"type": "message", "content": []any{map[string]any{"type": "input_image", "fileId": "file_2"}}}}, 2)
	if err != nil {
		t.Fatalf("recentImageReferences(camelCase file reference) error = %v", err)
	}
	if len(images) != 2 || images[1].FileID != "file_2" {
		t.Fatalf("recentImageReferences(camelCase file reference) = %#v", images)
	}
	images, err = recentImageReferences([]any{
		inline("a"),
		map[string]any{"type": "function_call_output", "output": []any{map[string]any{"type": "input_image", "file_id": "file_3"}}},
	}, 2)
	if err != nil {
		t.Fatalf("recentImageReferences(file tool output) error = %v", err)
	}
	if len(images) != 2 || images[1].FileID != "file_3" {
		t.Fatalf("recentImageReferences(file tool output) = %#v", images)
	}
}

// TestImageGenerationEditPassesFileBackedRecentWindow drives the same rule
// through the tool arguments (Rust #47956).
func TestImageGenerationEditPassesFileBackedRecentWindow(t *testing.T) {
	handler := NewImageGenerationHandler(&ImageGenerationOptions{
		InputItems: []any{
			map[string]any{"type": "message", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAA"}}},
			map[string]any{"type": "message", "content": []any{map[string]any{"type": "input_image", "file_id": "file_recent"}}},
		},
	})
	count := 2
	request, _, err := handler.requestForArgs(context.Background(), &imageGenerationArgs{
		Prompt:                 "add a frame",
		NumLastImagesToInclude: &count,
	})
	if err != nil {
		t.Fatalf("requestForArgs() error = %v", err)
	}
	if request.kind != imageGenerationRequestEdit {
		t.Fatalf("request kind = %q, want edit", request.kind)
	}
	if len(request.edit.Images) != 2 ||
		request.edit.Images[0].ImageURL != "data:image/png;base64,AAA" ||
		request.edit.Images[0].FileID != "" ||
		request.edit.Images[1].ImageURL != "" ||
		request.edit.Images[1].FileID != "file_recent" {
		t.Fatalf("edit images = %#v", request.edit.Images)
	}
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
		"- Avoid printing the full result or its base64 image data with `text()` or `notify()`; print only small metadata when needed.\n" +
		"- Set `transparent_background` to true when the request calls for a transparent background, including background removal or a cutout; set it to false otherwise. For edits, preserve existing transparency unless the user asks to change it.\n" +
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

// Mirrors Rust #54420 (ext/image-generation/src/tool.rs PreparedImageRequest):
// a completed generation records the local input images it read, in argument
// order, identified by the lowercase SHA-256 of the file bytes read before image
// normalization. The field is absent when no local input was read, and changed
// contents at the same path produce a different identity.
func TestImageGenerationRecordsInputImageIdentityLikeRust(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.png")
	second := filepath.Join(dir, "second.png")
	firstBytes := []byte("\x89PNG\r\n\x1a\nfirst-reference")
	secondBytes := []byte("\x89PNG\r\n\x1a\nsecond-reference")
	if err := os.WriteFile(first, firstBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(first) error = %v", err)
	}
	if err := os.WriteFile(second, secondBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(second) error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(codexapi.ImageResponse{Data: []codexapi.ImageData{{B64JSON: base64.StdEncoding.EncodeToString([]byte("edited"))}}})
	}))
	defer server.Close()
	handler := NewImageGenerationHandler(&ImageGenerationOptions{
		Provider:   model.APIProvider{BaseURL: server.URL + "/v1"},
		HTTPClient: server.Client(),
	})
	output, err := handler.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-input-images",
		ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"prompt":"add a frame","referenced_image_paths":["` + filepath.ToSlash(first) + `","` + filepath.ToSlash(second) + `"]}`,
		},
		Context: map[string]any{"turnId": "turn-input-images"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	inputImages, ok := output.Data["inputImages"].([]imageGenerationInputImage)
	if !ok || len(inputImages) != 2 {
		t.Fatalf("inputImages = %#v", output.Data["inputImages"])
	}
	// The recorded path is exactly what the request read, so it keeps the
	// slash-normalized form the arguments used.
	if inputImages[0].Path != filepath.ToSlash(first) || inputImages[1].Path != filepath.ToSlash(second) {
		t.Fatalf("input images = %#v, want the argument order", inputImages)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(firstBytes)); inputImages[0].SHA256 != want {
		t.Fatalf("first sha256 = %q, want %q", inputImages[0].SHA256, want)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(secondBytes)); inputImages[1].SHA256 != want {
		t.Fatalf("second sha256 = %q, want %q", inputImages[1].SHA256, want)
	}
	if _, ok := output.Data["input_images"]; !ok {
		t.Fatalf("snake-case alias missing: %#v", output.Data)
	}
	// Changed contents at the same path are a different identity.
	if err := os.WriteFile(first, []byte("different bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(first) error = %v", err)
	}
	_, changed, err := handler.requestForArgs(context.Background(), &imageGenerationArgs{Prompt: "add a frame", ReferencedImagePaths: []string{first}})
	if err != nil {
		t.Fatalf("requestForArgs() error = %v", err)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte("different bytes"))); len(changed) != 1 || changed[0].SHA256 != want {
		t.Fatalf("changed identity = %#v, want %q", changed, want)
	}
	// A generation without referenced images records no local input.
	_, none, err := handler.requestForArgs(context.Background(), &imageGenerationArgs{Prompt: "draw"})
	if err != nil {
		t.Fatalf("requestForArgs() error = %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("generation input images = %#v, want none", none)
	}
}

// Rust #57275: the premium imagegen limit is recognized, and an exhausted
// window's reset time fills in when the body carries none.
func TestImageGenerationPremiumUsageLimitFailureLikeRust(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-codex-active-limit", "imagegen_premium")
	headers.Set("x-imagegen-premium-primary-used-percent", "100.0")
	headers.Set("x-imagegen-premium-primary-window-minutes", "1440")
	headers.Set("x-imagegen-premium-primary-reset-at", "1786154400")
	failure := imageGenerationUsageLimitFailure([]byte(`{"error":{"type":"usage_limit_reached","message":"image limit reached"}}`), headers)
	if failure == nil {
		t.Fatal("premium failure = nil, want usageLimitExceeded")
	}
	if failure["type"] != "usageLimitExceeded" || failure["limitId"] != "imagegen_premium" {
		t.Fatalf("premium failure = %#v", failure)
	}
	resetsAt, ok := failure["resetsAt"].(*int64)
	if !ok || *resetsAt != 1786154400 {
		t.Fatalf("resetsAt = %#v, want the exhausted window reset", failure["resetsAt"])
	}
	// A body reset time wins over the header fallback.
	withBody := imageGenerationUsageLimitFailure([]byte(`{"error":{"limit_id":"image_gen","resets_at":1786150800}}`), headers)
	if withBody == nil {
		t.Fatal("body reset failure = nil")
	}
	if got, ok := withBody["resetsAt"].(*int64); !ok || *got != 1786150800 {
		t.Fatalf("body resetsAt = %#v, want the body value", withBody["resetsAt"])
	}
	// Neither a header limit id nor an exhausted window is a failure.
	unrelated := http.Header{}
	unrelated.Set("x-codex-active-limit", "premium")
	if got := imageGenerationUsageLimitFailure([]byte(`{"error":{}}`), unrelated); got != nil {
		t.Fatalf("unrelated limit = %#v, want nil", got)
	}
	noReset := http.Header{}
	noReset.Set("x-codex-active-limit", "imagegen_premium")
	if got := imageGenerationUsageLimitFailure([]byte(`{"error":{}}`), noReset); got == nil || got["resetsAt"] != (*int64)(nil) {
		t.Fatalf("missing reset = %#v, want the limit with no reset time", got)
	}
}
