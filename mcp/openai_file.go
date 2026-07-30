package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/utils"

	"github.com/google/uuid"
)

const (
	DefaultOpenAIFileUploadLimitBytes   int64 = 512 * 1024 * 1024
	defaultOpenAIFileBaseURL                  = "https://chatgpt.com/backend-api"
	defaultOpenAIFileRequestTimeout           = 60 * time.Second
	defaultOpenAIFileFinalizeTimeout          = 30 * time.Second
	defaultOpenAIFileFinalizeRetryDelay       = 250 * time.Millisecond
	maxOpenAIFileResponseBytes          int64 = 1024 * 1024
)

type OpenAIFileHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type OpenAIFileAuth struct {
	ChatGPTBackend bool
	BaseURL        string
	Headers        http.Header
	ApplyRequest   func(*http.Request, []byte) error
}

func OpenAIFileAuthFromRuntimeAuth(runtimeAuth *RuntimeAuth, baseURL string) *OpenAIFileAuth {
	if runtimeAuth == nil || !runtimeAuth.UsesCodexBackend {
		return nil
	}
	headers := http.Header{}
	if runtimeAuth.ApplyHTTPRequest == nil {
		for name, value := range runtimeAuth.HTTPHeaders {
			if strings.TrimSpace(name) != "" {
				headers.Set(name, value)
			}
		}
	}
	return &OpenAIFileAuth{
		ChatGPTBackend: true,
		BaseURL:        strings.TrimSpace(baseURL),
		Headers:        headers,
		ApplyRequest:   runtimeAuth.ApplyHTTPRequest,
	}
}

type OpenAIUploadedFile struct {
	DownloadURL   string `json:"download_url,omitempty"`
	FileID        string `json:"file_id"`
	MimeType      string `json:"mime_type,omitempty"`
	FileName      string `json:"file_name"`
	URI           string `json:"uri,omitempty"`
	FileSizeBytes int64  `json:"file_size_bytes"`
}

type OpenAIFileUploadRequest struct {
	Path          string
	FileName      string
	FileSizeBytes int64
	Open          func(context.Context) (io.ReadCloser, error)
}

type OpenAIFileUploader interface {
	UploadOpenAIFile(ctx context.Context, request OpenAIFileUploadRequest) (*OpenAIUploadedFile, error)
}

type OpenAIFileMetadata struct {
	IsFile bool
	Size   int64
}

type OpenAIFileSystem interface {
	Metadata(ctx context.Context, pathURI string) (*OpenAIFileMetadata, error)
	Open(ctx context.Context, pathURI string) (io.ReadCloser, error)
}

type localOpenAIFileSystem struct{}

func (localOpenAIFileSystem) Metadata(_ context.Context, pathURI string) (*OpenAIFileMetadata, error) {
	path, err := openAIFileHostPath(pathURI)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &OpenAIFileMetadata{IsFile: info.Mode().IsRegular(), Size: info.Size()}, nil
}

