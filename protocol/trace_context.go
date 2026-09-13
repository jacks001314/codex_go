package protocol

// Rust parity: codex_protocol::protocol::W3cTraceContext.

// W3CTraceContext is the W3C trace-context carrier a client may attach to a
// request; it follows the request into the turn and from there into the model
// request, so one trace spans the client, the app server, and the model call.
type W3CTraceContext struct {
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
}
