package codexapi

import (
	"fmt"
	"time"
)

type APIErrorKind string

const (
	ErrorTransport             APIErrorKind = "transport"
	ErrorAPI                   APIErrorKind = "api"
	ErrorStream                APIErrorKind = "stream"
	ErrorContextWindowExceeded APIErrorKind = "contextWindowExceeded"
	ErrorQuotaExceeded         APIErrorKind = "quotaExceeded"
	ErrorUsageNotIncluded      APIErrorKind = "usageNotIncluded"
	ErrorRetryable             APIErrorKind = "retryable"
	ErrorRateLimit             APIErrorKind = "rateLimit"
	ErrorInvalidRequest        APIErrorKind = "invalidRequest"
	ErrorCyberPolicy           APIErrorKind = "cyberPolicy"
	ErrorServerOverloaded      APIErrorKind = "serverOverloaded"
)

type APIError struct {
	Kind    APIErrorKind  `json:"kind"`
	Status  int           `json:"status,omitempty"`
	Message string        `json:"message,omitempty"`
	Delay   time.Duration `json:"delay,omitempty"`
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ErrorTransport:
		return e.Message
	case ErrorAPI:
		return fmt.Sprintf("api error %d: %s", e.Status, e.Message)
	case ErrorStream:
		return "stream error: " + e.Message
	case ErrorContextWindowExceeded:
		return "context window exceeded"
	case ErrorQuotaExceeded:
		return "quota exceeded"
	case ErrorUsageNotIncluded:
		return "usage not included"
	case ErrorRetryable:
		return "retryable error: " + e.Message
	case ErrorRateLimit:
		return "rate limit: " + e.Message
	case ErrorInvalidRequest:
		return "invalid request: " + e.Message
	case ErrorCyberPolicy:
		return "cyber policy: " + e.Message
	case ErrorServerOverloaded:
		return "server overloaded"
	default:
		return e.Message
	}
}

func NewAPIError(status int, message string) *APIError {
	return &APIError{Kind: ErrorAPI, Status: status, Message: message}
}

func NewRateLimitError(message string) *APIError {
	return &APIError{Kind: ErrorRateLimit, Message: message}
}
