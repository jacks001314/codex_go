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
	"codex_go/model"
	"codex_go/tool"
)

// The animated fixture mirrors the Rust #59073 test: two one-pixel frames.
const imageGenerationAnimatedFixture = "R0lGODlhAQABAIEAAP8AAAAAAAAAAAAAACH/C05FVFNDQVBFMi4wAwEAAAAh+QQAFAAAACwAAAAAAQABAAAIBAABBAQAIfkEARQAAQAsAAAAAAEAAQCBAAD/AAAAAAAAAAAACAQAAQQEADs="

func imageGenerationGIFTestHandler(t *testing.T, gifEnabled bool, body *map[string]any) (*ImageGenerationHandler, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body != nil {
			if err := json.NewDecoder(r.Body).Decode(body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codexapi.ImageResponse{Data: []codexapi.ImageData{{B64JSON: imageGenerationAnimatedFixture}}})
	}))
	handler := NewImageGenerationHandler(&ImageGenerationOptions{
		SessionID:  "thread-gif",
		CodexHome:  t.TempDir(),
		Provider:   model.APIProvider{BaseURL: server.URL + "/api/codex"},
		Auth:       model.BearerAuthHeaders("image-token", "", false),
		HTTPClient: server.Client(),
		GIFEnabled: gifEnabled,
	})
	return handler, server
}

// Rust #59073 sends the GIF controls as sibling fields and keeps the animated
// bytes in a `.gif` artifact with an image/gif data URL.
func TestImageGenerationGIFOutputLikeRust(t *testing.T) {
	var requestBody map[string]any
	handler, server := imageGenerationGIFTestHandler(t, true, &requestBody)
	defer server.Close()
	output, err := handler.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-gif",
		ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
		Payload: tool.Payload{
			Kind:      tool.PayloadFunction,
			Arguments: `{"prompt":"paint a blinking light","gif":{"frame_count":2,"frame_duration_ms":200}}`,
		},
	})
	if err != nil || output == nil || !output.Success {
		t.Fatalf("output = %#v, err = %v", output, err)
	}
	if requestBody["output_format"] != "gif" || requestBody["frame_count"] != float64(2) || requestBody["frame_duration_ms"] != float64(200) {
		t.Fatalf("request body = %#v", requestBody)
	}
	items := output.Data["content_items"].([]FunctionCallOutputContentItem)
	if items[0].ImageURL != imageGenerationGIFOutputPrefix+imageGenerationAnimatedFixture {
		t.Fatalf("image url = %q", items[0].ImageURL)
	}
	if output.Data["image_url"] != imageGenerationGIFOutputPrefix+imageGenerationAnimatedFixture {
		t.Fatalf("image_url = %v", output.Data["image_url"])
	}
	savedPath, _ := output.Data["savedPath"].(string)
	if filepath.Ext(savedPath) != ".gif" {
		t.Fatalf("saved path = %q, want a .gif artifact", savedPath)
	}
	saved, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("read saved gif: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(imageGenerationAnimatedFixture)
	if err != nil || string(saved) != string(decoded) {
		t.Fatalf("saved bytes do not match the animation: %v", err)
	}
}

// Rust #59073 accepts `gif: {}` for API defaults and refuses GIF requests when
// the animation feature is disabled.
func TestImageGenerationGIFDefaultsAndGatingLikeRust(t *testing.T) {
	var requestBody map[string]any
	handler, server := imageGenerationGIFTestHandler(t, true, &requestBody)
	output, err := handler.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-gif-defaults",
		ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"prompt":"paint a loop","gif":{}}`},
	})
	server.Close()
	if err != nil || output == nil || !output.Success {
		t.Fatalf("output = %#v, err = %v", output, err)
	}
	if requestBody["output_format"] != "gif" {
		t.Fatalf("request body = %#v", requestBody)
	}
	if _, ok := requestBody["frame_count"]; ok {
		t.Fatalf("defaults must omit frame_count: %#v", requestBody)
	}
	if _, ok := requestBody["frame_duration_ms"]; ok {
		t.Fatalf("defaults must omit frame_duration_ms: %#v", requestBody)
	}

	disabled, disabledServer := imageGenerationGIFTestHandler(t, false, nil)
	defer disabledServer.Close()
	blocked, err := disabled.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-gif-disabled",
		ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"prompt":"paint a loop","gif":{}}`},
	})
	if err != nil || blocked == nil || blocked.Success || !strings.Contains(blocked.Body, "GIF output is unavailable in this session") {
		t.Fatalf("disabled output = %#v, err = %v", blocked, err)
	}

	bounded, boundedServer := imageGenerationGIFTestHandler(t, true, nil)
	defer boundedServer.Close()
	for _, arguments := range []string{
		`{"prompt":"paint a loop","gif":{"frame_count":1}}`,
		`{"prompt":"paint a loop","gif":{"frame_count":17}}`,
		`{"prompt":"paint a loop","gif":{"frame_duration_ms":19}}`,
		`{"prompt":"paint a loop","gif":{"frame_duration_ms":2001}}`,
	} {
		rejected, err := bounded.Execute(context.Background(), &tool.Invocation{
			CallID:   "call-gif-out-of-range",
			ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: arguments},
		})
		if err != nil || rejected == nil || rejected.Success || !strings.Contains(rejected.Body, "must be between") {
			t.Fatalf("arguments %s: output = %#v, err = %v", arguments, rejected, err)
		}
	}
}

// Rust #59073 advertises the gif argument and its guidance only when the
// animation feature is enabled.
func TestImageGenerationGIFSchemaAndGuidanceLikeRust(t *testing.T) {
	enabledProperties := imageGenerationSchema(true)["properties"].(map[string]any)
	gif, ok := enabledProperties["gif"].(map[string]any)
	if !ok {
		t.Fatalf("gif property missing: %#v", enabledProperties)
	}
	properties := gif["properties"].(map[string]any)
	if properties["frame_count"].(map[string]any)["maximum"] != 16 || properties["frame_duration_ms"].(map[string]any)["maximum"] != 2000 {
		t.Fatalf("gif bounds = %#v", properties)
	}
	if _, ok := imageGenerationSchema(false)["properties"].(map[string]any)["gif"]; ok {
		t.Fatal("disabled schema must not advertise gif")
	}
	if !strings.Contains(imageGenerationDescriptionForGIF(true), "GIF guidelines:") {
		t.Fatal("enabled guidance must include the GIF guidelines")
	}
	if strings.Contains(imageGenerationDescriptionForGIF(false), "GIF guidelines:") {
		t.Fatal("disabled guidance must not mention GIFs")
	}
}