func (localOpenAIFileSystem) Open(_ context.Context, pathURI string) (io.ReadCloser, error) {
	path, err := openAIFileHostPath(pathURI)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func openAIFileHostPath(pathURI string) (string, error) {
	parsed, err := utils.Parse(pathURI)
	if err != nil {
		return "", err
	}
	return parsed.HostNativePath()
}

type LocalOpenAIFileUploader struct {
	BaseURL          string
	Auth             *OpenAIFileAuth
	HTTPClient       OpenAIFileHTTPDoer
	RequestTimeout   time.Duration
	FinalizeTimeout  time.Duration
	FinalizeInterval time.Duration
}

func (u *LocalOpenAIFileUploader) UploadOpenAIFile(ctx context.Context, request OpenAIFileUploadRequest) (*OpenAIUploadedFile, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if u == nil || u.Auth == nil || !u.Auth.ChatGPTBackend {
		return nil, fmt.Errorf("ChatGPT auth is required to upload files for Codex Apps tools")
	}
	if request.FileSizeBytes > DefaultOpenAIFileUploadLimitBytes {
		return nil, fmt.Errorf("file `%s` is too large: %d bytes exceeds the limit of %d bytes", request.FileName, request.FileSizeBytes, DefaultOpenAIFileUploadLimitBytes)
	}
	baseURL := strings.TrimRight(firstNonEmptyMCP(strings.TrimSpace(u.BaseURL), strings.TrimSpace(u.Auth.BaseURL), defaultOpenAIFileBaseURL), "/")
	createURL := baseURL + "/files"
	createBody := map[string]any{
		"file_name": request.FileName,
		"file_size": request.FileSizeBytes,
		"use_case":  "codex",
	}
	var created struct {
		FileID    string `json:"file_id"`
		UploadURL string `json:"upload_url"`
	}
	if err := u.authorizedJSON(ctx, http.MethodPost, createURL, createBody, &created); err != nil {
		return nil, err
	}
	if strings.TrimSpace(created.FileID) == "" || strings.TrimSpace(created.UploadURL) == "" {
		return nil, fmt.Errorf("failed to decode OpenAI file response from %s: missing file_id or upload_url", createURL)
	}

	if err := u.uploadBlob(ctx, created.UploadURL, request); err != nil {
		return nil, err
	}

	finalizeURL := baseURL + "/files/" + url.PathEscape(created.FileID) + "/uploaded"
	deadline := time.Now().Add(u.finalizeTimeout())
	for {
		var finalized struct {
			Status       string `json:"status"`
			DownloadURL  string `json:"download_url"`
			FileName     string `json:"file_name"`
			MimeType     string `json:"mime_type"`
			ErrorMessage string `json:"error_message"`
		}
		if err := u.authorizedJSON(ctx, http.MethodPost, finalizeURL, map[string]any{}, &finalized); err != nil {
			return nil, err
		}
		switch finalized.Status {
		case "success":
			if strings.TrimSpace(finalized.DownloadURL) == "" {
				return nil, fmt.Errorf("OpenAI file upload failed for %s: missing download_url", created.FileID)
			}
			fileName := strings.TrimSpace(finalized.FileName)
			if fileName == "" {
				fileName = request.FileName
			}
			return &OpenAIUploadedFile{
				DownloadURL:   finalized.DownloadURL,
				FileID:        created.FileID,
				MimeType:      finalized.MimeType,
				FileName:      fileName,
				URI:           "sediment://" + created.FileID,
				FileSizeBytes: request.FileSizeBytes,
			}, nil
		case "retry":
			if !time.Now().Before(deadline) {
				return nil, fmt.Errorf("OpenAI file upload is not ready for %s", created.FileID)
			}
			timer := time.NewTimer(u.finalizeInterval())
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return nil, ctx.Err()
			case <-timer.C:
			}
		default:
			message := strings.TrimSpace(finalized.ErrorMessage)
			if message == "" {
				message = "upload finalization returned an error"
			}
			return nil, fmt.Errorf("OpenAI file upload failed for %s: %s", created.FileID, message)
		}
	}
}

func (u *LocalOpenAIFileUploader) authorizedJSON(ctx context.Context, method string, endpoint string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode OpenAI file request for %s: %w", endpoint, err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, u.requestTimeout())
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build OpenAI file request for %s: %w", endpoint, err)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, values := range u.Auth.Headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if u.Auth.ApplyRequest != nil {
		if err := u.Auth.ApplyRequest(request, body); err != nil {
			return fmt.Errorf("failed to authorize OpenAI file request for %s: %w", endpoint, err)
		}
	}
	response, err := u.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("OpenAI file request to %s failed: %w", endpoint, err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxOpenAIFileResponseBytes))
	if readErr != nil {
		return fmt.Errorf("failed to read OpenAI file response from %s: %w", endpoint, readErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("OpenAI file request to %s failed with status %s: %s", endpoint, response.Status, strings.TrimSpace(string(responseBody)))
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("failed to decode OpenAI file response from %s: %w", endpoint, err)
	}
	return nil
}

