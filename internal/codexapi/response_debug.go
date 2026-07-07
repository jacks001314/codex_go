package codexapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	RequestIDHeader    = "x-request-id"
	OAIRequestIDHeader = "x-oai-request-id"
	CFRayHeader        = "cf-ray"
	AuthErrorHeader    = "x-openai-authorization-error"
	XErrorJSONHeader   = "x-error-json"
)

type ResponseDebugContext struct {
	RequestID     *string `json:"requestId,omitempty"`
	CFRay         *string `json:"cfRay,omitempty"`
	AuthError     *string `json:"authError,omitempty"`
	AuthErrorCode *string `json:"authErrorCode,omitempty"`
}

type ResponseTransportErrorKind string

const (
	ResponseTransportHTTP       ResponseTransportErrorKind = "http"
	ResponseTransportRetryLimit ResponseTransportErrorKind = "retryLimit"
	ResponseTransportTimeout    ResponseTransportErrorKind = "timeout"
	ResponseTransportNetwork    ResponseTransportErrorKind = "network"
	ResponseTransportBuild      ResponseTransportErrorKind = "build"
)

type ResponseTransportError struct {
	Kind    ResponseTransportErrorKind `json:"kind"`
	Status  int                        `json:"status,omitempty"`
	Headers http.Header                `json:"headers,omitempty"`
	Message string                     `json:"message,omitempty"`
}

func ExtractResponseDebugContext(transport *ResponseTransportError) ResponseDebugContext {
	var context ResponseDebugContext
	if transport == nil || transport.Kind != ResponseTransportHTTP {
		return context
	}
	context.RequestID = firstHeader(transport.Headers, RequestIDHeader, OAIRequestIDHeader)
	context.CFRay = firstHeader(transport.Headers, CFRayHeader)
	context.AuthError = firstHeader(transport.Headers, AuthErrorHeader)
	if encoded := firstHeader(transport.Headers, XErrorJSONHeader); encoded != nil {
		context.AuthErrorCode = decodeAuthErrorCode(*encoded)
	}
	return context
}

func TelemetryResponseTransportErrorMessage(err *ResponseTransportError) string {
	if err == nil {
		return ""
	}
	switch err.Kind {
	case ResponseTransportHTTP:
		return fmt.Sprintf("http %d", err.Status)
	case ResponseTransportRetryLimit:
		return "retry limit reached"
	case ResponseTransportTimeout:
		return "timeout"
	case ResponseTransportNetwork, ResponseTransportBuild:
		return err.Message
	default:
		return err.Message
	}
}

type ResponseAPIErrorKind string

const (
	ResponseAPITransport        ResponseAPIErrorKind = "transport"
	ResponseAPIRemote           ResponseAPIErrorKind = "api"
	ResponseAPIStream           ResponseAPIErrorKind = "stream"
	ResponseAPIContextWindow    ResponseAPIErrorKind = "contextWindowExceeded"
	ResponseAPIQuotaExceeded    ResponseAPIErrorKind = "quotaExceeded"
	ResponseAPIUsageNotIncluded ResponseAPIErrorKind = "usageNotIncluded"
	ResponseAPIRetryable        ResponseAPIErrorKind = "retryable"
	ResponseAPIRateLimit        ResponseAPIErrorKind = "rateLimit"
	ResponseAPIInvalidRequest   ResponseAPIErrorKind = "invalidRequest"
	ResponseAPICyberPolicy      ResponseAPIErrorKind = "cyberPolicy"
	ResponseAPIServerOverloaded ResponseAPIErrorKind = "serverOverloaded"
)

type ResponseAPIError struct {
	Kind      ResponseAPIErrorKind    `json:"kind"`
	Status    int                     `json:"status,omitempty"`
	Message   string                  `json:"message,omitempty"`
	Transport *ResponseTransportError `json:"transport,omitempty"`
}

func ExtractResponseDebugContextFromAPIError(err *ResponseAPIError) ResponseDebugContext {
	if err == nil || err.Kind != ResponseAPITransport {
		return ResponseDebugContext{}
	}
	return ExtractResponseDebugContext(err.Transport)
}

func TelemetryResponseAPIErrorMessage(err *ResponseAPIError) string {
	if err == nil {
		return ""
	}
	switch err.Kind {
	case ResponseAPITransport:
		return TelemetryResponseTransportErrorMessage(err.Transport)
	case ResponseAPIRemote:
		return fmt.Sprintf("api error %d", err.Status)
	case ResponseAPIStream:
		return err.Message
	case ResponseAPIContextWindow:
		return "context window exceeded"
	case ResponseAPIQuotaExceeded:
		return "quota exceeded"
	case ResponseAPIUsageNotIncluded:
		return "usage not included"
	case ResponseAPIRetryable:
		return "retryable error"
	case ResponseAPIRateLimit:
		return "rate limit"
	case ResponseAPIInvalidRequest:
		return "invalid request"
	case ResponseAPICyberPolicy:
		return "cyber policy"
	case ResponseAPIServerOverloaded:
		return "server overloaded"
	default:
		return err.Message
	}
}

func firstHeader(headers http.Header, names ...string) *string {
	for _, name := range names {
		value := headers.Get(name)
		if strings.TrimSpace(value) != "" {
			return &value
		}
	}
	return nil
}

func decodeAuthErrorCode(encoded string) *string {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil
	}
	errorPayload, _ := payload["error"].(map[string]any)
	code, _ := errorPayload["code"].(string)
	if code == "" {
		return nil
	}
	return &code
}
