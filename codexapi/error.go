package codexapi

import (
	"fmt"
	"time"
)

type APIErrorKind string

const (
	ErrorTransport                   APIErrorKind = "transport"
	ErrorAPI                         APIErrorKind = "api"
	ErrorStream                      APIErrorKind = "stream"
	ErrorContextWindowExceeded       APIErrorKind = "contextWindowExceeded"
	ErrorQuotaExceeded               APIErrorKind = "quotaExceeded"
	ErrorUsageNotIncluded            APIErrorKind = "usageNotIncluded"
	ErrorRetryable                   APIErrorKind = "retryable"
	ErrorRateLimit                   APIErrorKind = "rateLimit"
	ErrorInvalidRequest              APIErrorKind = "invalidRequest"
	ErrorCyberPolicy                 APIErrorKind = "cyberPolicy"
	ErrorBioPolicy                   APIErrorKind = "bioPolicy"
	ErrorMisalignmentPolicyViolation APIErrorKind = "misalignmentPolicyViolation"
	ErrorServerOverloaded            APIErrorKind = "serverOverloaded"
	ErrorRateLimitExceeded           APIErrorKind = "rateLimitExceeded"
	// ErrorFlexUnavailable is the terminal Flex-capacity failure Rust surfaces
	// for a `flex_unavailable` error code (Rust #47967).
	ErrorFlexUnavailable APIErrorKind = "flexUnavailable"
)

type APIError struct {
	Kind    APIErrorKind `json:"kind"`
	Status  int          `json:"status,omitempty"`
	Message string       `json:"message,omitempty"`
	// UsageLimitWindowMinutes is the server-selected window responsible for a
	// usage-limit failure, when the response carried one. Rust #48174 preserves
	// it on UsageLimitReachedError and reports it in turn/compaction analytics;
	// other error kinds leave it unset.
	UsageLimitWindowMinutes *uint16 `json:"usageLimitWindowMinutes,omitempty"`
	// The remaining usage-limit evidence mirrors Rust's UsageLimitReachedError
	// fields (#48174). They select the recovery copy Rust renders for the
	// failure and are unset for every other error kind.
	UsageLimitPlanType             *string    `json:"usageLimitPlanType,omitempty"`
	UsageLimitResetsAt             *time.Time `json:"usageLimitResetsAt,omitempty"`
	UsageLimitLimitName            *string    `json:"usageLimitLimitName,omitempty"`
	UsageLimitPromoMessage         *string    `json:"usageLimitPromoMessage,omitempty"`
	UsageLimitRateLimitReachedType *string    `json:"usageLimitRateLimitReachedType,omitempty"`
	// Misalignment carries the optional public explanation and continuation
	// instruction for a misalignment policy block (Rust #40952). It is exposed
	// to live clients but never serialized into rollout storage.
	Misalignment *MisalignmentDetails `json:"-"`
	// Delay is retained for compatibility. New code should use WithRetryDelay
	// and RetryDelay so retry metadata remains independent from error details.
	Delay time.Duration `json:"delay,omitempty"`

	details       *APIErrorDetails
	retryDelay    time.Duration
	hasRetryDelay bool
}

type APIErrorDetails struct {
	Kind                           APIErrorKind
	Status                         int
	Message                        string
	UsageLimitWindowMinutes        *uint16
	UsageLimitPlanType             *string
	UsageLimitResetsAt             *time.Time
	UsageLimitLimitName            *string
	UsageLimitPromoMessage         *string
	UsageLimitRateLimitReachedType *string
}

// MisalignmentDetails mirrors the customer-facing misalignment block details
// supplied by the Responses API (Rust MisalignmentErrorDetails).
type MisalignmentDetails struct {
	ErrorType           *string            `json:"errorType,omitempty"`
	DetailedExplanation *string            `json:"detailedExplanation,omitempty"`
	Steer               *MisalignmentSteer `json:"steer,omitempty"`
}

type MisalignmentSteer struct {
	Message string `json:"message"`
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
		// Rust's CodexErrorDetails::Stream.
		return "stream disconnected before completion: " + e.Message
	case ErrorContextWindowExceeded:
		// Rust's CodexErrorDetails::ContextWindowExceeded. The iOS input-limit
		// classifier matches this message's ASCII prefix.
		return "Codex ran out of room in the model's context window. Start a new thread or clear earlier history before retrying."
	case ErrorQuotaExceeded:
		return "Quota exceeded. Check your plan and billing details."
	case ErrorUsageNotIncluded:
		return "To use Codex with your ChatGPT plan, upgrade to Plus: https://chatgpt.com/explore/plus."
	case ErrorRetryable:
		// Rust maps ApiError::Retryable onto CodexErr::Stream, whose display is
		// the stream copy.
		return "stream disconnected before completion: " + e.Message
	case ErrorRateLimit:
		// Rust's CodexErr::UsageLimitReached displays only the usage-limit copy
		// (`#[error("{0}")]` over UsageLimitReachedError's Display), and the
		// app-server reports that string as the turn error message.
		return e.Message
	case ErrorInvalidRequest:
		// Rust's CodexErrorDetails::InvalidRequest(String) displays the message.
		return e.Message
	case ErrorCyberPolicy:
		return e.Message
	case ErrorBioPolicy:
		return e.Message
	case ErrorMisalignmentPolicyViolation:
		return e.Message
	case ErrorRateLimitExceeded:
		return "rate limit exceeded: " + e.Message
	case ErrorServerOverloaded:
		return "Selected model is at capacity. Please try a different model."
	case ErrorFlexUnavailable:
		// Rust's CodexErr display for CodexErrorDetails::FlexUnavailable.
		return "Flex capacity unavailable."
	default:
		return e.Message
	}
}

func NewAPIErrorWithDetails(details APIErrorDetails) *APIError {
	return &APIError{
		Kind:                           details.Kind,
		Status:                         details.Status,
		Message:                        details.Message,
		UsageLimitWindowMinutes:        details.UsageLimitWindowMinutes,
		UsageLimitPlanType:             details.UsageLimitPlanType,
		UsageLimitResetsAt:             details.UsageLimitResetsAt,
		UsageLimitLimitName:            details.UsageLimitLimitName,
		UsageLimitPromoMessage:         details.UsageLimitPromoMessage,
		UsageLimitRateLimitReachedType: details.UsageLimitRateLimitReachedType,
		details:                        &details,
	}
}

func (e *APIError) Details() APIErrorDetails {
	if e == nil {
		return APIErrorDetails{}
	}
	if e.details != nil {
		return *e.details
	}
	return APIErrorDetails{
		Kind:                           e.Kind,
		Status:                         e.Status,
		Message:                        e.Message,
		UsageLimitWindowMinutes:        e.UsageLimitWindowMinutes,
		UsageLimitPlanType:             e.UsageLimitPlanType,
		UsageLimitResetsAt:             e.UsageLimitResetsAt,
		UsageLimitLimitName:            e.UsageLimitLimitName,
		UsageLimitPromoMessage:         e.UsageLimitPromoMessage,
		UsageLimitRateLimitReachedType: e.UsageLimitRateLimitReachedType,
	}
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
