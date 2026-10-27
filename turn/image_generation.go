package turn

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codex_go/attachmentstore"
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
	// imagegenRequestIDHeader carries the nested ImageGen service request id
	// (Rust #47327). It is retained only for in-process analytics; serialized
	// items and rollout records must not include it.
	imagegenRequestIDHeader = "x-codex-imagegen-request-id"
)

type ImageGenerationOptions struct {
	SessionID string
	CodexHome string
	Provider  model.APIProvider
	Auth      model.AuthHeaders
	// AttachmentStore receives generated images (Rust #54398: the image
	// extension uploads through the session's image store). A nil store means
	// the inline store, which keeps the generated image inline.
	AttachmentStore attachmentstore.Store
	// Ephemeral mirrors `call.ephemeral` in Rust #54398: uploads from ephemeral
	// turns keep their persistence intent, so a durable backend can skip them.
	Ephemeral  bool
	HTTPClient model.HTTPDoer
	InputItems []any
	// RequestIDObserver receives the nested ImageGen request id observed for one
	// call so analytics can retain it without serializing it on the item (Rust
	// #47327 `ImageGenerationItem::imagegen_request_id` is `#[serde(skip)]`).
	RequestIDObserver func(threadID string, callID string, requestID string)
}

type ImageGenerationHandler struct {
	options ImageGenerationOptions
}

