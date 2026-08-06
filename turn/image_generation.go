package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/codexapi"
	"codex_go/eventmap"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/utils"
)

const (
	ImageGenerationNamespace = "image_gen"
	ImageGenerationToolName  = "imagegen"

	imageGenerationModel         = "gpt-image-2"
	imageGenerationMaxEditImages = 5
	imageGenerationDefaultDetail = "high"
	imageGenerationOutputPrefix  = "data:image/png;base64,"
	imageTurnIDHeader            = "x-codex-image-turn-id"
)

type ImageGenerationOptions struct {
	SessionID  string
	CodexHome  string
	Provider   model.APIProvider
	Auth       model.AuthHeaders
	HTTPClient model.HTTPDoer
	InputItems []any
}

type ImageGenerationHandler struct {
	options ImageGenerationOptions
}

type imageGenerationArgs struct {
	Prompt                 string   `json:"prompt"`
	ReferencedImagePaths   []string `json:"referenced_image_paths,omitempty"`
	NumLastImagesToInclude *int     `json:"num_last_images_to_include,omitempty"`
}

type imageGenerationRequestKind string

const (
	imageGenerationRequestGenerate imageGenerationRequestKind = "generate"
	imageGenerationRequestEdit     imageGenerationRequestKind = "edit"
)

type imageGenerationRequest struct {
	kind     imageGenerationRequestKind
	generate codexapi.ImageGenerationRequest
	edit     codexapi.ImageEditRequest
}

func NewImageGenerationHandler(options *ImageGenerationOptions) *ImageGenerationHandler {
	if options == nil {
		options = &ImageGenerationOptions{}
	}
	return &ImageGenerationHandler{options: *options}
}

func (h *ImageGenerationHandler) Spec() tool.Spec {
	return tool.Spec{
		Name:                 tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
		Description:          imageGenerationDescription,
		InputSchema:          imageGenerationSchema(),
		Exposure:             tool.ExposureModelVisible,
		Parallel:             false,
		NamespaceDescription: "Tools in the image_gen namespace.",
	}
}

func (h *ImageGenerationHandler) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", tool.ErrToolInvalidCall)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var args imageGenerationArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Prompt == "" {
		return imageGenerationErrorOutput(invocation, args.Prompt, "prompt must not be empty"), nil
	}
	request, err := h.requestForArgs(ctx, &args)
	if err != nil {
		return imageGenerationErrorOutput(invocation, args.Prompt, err.Error()), nil
	}
	result, transparentBackground, err := h.executeImageRequest(ctx, request, imageGenerationTurnID(invocation))
	if err != nil {
		return imageGenerationErrorOutput(invocation, args.Prompt, "image generation failed: "+err.Error()), nil
	}
	result = strings.TrimSpace(result)
	if result == "" {
		return imageGenerationErrorOutput(invocation, args.Prompt, "image generation returned no image data"), nil
	}
	savedPath := ""
	if codexHome := strings.TrimSpace(h.options.CodexHome); codexHome != "" {
		if path, err := eventmap.SaveImageGenerationResult(codexHome, h.sessionID(), invocation.CallID, result); err == nil {
			savedPath = path
		}
	}
	hint := imageGenerationOutputHint(savedPath)
	detail := imageGenerationDefaultDetail
	contentItems := []FunctionCallOutputContentItem{{
		Type:     "input_image",
		ImageURL: imageGenerationOutputPrefix + result,
		Detail:   &detail,
	}}
	if hint != "" {
		contentItems = append(contentItems, FunctionCallOutputContentItem{Type: "input_text", Text: hint})
	}
	data := map[string]any{
		"content_items":    contentItems,
		"image_generation": true,
		"status":           "completed",
		"result":           result,
		"revisedPrompt":    args.Prompt,
		"revised_prompt":   args.Prompt,
		"image_url":        imageGenerationOutputPrefix + result,
	}
	if transparentBackground != nil {
		data["transparentBackground"] = *transparentBackground
		data["transparent_background"] = *transparentBackground
	}
	if savedPath != "" {
		data["savedPath"] = savedPath
		data["saved_path"] = savedPath
	}
	if hint != "" {
		data["output_hint"] = hint
	}
	return &tool.Output{
		CallID:      invocation.CallID,
		ToolName:    invocation.ToolName,
		Success:     true,
		Body:        "[generated image]",
		LogPreview:  "[generated image]",
		Data:        data,
		CompletedAt: time.Now().UTC(),
	}, nil
}