func (u *LocalOpenAIFileUploader) uploadBlob(ctx context.Context, uploadURL string, request OpenAIFileUploadRequest) error {
	requestID := uuid.NewString()
	host := openAIFileUploadHost(uploadURL)
	started := time.Now()
	requestCtx, cancel := context.WithTimeout(ctx, u.requestTimeout())
	defer cancel()
	var contents io.ReadCloser
	var err error
	if request.Open != nil {
		contents, err = request.Open(requestCtx)
	} else {
		contents, err = os.Open(request.Path)
	}
	if err != nil {
		return err
	}
	defer contents.Close()
	uploadRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPut, uploadURL, contents)
	if err != nil {
		return fmt.Errorf("OpenAI file blob upload failed after %dms (request, host=%s, azure_client_request_id=%s)", time.Since(started).Milliseconds(), host, requestID)
	}
	uploadRequest.ContentLength = request.FileSizeBytes
	uploadRequest.Header.Set("Content-Length", fmt.Sprintf("%d", request.FileSizeBytes))
	uploadRequest.Header.Set("x-ms-blob-type", "BlockBlob")
	uploadRequest.Header.Set("x-ms-client-request-id", requestID)
	response, err := u.httpClient().Do(uploadRequest)
	if err != nil {
		return fmt.Errorf("OpenAI file blob upload failed after %dms (%s, host=%s, azure_client_request_id=%s)", time.Since(started).Milliseconds(), openAIFileUploadErrorKind(err), host, requestID)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"OpenAI file blob upload to %s failed with status %d (azure_client_request_id=%s, azure_request_id=%s, azure_error_code=%s)",
			host,
			response.StatusCode,
			requestID,
			openAIFileResponseHeader(response, "x-ms-request-id"),
			openAIFileResponseHeader(response, "x-ms-error-code"),
		)
	}
	return nil
}

func (u *LocalOpenAIFileUploader) httpClient() OpenAIFileHTTPDoer {
	if u != nil && u.HTTPClient != nil {
		return u.HTTPClient
	}
	return http.DefaultClient
}

func (u *LocalOpenAIFileUploader) requestTimeout() time.Duration {
	if u != nil && u.RequestTimeout > 0 {
		return u.RequestTimeout
	}
	return defaultOpenAIFileRequestTimeout
}

func (u *LocalOpenAIFileUploader) finalizeTimeout() time.Duration {
	if u != nil && u.FinalizeTimeout > 0 {
		return u.FinalizeTimeout
	}
	return defaultOpenAIFileFinalizeTimeout
}

func (u *LocalOpenAIFileUploader) finalizeInterval() time.Duration {
	if u != nil && u.FinalizeInterval > 0 {
		return u.FinalizeInterval
	}
	return defaultOpenAIFileFinalizeRetryDelay
}

func openAIFileUploadHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err == nil && strings.TrimSpace(parsed.Hostname()) != "" {
		return parsed.Hostname()
	}
	return "unknown-host"
}

func openAIFileUploadErrorKind(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "connect"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return "request"
	}
	return "other"
}

func openAIFileResponseHeader(response *http.Response, name string) string {
	if response != nil {
		if value := strings.TrimSpace(response.Header.Get(name)); value != "" {
			return value
		}
	}
	return "missing"
}

type OpenAIFileRewriterOptions struct {
	CWD              string
	Auth             *OpenAIFileAuth
	Uploader         OpenAIFileUploader
	FileSystem       OpenAIFileSystem
	HTTPClient       OpenAIFileHTTPDoer
	UploadLimitBytes int64
}

type OpenAIFileRewriter struct {
	CWD              string
	Auth             *OpenAIFileAuth
	Uploader         OpenAIFileUploader
	FileSystem       OpenAIFileSystem
	UploadLimitBytes int64
}

