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
	Kind    APIErrorKind `json:"kind"`
	Status  int          `json:"status,omitempty"`
	Message string       `json:"message,omitempty"`
	// Delay is retained for compatibility. New code should use WithRetryDelay
	// and RetryDelay so retry metadata remains independent from error details.
	Delay time.Duration `json:"delay,omitempty"`

	details       *APIErrorDetails
	retryDelay    time.Duration
	hasRetryDelay bool
}

type APIErrorDetails struct {
	Kind    APIErrorKind
	Status  int
	Message string
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

func NewAPIErrorWithDetails(details APIErrorDetails) *APIError {
	return &APIError{
		Kind:    details.Kind,
		Status:  details.Status,
		Message: details.Message,
		details: &details,
	}
}

func (e *APIError) Details() APIErrorDetails {
	if e == nil {
		return APIErrorDetails{}
	}
	if e.details != nil {
		return *e.details
	}
	return APIErrorDetails{Kind: e.Kind, Status: e.Status, Message: e.Message}
}

func (e *APIError) WithRetryDelay(delay time.Duration) *APIError {
	if e != nil && delay >= 0 {
		e.retryDelay = delay
		e.hasRetryDelay = true
	}
	return e
}

func (e *APIError) RequestedRetryDelay() (time.Duration, bool) {
	if e == nil {
		return 0, false
	}
	if e.hasRetryDelay {
		return e.retryDelay, true
	}
	return e.Delay, e.Delay > 0
}

type retryDelayProvider interface {
	RequestedRetryDelay() (time.Duration, bool)
}

func RetryDelayInfo(err error) (time.Duration, bool) {
	for err != nil {
		if provider, ok := err.(retryDelayProvider); ok {
			return provider.RequestedRetryDelay()
		}
		type unwrapper interface{ Unwrap() error }
		wrapped, ok := err.(unwrapper)
		if !ok {
			return 0, false
		}
		err = wrapped.Unwrap()
	}
	return 0, false
}

func RetryDelay(err error) time.Duration {
	delay, _ := RetryDelayInfo(err)
	return delay
}

func NewAPIError(status int, message string) *APIError {
	return NewAPIErrorWithDetails(APIErrorDetails{Kind: ErrorAPI, Status: status, Message: message})
}

func NewRateLimitError(message string) *APIError {
	return NewAPIErrorWithDetails(APIErrorDetails{Kind: ErrorRateLimit, Message: message})
}
