package network

import (
	"encoding/json"
	"net/http"
)

type ProxyPolicyDecisionDetails struct {
	Decision ProxyPolicyDecision `json:"decision"`
	Reason   string              `json:"reason"`
	Source   ProxyDecisionSource `json:"source"`
	Protocol ProxyProtocol       `json:"protocol"`
	Host     string              `json:"host"`
	Port     uint16              `json:"port"`
}

type ProxySimpleResponse struct {
	Status  int
	Headers map[string]string
	Body    string
}

func ProxyTextResponse(status int, body string) ProxySimpleResponse {
	return ProxySimpleResponse{
		Status:  status,
		Headers: map[string]string{"content-type": "text/plain"},
		Body:    body,
	}
}

func ProxyJSONResponse(value any) ProxySimpleResponse {
	body, err := json.Marshal(value)
	if err != nil {
		body = []byte("{}")
	}
	return ProxySimpleResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"content-type": "application/json"},
		Body:    string(body),
	}
}

func ProxyBlockedHeaderValue(reason string) string {
	switch reason {
	case ProxyReasonNotAllowed, ProxyReasonNotAllowedLocal:
		return "blocked-by-allowlist"
	case ProxyReasonDenied:
		return "blocked-by-denylist"
	case ProxyReasonMethodNotAllowed:
		return "blocked-by-method-policy"
	case ProxyReasonMITMHookDenied:
		return "blocked-by-mitm-hook"
	case ProxyReasonMITMRequired:
		return "blocked-by-mitm-required"
	default:
		return "blocked-by-policy"
	}
}

func ProxyBlockedMessage(reason string) string {
	switch reason {
	case ProxyReasonNotAllowed:
		return "Domain not in allowlist."
	case ProxyReasonNotAllowedLocal:
		return "Sandbox policy blocks local/private network addresses."
	case ProxyReasonDenied:
		return "Domain denied by the sandbox policy."
	case ProxyReasonMethodNotAllowed:
		return "Method not allowed in limited mode."
	case ProxyReasonMITMHookDenied:
		return "HTTPS request denied by MITM hook policy."
	case ProxyReasonMITMRequired:
		return "MITM required for limited HTTPS."
	case ProxyReasonProxyDisabled:
		return "network proxy is disabled"
	default:
		return "Request blocked by network policy."
	}
}

func ProxyBlockedTextResponse(reason string) ProxySimpleResponse {
	response := ProxyTextResponse(http.StatusForbidden, ProxyBlockedMessage(reason))
	response.Headers["x-proxy-error"] = ProxyBlockedHeaderValue(reason)
	return response
}

func ProxyBlockedMessageWithPolicy(reason string, details ProxyPolicyDecisionDetails) string {
	_ = details
	return ProxyBlockedMessage(reason)
}

func ProxyBlockedTextResponseWithPolicy(reason string, details ProxyPolicyDecisionDetails) ProxySimpleResponse {
	response := ProxyTextResponse(http.StatusForbidden, ProxyBlockedMessageWithPolicy(reason, details))
	response.Headers["x-proxy-error"] = ProxyBlockedHeaderValue(reason)
	return response
}