func NewOpenAIFileRewriter(cwd string, auth *OpenAIFileAuth, uploader OpenAIFileUploader) *OpenAIFileRewriter {
	return NewOpenAIFileRewriterWithOptions(OpenAIFileRewriterOptions{CWD: cwd, Auth: auth, Uploader: uploader})
}

func NewOpenAIFileRewriterWithOptions(options OpenAIFileRewriterOptions) *OpenAIFileRewriter {
	uploader := options.Uploader
	if uploader == nil {
		uploader = &LocalOpenAIFileUploader{Auth: options.Auth, HTTPClient: options.HTTPClient}
	}
	limit := options.UploadLimitBytes
	if limit <= 0 {
		limit = DefaultOpenAIFileUploadLimitBytes
	}
	return &OpenAIFileRewriter{
		CWD:              options.CWD,
		Auth:             options.Auth,
		Uploader:         uploader,
		FileSystem:       options.FileSystem,
		UploadLimitBytes: limit,
	}
}

func (r *OpenAIFileRewriter) RewriteArguments(ctx context.Context, arguments any, openAIFileInputParams []string) (any, error) {
	optionalFields := make(map[string][]string, len(openAIFileInputParams))
	for _, fieldName := range openAIFileInputParams {
		if strings.TrimSpace(fieldName) != "" {
			optionalFields[fieldName] = nil
		}
	}
	return r.RewriteArgumentsWithOptionalFields(ctx, arguments, optionalFields)
}

func (r *OpenAIFileRewriter) RewriteArgumentsWithOptionalFields(ctx context.Context, arguments any, openAIFileInputOptionalFields map[string][]string) (any, error) {
	if len(openAIFileInputOptionalFields) == 0 || arguments == nil {
		return arguments, nil
	}
	object, ok := arguments.(map[string]any)
	if !ok {
		return arguments, nil
	}
	rewritten := cloneOpenAIFileMap(object)
	changed := false
	for fieldName, optionalFields := range openAIFileInputOptionalFields {
		value, exists := object[fieldName]
		if !exists {
			continue
		}
		uploaded, ok, err := r.rewriteValue(ctx, fieldName, value, optionalFields)
		if err != nil {
			return nil, err
		}
		if ok {
			rewritten[fieldName] = uploaded
			changed = true
		}
	}
	if !changed {
		return arguments, nil
	}
	return rewritten, nil
}

func (r *OpenAIFileRewriter) rewriteValue(ctx context.Context, fieldName string, value any, optionalFields []string) (any, bool, error) {
	switch typed := value.(type) {
	case string:
		uploaded, err := r.buildUploadedValue(ctx, fieldName, -1, typed, optionalFields)
		return uploaded, true, err
	case []string:
		values := make([]any, 0, len(typed))
		for index, item := range typed {
			uploaded, err := r.buildUploadedValue(ctx, fieldName, index, item, optionalFields)
			if err != nil {
				return nil, false, err
			}
			values = append(values, uploaded)
		}
		return values, true, nil
	case []any:
		values := make([]any, 0, len(typed))
		for index, item := range typed {
			filePath, ok := item.(string)
			if !ok {
				return nil, false, nil
			}
			uploaded, err := r.buildUploadedValue(ctx, fieldName, index, filePath, optionalFields)
			if err != nil {
				return nil, false, err
			}
			values = append(values, uploaded)
		}
		return values, true, nil
	default:
		return nil, false, nil
	}
}