type imageGenerationArgs struct {
	Prompt string `json:"prompt"`
	// TransparentBackground mirrors Rust's `transparent_background`
	// (ext/image-generation/src/tool.rs, #47484): true maps the Images API
	// background to `transparent`, false or omission to `opaque`.
	TransparentBackground  bool     `json:"transparent_background"`
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

// imageGenerationAPIError carries the usage-limit failure (if any) alongside
// the API error message (Rust #38024).
type imageGenerationAPIError struct {
	message   string
	failure   map[string]any
	requestID string
	cause     error
}

func (e *imageGenerationAPIError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *imageGenerationAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// imageGenerationRequestError is a failure with a nested ImageGen request id
// but no usage-limit classification (decode failures and empty image data).
type imageGenerationRequestError struct {
	message   string
	requestID string
	cause     error
}

func (e *imageGenerationRequestError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *imageGenerationRequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func imageGenerationErrorRequestID(err error) string {
	var apiErr *imageGenerationAPIError
	if errors.As(err, &apiErr) {
		return strings.TrimSpace(apiErr.requestID)
	}
	var requestErr *imageGenerationRequestError
	if errors.As(err, &requestErr) {
		return strings.TrimSpace(requestErr.requestID)
	}
	return ""
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
	request, inputImages, err := h.requestForArgs(ctx, &args)
	if err != nil {
		return imageGenerationErrorOutput(invocation, args.Prompt, err.Error()), nil
	}
	result, transparentBackground, requestID, err := h.executeImageRequest(ctx, request, imageGenerationTurnID(invocation))
	if err != nil {
		if requestID == "" {
			requestID = imageGenerationErrorRequestID(err)
		}
		h.observeImageGenerationRequestID(invocation, requestID)
		var apiErr *imageGenerationAPIError
		failure := map[string]any(nil)
		if errors.As(err, &apiErr) {
			failure = apiErr.failure
		}
		return imageGenerationErrorOutput(invocation, args.Prompt, "image generation failed: "+err.Error(), failure), nil
	}
	result = strings.TrimSpace(result)
	if result == "" {
		h.observeImageGenerationRequestID(invocation, requestID)
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
	if fileID := generatedImageFileRef(h.options, h.sessionID(), result); fileID != "" {
		// Rust #54398: a durable attachment reference replaces the inline copy
		// on the item. Go keeps `result` as well because the in-process readers
		// (`recentImageURLs`, rollout persistence) still consume it, and Rust's
		// reader prefers `fileId` when a record carries both fields
		// (ext/items/src/image_generation.rs `GeneratedImageRef`: the `File`
		// variant is declared first for exactly that reason). The app-server
		// wire form emits `fileId` and drops `result`.
		data["fileId"] = fileID
		data["file_id"] = fileID
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
	if len(inputImages) > 0 {
		// Rust #54420: the field is absent when no local input was read, so older
		// records and generations without references serialize exactly as before.
		data["inputImages"] = inputImages
		data["input_images"] = inputImages
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

// generatedImageFileRef mirrors the write side of Rust #54398
// (ext/image-generation/src/tool.rs): decode the generated base64 image, upload
// the decoded bytes through the attachment store tagged `usage=image_gen`, and
// return the durable file id when the store answers with a file reference. A
// decode failure, an upload failure, or an inline-only store yields "", and the
// caller keeps the inline result, so no generation is ever lost.
func generatedImageFileRef(options ImageGenerationOptions, threadID string, result string) string {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(result))
	if err != nil {
		return ""
	}
	uploaded, err := attachmentstore.StoreOrDefault(options.AttachmentStore).Upload(attachmentstore.UploadRequest{
		ThreadID:  strings.TrimSpace(threadID),
		Ephemeral: options.Ephemeral,
		Tags:      attachmentstore.ImageGenerationUploadTags(),
		Data:      decoded,
	})
	if err != nil || !uploaded.IsFile() {
		return ""
	}
	return uploaded.FileID
}

// imageGenerationInputImage mirrors Rust's ImageGenerationInputImage (#54420):
// one local image file successfully read for this exact image-generation request.
type imageGenerationInputImage struct {
	Path string `json:"path"`
	// SHA256 is the lowercase hex SHA-256 of the file bytes read before image
	// normalization (Rust: "Lowercase SHA-256 of the file bytes read before
	// image normalization").
	SHA256 string `json:"sha256"`
}

func (h *ImageGenerationHandler) requestForArgs(ctx context.Context, args *imageGenerationArgs) (*imageGenerationRequest, []imageGenerationInputImage, error) {
	if args == nil {
		return nil, nil, fmt.Errorf("image generation arguments are required")
	}
	paths := nonEmptyImagePaths(args.ReferencedImagePaths)
	if len(paths) > imageGenerationMaxEditImages {
		return nil, nil, fmt.Errorf("`referenced_image_paths` must contain at most %d paths", imageGenerationMaxEditImages)
	}
	// Rust #47484: the boolean argument replaces the previous `auto` background,
	// so generation and editing always send an explicit background.
	background := codexapi.ImageBackgroundOpaque
	if args.TransparentBackground {
		background = codexapi.ImageBackgroundTransparent
	}
	if len(paths) == 0 && args.NumLastImagesToInclude == nil {
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
		}, nil, nil
	}
	var images []codexapi.ImageURL
	var inputImages []imageGenerationInputImage
	switch {
	case len(paths) > 0 && args.NumLastImagesToInclude != nil:
		return nil, nil, fmt.Errorf("provide only one of `referenced_image_paths` or `num_last_images_to_include`")
	case len(paths) > 0:
		images = make([]codexapi.ImageURL, 0, len(paths))
		inputImages = make([]imageGenerationInputImage, 0, len(paths))
		for _, path := range paths {
			imageURL, identity, err := imageURLForLocalPath(path)
			if err != nil {
				return nil, nil, err
			}
			images = append(images, codexapi.ImageURL{ImageURL: imageURL})
			// Rust #54420: a completed generation records the local inputs that
			// produced it, identified by the bytes read before normalization.
			inputImages = append(inputImages, imageGenerationInputImage{Path: path, SHA256: identity})
		}
	default:
		count := 0
		if args.NumLastImagesToInclude != nil {
			count = *args.NumLastImagesToInclude
		}
		if count < 1 || count > imageGenerationMaxEditImages {
			return nil, nil, fmt.Errorf("`num_last_images_to_include` must be between 1 and %d", imageGenerationMaxEditImages)
		}
		recent, err := recentImageURLs(h.options.InputItems, count)
		if err != nil {
			return nil, nil, err
		}
		images = recent
	}
	_ = ctx
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
	}, inputImages, nil
}

func (h *ImageGenerationHandler) executeImageRequest(ctx context.Context, request *imageGenerationRequest, turnID string) (string, *bool, string, error) {
	if request == nil {
		return "", nil, "", fmt.Errorf("image request is nil")
	}
	var response *codexapi.ImageResponse
	var requestID string
	var err error
	switch request.kind {
	case imageGenerationRequestGenerate:
		response, requestID, err = h.postImageRequest(ctx, "images/generations", request.generate, turnID)
	case imageGenerationRequestEdit:
		response, requestID, err = h.postImageRequest(ctx, "images/edits", request.edit, turnID)
	default:
		return "", nil, "", fmt.Errorf("unknown image request kind %q", request.kind)
	}
	if err != nil {
		return "", nil, requestID, err
	}
	if response == nil || len(response.Data) == 0 {
		return "", nil, requestID, &imageGenerationRequestError{
			message:   "image generation returned no image data",
			requestID: requestID,
		}
	}
	return response.Data[0].B64JSON, transparentBackgroundValue(response.Background), requestID, nil
}

// observeImageGenerationRequestID records the nested ImageGen request id for
// analytics only; callers must never add it to serialized item data.
func (h *ImageGenerationHandler) observeImageGenerationRequestID(invocation *tool.Invocation, requestID string) {
	requestID = strings.TrimSpace(requestID)
	if h == nil || h.options.RequestIDObserver == nil || requestID == "" || invocation == nil {
		return
	}
	h.options.RequestIDObserver(strings.TrimSpace(h.options.SessionID), invocation.CallID, requestID)
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

func (h *ImageGenerationHandler) postImageRequest(ctx context.Context, path string, payload any, turnID string) (*codexapi.ImageResponse, string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode image request: %w", err)
	}
	endpoint := (&codexapi.Provider{
		Name:        h.options.Provider.Name,
		BaseURL:     h.options.Provider.BaseURL,
		QueryParams: h.options.Provider.QueryParams,
		Headers:     h.options.Provider.Headers,
	}).URLForPath(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if turnID = strings.TrimSpace(turnID); turnID != "" && !strings.ContainsAny(turnID, "\r\n") {
		req.Header.Set(imageTurnIDHeader, turnID)
	}
	addHeaderValues(req.Header, h.options.Provider.Headers)
	signed, err := h.options.Auth.ApplyRequest(ctx, req, body)
	if err != nil {
		return nil, "", err
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
		return nil, "", err
	}
	defer resp.Body.Close()
	requestID := strings.TrimSpace(resp.Header.Get(imagegenRequestIDHeader))
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, requestID, &imageGenerationRequestError{message: readErr.Error(), requestID: requestID, cause: readErr}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(respBody))
		if message == "" {
			message = resp.Status
		}
		if failure := imageGenerationUsageLimitFailure(respBody, resp.Header); failure != nil {
			return nil, requestID, &imageGenerationAPIError{message: message, failure: failure, requestID: requestID}
		}
		return nil, requestID, &imageGenerationRequestError{message: message, requestID: requestID}
	}
	var decoded codexapi.ImageResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, requestID, &imageGenerationRequestError{
			message:   fmt.Sprintf("failed to decode image response: %v", err),
			requestID: requestID,
			cause:     err,
		}
	}
	return &decoded, requestID, nil
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

// imageGenerationUsageLimitFailure classifies a failed image request as a
// structured usage-limit failure. Rust #57275 recognizes the premium imagegen
// limit alongside the ordinary image_gen limit and falls back to the exhausted
// window's reset time when the body carries none.
func imageGenerationUsageLimitFailure(body []byte, headers http.Header) map[string]any {
	var payload struct {
		Error struct {
			LimitID  string `json:"limit_id"`
			ResetsAt *int64 `json:"resets_at"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	limitID := strings.TrimSpace(payload.Error.LimitID)
	if limitID == "" && headers != nil {
		limitID = strings.TrimSpace(headers.Get("x-codex-active-limit"))
	}
	if limitID != "image_gen" && limitID != "imagegen_premium" {
		return nil
	}
	resetsAt := payload.Error.ResetsAt
	if resetsAt == nil {
		resetsAt = imageGenerationExhaustedWindowReset(headers, "x-"+strings.ReplaceAll(limitID, "_", "-"))
	}
	return map[string]any{
		"type":     "usageLimitExceeded",
		"limitId":  limitID,
		"resetsAt": resetsAt,
	}
}

// imageGenerationExhaustedWindowReset mirrors Rust's fallback: the latest reset
// time among the limit's primary and secondary windows that are fully used.
func imageGenerationExhaustedWindowReset(headers http.Header, prefix string) *int64 {
	if headers == nil || prefix == "" {
		return nil
	}
	var latest *int64
	for _, window := range []string{"primary", "secondary"} {
		used, ok := imageGenerationHeaderFloat(headers, prefix+"-"+window+"-used-percent")
		if !ok || used < 100 {
			continue
		}
		reset, ok := imageGenerationHeaderInt(headers, prefix+"-"+window+"-reset-at")
		if !ok {
			continue
		}
		if latest == nil || reset > *latest {
			value := reset
			latest = &value
		}
	}
	return latest
}

func imageGenerationHeaderFloat(headers http.Header, name string) (float64, bool) {
	raw := strings.TrimSpace(headers.Get(name))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func imageGenerationHeaderInt(headers http.Header, name string) (int64, bool) {
	raw := strings.TrimSpace(headers.Get(name))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func imageGenerationErrorOutput(invocation *tool.Invocation, prompt string, message string, failures ...map[string]any) *tool.Output {
	data := map[string]any{
		"image_generation": true,
		"status":           "failed",
		"revisedPrompt":    strings.TrimSpace(prompt),
		"revised_prompt":   strings.TrimSpace(prompt),
	}
	if len(failures) > 0 && failures[0] != nil {
		data["failure"] = failures[0]
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

// imageURLForLocalPath inlines a local image and reports the lowercase SHA-256 of
// the bytes read, which is the identity Rust #54420 records before normalization.
func imageURLForLocalPath(path string) (string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", fmt.Errorf("referenced image path must not be empty")
	}
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("referenced image path must be absolute: %s", path)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("unable to read referenced image at `%s`: %w", path, err)
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
	sum := sha256.Sum256(bytes)
	return utils.DataURLFromBytes(mimeType, bytes), hex.EncodeToString(sum[:]), nil
}

// recentImageEntry is one image in a recent-images window: an inline URL, or a
// file reference the harness cannot inline (Rust #45794).
type recentImageEntry struct {
	imageURL string
	fileID   string
}

func (e recentImageEntry) inline() bool { return strings.TrimSpace(e.imageURL) != "" }

// recentImageURLs mirrors Rust's `recent_images`: it walks the history
// newest-first, counts the last `count` images including file references (so
// numbering matches Rust), then rejects a window that is incomplete or that
// contains a file-backed image instead of silently editing an older inline one.
func recentImageURLs(items []any, count int) ([]codexapi.ImageURL, error) {
	if count <= 0 {
		return nil, nil
	}
	entries := make([]recentImageEntry, 0, count)
	for i := len(items) - 1; i >= 0 && len(entries) < count; i-- {
		for _, entry := range imageEntriesFromInputItem(items[i]) {
			if !entry.inline() && strings.TrimSpace(entry.fileID) == "" {
				continue
			}
			entries = append(entries, entry)
			if len(entries) == count {
				break
			}
		}
	}
	if len(entries) != count {
		return nil, fmt.Errorf("requested the last %d conversation images, but only %d were available", count, len(entries))
	}
	out := make([]codexapi.ImageURL, 0, count)
	for _, entry := range entries {
		if !entry.inline() {
			return nil, fmt.Errorf("requested the last %d conversation images, but that window includes a file-backed image that cannot be used for editing", count)
		}
		out = append(out, codexapi.ImageURL{ImageURL: strings.TrimSpace(entry.imageURL)})
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out, nil
}

func imageEntriesFromInputItem(item any) []recentImageEntry {
	normalized, ok := mapAnyTurn(item)
	if !ok {
		if agentItem, ok := item.(*model.AgentItem); ok && agentItem != nil && agentItem.Type == "image_generation_call" {
			if result := strings.TrimSpace(firstNonEmptyTurnString(stringValueFromMap(agentItem.Data, "result"), agentItem.Text)); result != "" {
				return []recentImageEntry{{imageURL: imageGenerationOutputPrefix + result}}
			}
		}
		if response, ok := item.(*ToolResponseItem); ok && response != nil && response.Output != nil {
			return outputImageEntries(response.Output.Body)
		}
		return nil
	}
	itemType, _ := normalized["type"].(string)
	switch itemType {
	case "message":
		return imageEntriesFromContent(normalized["content"])
	case "image_generation_call", "imageGeneration", "image_generation":
		if result := stringFromMapTurn(normalized, "result"); result != "" {
			return []recentImageEntry{{imageURL: imageGenerationOutputPrefix + result}}
		}
		if data, ok := normalized["data"].(map[string]any); ok {
			if result := stringFromMapTurn(data, "result"); result != "" {
				return []recentImageEntry{{imageURL: imageGenerationOutputPrefix + result}}
			}
		}
	case "function_call_output", "custom_tool_call_output":
		if output, ok := normalized["output"]; ok {
			return outputImageEntries(output)
		}
	}
	return nil
}

func imageEntriesFromContent(value any) []recentImageEntry {
	items, ok := sliceAnyTurn(value)
	if !ok {
		return nil
	}
	out := make([]recentImageEntry, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		content, ok := mapAnyTurn(items[i])
		if !ok {
			continue
		}
		itemType, _ := content["type"].(string)
		switch itemType {
		case "input_image", "image":
			out = append(out, imageEntryFromMap(content))
		}
	}
	return out
}

// imageEntryFromMap reads an image content entry, keeping a file reference
// distinct from an inline URL (Rust #45794).
func imageEntryFromMap(content map[string]any) recentImageEntry {
	return recentImageEntry{
		imageURL: stringFromMapTurn(content, "image_url", "imageURL", "url"),
		fileID:   stringFromMapTurn(content, "file_id", "fileId"),
	}
}

func outputImageEntries(value any) []recentImageEntry {
	switch typed := value.(type) {
	case []FunctionCallOutputContentItem:
		out := make([]recentImageEntry, 0, len(typed))
		for i := len(typed) - 1; i >= 0; i-- {
			if typed[i].Type != "input_image" {
				continue
			}
			entry := recentImageEntry{imageURL: strings.TrimSpace(typed[i].ImageURL), fileID: strings.TrimSpace(typed[i].FileID)}
			if entry.inline() || entry.fileID != "" {
				out = append(out, entry)
			}
		}
		return out
	case []any:
		out := make([]recentImageEntry, 0, len(typed))
		for i := len(typed) - 1; i >= 0; i-- {
			item, ok := mapAnyTurn(typed[i])
			if !ok {
				continue
			}
			if itemType, _ := item["type"].(string); itemType == "input_image" {
				out = append(out, imageEntryFromMap(item))
			}
		}
		return out
	case map[string]any:
		if content, ok := typed["content"]; ok {
			return outputImageEntries(content)
		}
	}
	normalized, ok := normalizeAnyMap(value)
	if !ok {
		return nil
	}
	if content, ok := normalized["content"]; ok {
		return outputImageEntries(content)
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
			"transparent_background": map[string]any{
				"type":        "boolean",
				"description": "Whether the output should have a transparent background. Defaults to false.",
				"default":     false,
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

const imageGenerationDescription = `The ` + "`image_gen.imagegen`" + ` tool enables image generation from descriptions and editing of existing images based on specific instructions. Use it when:

- The user requests an image based on a scene description, such as a diagram, portrait, comic, meme, or any other visual.
- The user wants to modify an attached or previously generated image with specific changes, including adding or removing elements, altering colors, improving quality/resolution, or transforming the style (e.g., cartoon, oil painting).

Guidelines:
- imagegen needs a few minutes to finish. In code-mode, use the first-line @exec directive to give the initial call 120 seconds and the same yield for any waits that follow. Once it finishes, return the image with generatedImage(result).
- Avoid printing the full result or its base64 image data with ` + "`text()`" + ` or ` + "`notify()`" + `; print only small metadata when needed.
- Set ` + "`transparent_background`" + ` to true when the request calls for a transparent background, including background removal or a cutout; set it to false otherwise. For edits, preserve existing transparency unless the user asks to change it.
- Omit both ` + "`referenced_image_paths`" + ` and ` + "`num_last_images_to_include`" + ` when generating a brand new image.
- For edits, use ` + "`referenced_image_paths`" + ` when every target image has a local file path.
- If you have not seen a local image yet, use ` + "`view_image`" + ` to inspect it before editing.
- Use ` + "`num_last_images_to_include`" + ` only when at least one target image has no local file path.
- Set ` + "`num_last_images_to_include`" + ` to the smallest number of recent conversation images that includes every target image, up to 5.
- Never provide both ` + "`referenced_image_paths`" + ` and ` + "`num_last_images_to_include`" + `.
- If neither mechanism can include every target image, ask the user to attach the missing images again.
- Directly generate the image without reconfirmation or clarification unless required images must be attached again.
- Always use this tool for image editing unless the user explicitly requests otherwise. Do not use the ` + "`python`" + ` tool for image editing unless specifically instructed.
`

var _ tool.Executor = (*ImageGenerationHandler)(nil)
