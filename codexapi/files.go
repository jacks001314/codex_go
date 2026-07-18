package codexapi

import "fmt"

const (
	OpenAIFileURIPrefix        = "sediment://"
	OpenAIFileUploadLimitBytes = uint64(512 * 1024 * 1024)
)

type UploadedOpenAIFile struct {
	FileID        string  `json:"fileId"`
	URI           string  `json:"uri"`
	DownloadURL   string  `json:"downloadUrl"`
	FileName      string  `json:"fileName"`
	FileSizeBytes uint64  `json:"fileSizeBytes"`
	MimeType      *string `json:"mimeType,omitempty"`
}

type OpenAIFileError struct {
	Kind       string `json:"kind"`
	FileName   string `json:"fileName,omitempty"`
	SizeBytes  uint64 `json:"sizeBytes,omitempty"`
	LimitBytes uint64 `json:"limitBytes,omitempty"`
	FileID     string `json:"fileId,omitempty"`
	Message    string `json:"message,omitempty"`
}

func (e *OpenAIFileError) Error() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case "file_too_large":
		return fmt.Sprintf("file `%s` is too large: %d bytes exceeds the limit of %d bytes", e.FileName, e.SizeBytes, e.LimitBytes)
	case "upload_not_ready":
		return fmt.Sprintf("OpenAI file upload for `%s` is not ready yet", e.FileID)
	case "upload_failed":
		return fmt.Sprintf("OpenAI file upload for `%s` failed: %s", e.FileID, e.Message)
	default:
		return e.Message
	}
}

func OpenAIFileURI(fileID string) string {
	return OpenAIFileURIPrefix + fileID
}

func ValidateOpenAIFileSize(fileName string, sizeBytes uint64) error {
	if sizeBytes > OpenAIFileUploadLimitBytes {
		return &OpenAIFileError{Kind: "file_too_large", FileName: fileName, SizeBytes: sizeBytes, LimitBytes: OpenAIFileUploadLimitBytes}
	}
	return nil
}