func (h *ImageGenerationHandler) requestForArgs(ctx context.Context, args *imageGenerationArgs) (*imageGenerationRequest, error) {
	if args == nil {
		return nil, fmt.Errorf("image generation arguments are required")
	}
	paths := nonEmptyImagePaths(args.ReferencedImagePaths)
	if len(paths) > imageGenerationMaxEditImages {
		return nil, fmt.Errorf("`referenced_image_paths` must contain at most %d paths", imageGenerationMaxEditImages)
	}
	if len(paths) == 0 && args.NumLastImagesToInclude == nil {
		background := codexapi.ImageBackgroundAuto
		quality := codexapi.ImageQualityAuto
		size := "auto"
		return &imageGenerationRequest{
			kind: imageGenerationRequestGenerate,
			generate: codexapi.ImageGenerationRequest{
				Prompt:     args.Prompt,
				Background: &background,
				Model:      imageGenerationModel,
				Quality:    &quality,
				Size:       &size,
			},
		}, nil
	}
	var images []codexapi.ImageURL
	switch {
	case len(paths) > 0 && args.NumLastImagesToInclude != nil:
		return nil, fmt.Errorf("provide only one of `referenced_image_paths` or `num_last_images_to_include`")
	case len(paths) > 0:
		images = make([]codexapi.ImageURL, 0, len(paths))
		for _, path := range paths {
			imageURL, err := imageURLForLocalPath(path)
			if err != nil {
				return nil, err
			}
			images = append(images, codexapi.ImageURL{ImageURL: imageURL})
		}
	default:
		count := 0
		if args.NumLastImagesToInclude != nil {
			count = *args.NumLastImagesToInclude
		}
		if count < 1 || count > imageGenerationMaxEditImages {
			return nil, fmt.Errorf("`num_last_images_to_include` must be between 1 and %d", imageGenerationMaxEditImages)
		}
		images = recentImageURLs(h.options.InputItems, count)
		if len(images) != count {
			return nil, fmt.Errorf("requested the last %d conversation images, but only %d were available", count, len(images))
		}
	}
	_ = ctx
	background := codexapi.ImageBackgroundAuto
	quality := codexapi.ImageQualityAuto
	size := "auto"
	return &imageGenerationRequest{
		kind: imageGenerationRequestEdit,
		edit: codexapi.ImageEditRequest{
			Images:     images,
			Prompt:     args.Prompt,
			Background: &background,
			Model:      imageGenerationModel,
			Quality:    &quality,
			Size:       &size,
		},
	}, nil
}

func (h *ImageGenerationHandler) executeImageRequest(ctx context.Context, request *imageGenerationRequest, turnID string) (string, *bool, error) {
	if request == nil {
		return "", nil, fmt.Errorf("image request is nil")
	}
	var response *codexapi.ImageResponse
	var err error
	switch request.kind {
	case imageGenerationRequestGenerate:
		response, err = h.postImageRequest(ctx, "images/generations", request.generate, turnID)
	case imageGenerationRequestEdit:
		response, err = h.postImageRequest(ctx, "images/edits", request.edit, turnID)
	default:
		return "", nil, fmt.Errorf("unknown image request kind %q", request.kind)
	}
	if err != nil {
		return "", nil, err
	}
	if response == nil || len(response.Data) == 0 {
		return "", nil, fmt.Errorf("image generation returned no image data")
	}
	return response.Data[0].B64JSON, transparentBackgroundValue(response.Background), nil
}

// transparentBackgroundValue mirrors Rust's Images API background mapping:
// transparent -> true, opaque -> false, auto/absent -> nil (null).
func transparentBackgroundValue(background *codexapi.ImageBackground) *bool {
	if background == nil {
		return nil
	}
	switch *background {
	case codexapi.ImageBackgroundTransparent:
		value := true
		return &value
	case codexapi.ImageBackgroundOpaque:
		value := false
		return &value
	default: // auto or unrecognized
		return nil
	}
}

