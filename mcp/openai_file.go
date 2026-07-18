package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultOpenAIFileUploadLimitBytes int64 = 512 * 1024 * 1024

type OpenAIFileAuth struct {
	ChatGPTBackend bool
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
}

type OpenAIFileUploader interface {
	UploadOpenAIFile(ctx context.Context, request OpenAIFileUploadRequest) (*OpenAIUploadedFile, error)
}

type LocalOpenAIFileUploader struct{}

func (u *LocalOpenAIFileUploader) UploadOpenAIFile(ctx context.Context, request OpenAIFileUploadRequest) (*OpenAIUploadedFile, error) {
	_ = ctx
	fileID := "file-" + request.FileName
	return &OpenAIUploadedFile{
		FileID:        fileID,
		FileName:      request.FileName,
		URI:           "sediment://" + fileID,
		FileSizeBytes: request.FileSizeBytes,
	}, nil
}

type OpenAIFileRewriter struct {
	CWD              string
	Auth             *OpenAIFileAuth
	Uploader         OpenAIFileUploader
	UploadLimitBytes int64
}

func NewOpenAIFileRewriter(cwd string, auth *OpenAIFileAuth, uploader OpenAIFileUploader) *OpenAIFileRewriter {
	if uploader == nil {
		uploader = &LocalOpenAIFileUploader{}
	}
	return &OpenAIFileRewriter{
		CWD:              cwd,
		Auth:             auth,
		Uploader:         uploader,
		UploadLimitBytes: DefaultOpenAIFileUploadLimitBytes,
	}
}

func (r *OpenAIFileRewriter) RewriteArguments(ctx context.Context, arguments any, openAIFileInputParams []string) (any, error) {
	if len(openAIFileInputParams) == 0 || arguments == nil {
		return arguments, nil
	}
	object, ok := arguments.(map[string]any)
	if !ok {
		return arguments, nil
	}
	rewritten := cloneOpenAIFileMap(object)
	changed := false
	for _, fieldName := range openAIFileInputParams {
		value, exists := object[fieldName]
		if !exists {
			continue
		}
		uploaded, ok, err := r.rewriteValue(ctx, fieldName, value)
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

func (r *OpenAIFileRewriter) rewriteValue(ctx context.Context, fieldName string, value any) (any, bool, error) {
	switch typed := value.(type) {
	case string:
		uploaded, err := r.buildUploadedValue(ctx, fieldName, -1, typed)
		return uploaded, true, err
	case []string:
		values := make([]any, 0, len(typed))
		for index, item := range typed {
			uploaded, err := r.buildUploadedValue(ctx, fieldName, index, item)
			if err != nil {
				return nil, false, err
			}
			values = append(values, uploaded)
		}
		return values, true, nil
	case []any:
		values := make([]any, 0, len(typed))
		for index, item := range typed {
			uploaded, ok, err := r.rewriteArrayItem(ctx, fieldName, index, item)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return nil, false, nil
			}
			values = append(values, uploaded)
		}
		return values, true, nil
	default:
		return nil, false, nil
	}
}

func (r *OpenAIFileRewriter) rewriteArrayItem(ctx context.Context, fieldName string, index int, item any) (any, bool, error) {
	switch typed := item.(type) {
	case string:
		uploaded, err := r.buildUploadedValue(ctx, fieldName, index, typed)
		return uploaded, true, err
	case map[string]any:
		for _, key := range []string{"path", "file", "file_path"} {
			value, ok := typed[key].(string)
			if !ok || strings.TrimSpace(value) == "" {
				continue
			}
			uploaded, err := r.buildUploadedValue(ctx, fieldName, index, value)
			if err != nil {
				return nil, false, err
			}
			rewritten := cloneOpenAIFileMap(typed)
			rewritten[key] = uploaded
			return rewritten, true, nil
		}
	}
	return nil, false, nil
}

func (r *OpenAIFileRewriter) buildUploadedValue(ctx context.Context, fieldName string, index int, filePath string) (*OpenAIUploadedFile, error) {
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
	resolved := filePath
	if !filepath.IsAbs(filePath) {
		resolved = filepath.Join(cwd, filePath)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, r.contextualize(fieldName, index, filePath, err.Error())
	}
	if !info.Mode().IsRegular() {
		return nil, r.contextualize(fieldName, index, filePath, fmt.Sprintf("path `%s` is not a file", resolved))
	}
	limit := r.UploadLimitBytes
	if limit <= 0 {
		limit = DefaultOpenAIFileUploadLimitBytes
	}
	if info.Size() > limit {
		return nil, r.contextualize(fieldName, index, filePath, fmt.Sprintf("file `%s` is too large: %d bytes exceeds the limit of %d bytes", resolved, info.Size(), limit))
	}
	uploader := r.Uploader
	if uploader == nil {
		uploader = &LocalOpenAIFileUploader{}
	}
	return uploader.UploadOpenAIFile(ctx, OpenAIFileUploadRequest{
		Path:          resolved,
		FileName:      filepath.Base(resolved),
		FileSizeBytes: info.Size(),
	})
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
