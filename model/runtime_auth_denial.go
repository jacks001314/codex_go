package model

import (
	"encoding/json"
	"net/http"
	"strings"
)

// runtimeAuthDenialCodes are the terminal runtime-credential denials (Rust
// #60096, protocol/src/error.rs is_runtime_auth_denial): an invalid assertion and
// a paused or revoked runtime instance cannot be repaired by retrying the
// request or by refreshing the original credential.
var runtimeAuthDenialCodes = map[string]struct{}{
	"runtime_assertion_invalid":        {},
	"runtime_instance_paused":          {},
	"runtime_instance_revoked":         {},
	"invalid_agent_instance_assertion": {},
}

// isRuntimeAuthDenial reports whether a response is a terminal runtime denial.
// Only 401 and 403 carry one, and the code comes from the identity error header
// first and the body's `/error/code` second, exactly like Rust.
func isRuntimeAuthDenial(status int, body []byte, identityErrorCode string) bool {
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return false
	}
	if runtimeAuthDenialCode(identityErrorCode) {
		return true
	}
	if len(body) == 0 {
		return false
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	return runtimeAuthDenialCode(payload.Error.Code)
}

func runtimeAuthDenialCode(code string) bool {
	_, ok := runtimeAuthDenialCodes[strings.TrimSpace(code)]
	return ok
}

// isRecoverableAuthError mirrors ConfiguredModelProvider::is_recoverable_auth_error
// (Rust #60096): a 401 is recoverable unless the request itself presented runtime
// credentials and the response is a terminal runtime denial. A request that did
// not use runtime auth keeps the ordinary credential-expiry recovery.
//
// An unset status keeps the previous behavior: callers that do not record the
// response status must not change how recovery behaves.
func (r *ResponsesAgentRunner) isRecoverableAuthError(status int, body string, identityErrorCode string) bool {
	if status == 0 {
		return true
	}
	if status != http.StatusUnauthorized {
		return false
	}
	if r == nil || !r.RuntimeAuthActive {
		return true
	}
	return !isRuntimeAuthDenial(status, []byte(body), identityErrorCode)
}