func (h *ImageGenerationHandler) postImageRequest(ctx context.Context, path string, payload any, turnID string) (*codexapi.ImageResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode image request: %w", err)
	}
	endpoint := (&codexapi.Provider{
		Name:        h.options.Provider.Name,
		BaseURL:     h.options.Provider.BaseURL,
		QueryParams: h.options.Provider.QueryParams,
		Headers:     h.options.Provider.Headers,
	}).URLForPath(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if turnID = strings.TrimSpace(turnID); turnID != "" && !strings.ContainsAny(turnID, "\r\n") {
		req.Header.Set(imageTurnIDHeader, turnID)
	}
	addHeaderValues(req.Header, h.options.Provider.Headers)
	signed, err := h.options.Auth.ApplyRequest(ctx, req, body)
	if err != nil {
		return nil, err
	}
	if signed != nil && signed.Body != nil {
		body = signed.Body
	}
	if req.Body == nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		req.ContentLength = int64(len(body))
	}
	client := h.options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(respBody))
		if message == "" {
			message = resp.Status
		}
		return nil, errors.New(message)
	}
	var decoded codexapi.ImageResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("failed to decode image response: %w", err)
	}
	return &decoded, nil
}

func imageGenerationTurnID(invocation *tool.Invocation) string {
	if invocation == nil || invocation.Context == nil {
		return ""
	}
	for _, key := range []string{"turn_id", "turnId"} {
		if value, ok := invocation.Context[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (h *ImageGenerationHandler) sessionID() string {
	if h == nil {
		return ""
	}
	if value := strings.TrimSpace(h.options.SessionID); value != "" {
		return value
	}
	return "session"
}

func imageGenerationErrorOutput(invocation *tool.Invocation, prompt string, message string) *tool.Output {
	data := map[string]any{
		"image_generation": true,
		"status":           "failed",
		"revisedPrompt":    strings.TrimSpace(prompt),
		"revised_prompt":   strings.TrimSpace(prompt),
	}
	return &tool.Output{
		CallID:      invocation.CallID,
		ToolName:    invocation.ToolName,
		Success:     false,
		Body:        message,
		Error:       message,
		LogPreview:  message,
		Data:        data,
		CompletedAt: time.Now().UTC(),
	}
}

func imageURLForLocalPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("referenced image path must not be empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("referenced image path must be absolute: %s", path)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("unable to read referenced image at `%s`: %w", path, err)
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if idx := strings.IndexByte(mimeType, ';'); idx >= 0 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(bytes)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return utils.DataURLFromBytes(mimeType, bytes), nil
}

func recentImageURLs(items []any, count int) []codexapi.ImageURL {
	if count <= 0 {
		return nil
	}
	out := make([]codexapi.ImageURL, 0, count)
	for i := len(items) - 1; i >= 0 && len(out) < count; i-- {
		for _, imageURL := range imageURLsFromInputItem(items[i]) {
			if strings.TrimSpace(imageURL) == "" {
				continue
			}
			out = append(out, codexapi.ImageURL{ImageURL: imageURL})
			if len(out) == count {
				break
			}
		}
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func imageURLsFromInputItem(item any) []string {
	normalized, ok := mapAnyTurn(item)
	if !ok {
		if agentItem, ok := item.(*model.AgentItem); ok && agentItem != nil && agentItem.Type == "image_generation_call" {
			if result := strings.TrimSpace(firstNonEmptyTurnString(stringValueFromMap(agentItem.Data, "result"), agentItem.Text)); result != "" {
				return []string{imageGenerationOutputPrefix + result}
			}
		}
		if response, ok := item.(*ToolResponseItem); ok && response != nil && response.Output != nil {
			return outputImageURLs(response.Output.Body)
		}
		return nil
	}
	itemType, _ := normalized["type"].(string)
	switch itemType {
	case "message":
		return imageURLsFromContent(normalized["content"])
	case "image_generation_call", "imageGeneration", "image_generation":
		if result := stringFromMapTurn(normalized, "result"); result != "" {
			return []string{imageGenerationOutputPrefix + result}
		}
		if data, ok := normalized["data"].(map[string]any); ok {
			if result := stringFromMapTurn(data, "result"); result != "" {
				return []string{imageGenerationOutputPrefix + result}
			}
		}
	case "function_call_output", "custom_tool_call_output":
		if output, ok := normalized["output"]; ok {
			return outputImageURLs(output)
		}
	}
	return nil
}

func imageURLsFromContent(value any) []string {
	items, ok := sliceAnyTurn(value)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		content, ok := mapAnyTurn(items[i])
		if !ok {
			continue
		}
		itemType, _ := content["type"].(string)
		switch itemType {
		case "input_image", "image":
			if imageURL := stringFromMapTurn(content, "image_url", "imageURL", "url"); imageURL != "" {
				out = append(out, imageURL)
			}
		}
	}
	return out
}

func outputImageURLs(value any) []string {
	switch typed := value.(type) {
	case []FunctionCallOutputContentItem:
		out := make([]string, 0, len(typed))
		for i := len(typed) - 1; i >= 0; i-- {
			if typed[i].Type == "input_image" && strings.TrimSpace(typed[i].ImageURL) != "" {
				out = append(out, strings.TrimSpace(typed[i].ImageURL))
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for i := len(typed) - 1; i >= 0; i-- {
			item, ok := mapAnyTurn(typed[i])
			if !ok {
				continue
			}
			if itemType, _ := item["type"].(string); itemType == "input_image" {
				if imageURL := stringFromMapTurn(item, "image_url", "imageURL"); imageURL != "" {
					out = append(out, imageURL)
				}
			}
		}
		return out
	case map[string]any:
		if content, ok := typed["content"]; ok {
			return outputImageURLs(content)
		}
	}
	normalized, ok := normalizeAnyMap(value)
	if !ok {
		return nil
	}
	if content, ok := normalized["content"]; ok {
		return outputImageURLs(content)
	}
	return nil
}

func normalizeAnyMap(value any) (map[string]any, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

func stringValueFromMap(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nonEmptyImagePaths(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func imageGenerationOutputHint(savedPath string) string {
	savedPath = strings.TrimSpace(savedPath)
	if savedPath == "" {
		return ""
	}
	outputDir := filepath.Dir(savedPath)
	hint := fmt.Sprintf("Generated images are saved to %s as %s by default.\nIf you need to use a generated image at another path, copy it and leave the original in place unless the user explicitly asks you to delete it.", outputDir, savedPath)
	if len(hint) > 1024 {
		return ""
	}
	return hint
}

func imageGenerationSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"prompt"},
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "The prompt for the image to generate or edit.",
			},
			"referenced_image_paths": map[string]any{
				"type":        "array",
				"description": "Absolute local image paths to edit. Omit when generating a brand new image.",
				"maxItems":    imageGenerationMaxEditImages,
				"items":       map[string]any{"type": "string"},
			},
			"num_last_images_to_include": map[string]any{
				"type":        "integer",
				"description": "Number of recent conversation images to include for edits when local paths are unavailable.",
				"minimum":     1,
				"maximum":     imageGenerationMaxEditImages,
			},
		},
	}
}

const imageGenerationDescription = `The image_gen.imagegen tool enables image generation from descriptions and editing of existing images based on specific instructions. Use it when:

- The user requests an image based on a scene description, such as a diagram, portrait, comic, meme, or any other visual.
- The user wants to modify an attached or previously generated image with specific changes, including adding or removing elements, altering colors, improving quality/resolution, or transforming the style (e.g., cartoon, oil painting).

Guidelines:
- Omit both referenced_image_paths and num_last_images_to_include when generating a brand new image.
- For edits, use referenced_image_paths when every target image has a local file path.
- If you have not seen a local image yet, inspect it before editing.
- Use num_last_images_to_include only when at least one target image has no local file path.
- Set num_last_images_to_include to the smallest number of recent conversation images that includes every target image, up to 5.
- Never provide both referenced_image_paths and num_last_images_to_include.
- Directly generate the image without reconfirmation or clarification unless required images must be attached again.
- After each image generation, do not mention anything related to download. Do not summarize the image. Do not ask followup question. Do not say anything after you generate an image.`

var _ tool.Executor = (*ImageGenerationHandler)(nil)