func (r *OpenAIFileRewriter) buildUploadedValue(ctx context.Context, fieldName string, index int, filePath string, optionalFields []string) (map[string]any, error) {
	if r == nil {
		return nil, fmt.Errorf("failed to upload `%s` for `%s`: rewriter is nil", filePath, fieldName)
	}
	if r.Auth == nil || !r.Auth.ChatGPTBackend {
		return nil, fmt.Errorf("ChatGPT auth is required to upload files for Codex Apps tools")
	}
	cwd := strings.TrimSpace(r.CWD)
	if cwd == "" {
		return nil, r.contextualize(fieldName, index, filePath, "no primary turn environment is available")
	}
	cwdURI, err := openAIFileDirectoryURI(cwd)
	if err != nil {
		return nil, r.contextualize(fieldName, index, filePath, err.Error())
	}
	resolved, err := cwdURI.Join(filePath)
	if err != nil {
		return nil, r.contextualize(fieldName, index, filePath, err.Error())
	}
	fileSystem := r.FileSystem
	if fileSystem == nil {
		fileSystem = localOpenAIFileSystem{}
	}
	info, err := fileSystem.Metadata(ctx, resolved.String())
	if err != nil {
		return nil, r.contextualize(fieldName, index, filePath, err.Error())
	}
	displayPath := resolved.NativePathString()
	if info == nil || !info.IsFile {
		return nil, r.contextualize(fieldName, index, filePath, fmt.Sprintf("path `%s` is not a file", displayPath))
	}
	limit := r.UploadLimitBytes
	if limit <= 0 {
		limit = DefaultOpenAIFileUploadLimitBytes
	}
	if info.Size > limit {
		return nil, r.contextualize(fieldName, index, filePath, fmt.Sprintf("file `%s` is too large: %d bytes exceeds the limit of %d bytes", displayPath, info.Size, limit))
	}
	uploader := r.Uploader
	if uploader == nil {
		uploader = &LocalOpenAIFileUploader{Auth: r.Auth}
	}
	fileName, ok := resolved.Basename()
	if !ok || strings.TrimSpace(fileName) == "" {
		fileName = "file"
	}
	resolvedURI := resolved.String()
	uploaded, err := uploader.UploadOpenAIFile(ctx, OpenAIFileUploadRequest{
		Path:          displayPath,
		FileName:      fileName,
		FileSizeBytes: info.Size,
		Open: func(openCtx context.Context) (io.ReadCloser, error) {
			return fileSystem.Open(openCtx, resolvedURI)
		},
	})
	if err != nil {
		return nil, r.contextualize(fieldName, index, filePath, err.Error())
	}
	if uploaded == nil {
		return nil, r.contextualize(fieldName, index, filePath, "upload returned no file")
	}
	payload := map[string]any{
		"download_url": uploaded.DownloadURL,
		"file_id":      uploaded.FileID,
	}
	if containsOpenAIFileOptionalField(optionalFields, "mime_type") && strings.TrimSpace(uploaded.MimeType) != "" {
		payload["mime_type"] = uploaded.MimeType
	}
	if containsOpenAIFileOptionalField(optionalFields, "file_name") {
		payload["file_name"] = uploaded.FileName
	}
	return payload, nil
}

func openAIFileDirectoryURI(cwd string) (*utils.PathURI, error) {
	cwd = strings.TrimSpace(cwd)
	if parsed, err := utils.Parse(cwd); err == nil {
		if strings.HasSuffix(parsed.EncodedPath(), "/") {
			return parsed, nil
		}
		return utils.Parse(parsed.String() + "/")
	}
	legacy := utils.NewLegacyAppPathString(cwd)
	convention, absolute := legacy.InferAbsolutePathConvention()
	if !absolute {
		hostPath, err := filepath.Abs(cwd)
		if err != nil {
			return nil, err
		}
		return openAIFileDirectoryURI(hostPath)
	}
	separator := "/"
	if convention == utils.ConventionWindows {
		separator = `\`
	}
	if !strings.HasSuffix(cwd, "/") && !strings.HasSuffix(cwd, `\`) {
		cwd += separator
	}
	return utils.FromAbsoluteNativePath(cwd, convention)
}

func containsOpenAIFileOptionalField(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (r *OpenAIFileRewriter) contextualize(fieldName string, index int, filePath string, message string) error {
	if index >= 0 {
		return fmt.Errorf("failed to upload `%s` for `%s[%d]`: %s", filePath, fieldName, index, message)
	}
	return fmt.Errorf("failed to upload `%s` for `%s`: %s", filePath, fieldName, message)
}

func cloneOpenAIFileMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
