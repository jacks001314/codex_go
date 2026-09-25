package network

// Transport-level application-policy enforcement.
//
// PolicyHTTPDoer covers consumers that only need `Do`; some transports (the
// realtime WebSocket dial, for example) need the concrete `*http.Client` so the
// client's own transport carries request setup. Rust binds its policy to the
// client's request execution in one place, so Go provides the same semantics at
// both seams: the destination is checked before the request is issued and the
// permit stays valid for the response body.

import (
	"net/http"
)

// PolicyRoundTripper enforces an application network policy for one request and
// its response body.
type PolicyRoundTripper struct {
	Policy NetworkPolicy
	Next   http.RoundTripper
}

func (t *PolicyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	next := t.Next
	if next == nil {
		next = http.DefaultTransport
	}
	if request == nil || !t.Policy.IsScoped() {
		return next.RoundTrip(request)
	}
	permit, err := t.Policy.Acquire(request.URL)
	if err != nil {
		return nil, &PolicyError{Err: err}
	}
	response, err := next.RoundTrip(request)
	if err != nil {
		permit.Release()
		return nil, err
	}
	if response == nil {
		permit.Release()
		return nil, nil
	}
	if response.Body != nil {
		response.Body = newPermitBody(permit, response.Body)
	} else {
		permit.Release()
	}
	return response, nil
}

// PolicyHTTPClient returns a client whose transport enforces the policy. An
// unscoped policy returns the client unchanged, so callers that share a client
// keep sharing it.
func PolicyHTTPClient(policy NetworkPolicy, client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	if !policy.IsScoped() {
		return client
	}
	clone := *client
	transport := clone.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clone.Transport = &PolicyRoundTripper{Policy: policy, Next: transport}
	return &clone
}

var _ http.RoundTripper = (*PolicyRoundTripper)(nil)
