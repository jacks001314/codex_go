package network

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Rust parity: codex-rs/http-client/src/network_policy.rs.
//
// Application destination checks and revocation of outstanding network
// operations. The configuration owner publishes an already-composed policy;
// transports retain only its read side plus a permit for each request, so an
// invalidation can never leave an old request authorized.

// Deterministic denials, distinct from retryable network failures (Rust
// NetworkPolicyDenied).
var (
	ErrNetworkPolicyUnavailable          = errors.New("application network policy is unavailable")
	ErrNetworkPolicyDestination          = errors.New("destination denied by application network policy")
	ErrNetworkPolicyRevoked              = errors.New("application network permission was revoked")
	ErrNetworkPolicyUnsupportedTransport = errors.New("this SDK transport is disabled by application network restrictions")
)

// DestinationPolicy is the effective set of application destinations after the
// requirements owner has applied precedence.
type DestinationPolicy struct {
	restricted   bool
	allowedHosts []string
	lookup       map[string]struct{}
}

// UnrestrictedDestinationPolicy permits every destination.
func UnrestrictedDestinationPolicy() DestinationPolicy { return DestinationPolicy{} }

// RestrictedDestinationPolicy permits only HTTPS and WSS requests to these
// exact, normalized hosts (Rust DestinationPolicy::Restricted).
func RestrictedDestinationPolicy(allowedHosts []string) DestinationPolicy {
	lookup := make(map[string]struct{}, len(allowedHosts))
	hosts := make([]string, 0, len(allowedHosts))
	for _, host := range allowedHosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized == "" {
			continue
		}
		if _, ok := lookup[normalized]; ok {
			continue
		}
		lookup[normalized] = struct{}{}
		hosts = append(hosts, normalized)
	}
	sort.Strings(hosts)
	return DestinationPolicy{restricted: true, allowedHosts: hosts, lookup: lookup}
}

// IsRestricted reports whether this policy limits destinations.
func (p DestinationPolicy) IsRestricted() bool { return p.restricted }

// AllowedHosts returns the normalized allowed hosts (sorted).
func (p DestinationPolicy) AllowedHosts() []string {
	return append([]string(nil), p.allowedHosts...)
}

// Allows mirrors Rust DestinationPolicy::allows: restricted policies permit only
// https/wss requests to an exact host, ignoring a single trailing dot.
func (p DestinationPolicy) Allows(target *url.URL) bool {
	if !p.restricted {
		return true
	}
	if target == nil {
		return false
	}
	switch strings.ToLower(target.Scheme) {
	case "https", "wss":
	default:
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	if host == "" {
		return false
	}
	_, ok := p.lookup[host]
	return ok
}

// Equal reports whether two policies are identical.
func (p DestinationPolicy) Equal(other DestinationPolicy) bool {
	if p.restricted != other.restricted || len(p.allowedHosts) != len(other.allowedHosts) {
		return false
	}
	for i := range p.allowedHosts {
		if p.allowedHosts[i] != other.allowedHosts[i] {
			return false
		}
	}
	return true
}

// NetworkPolicyRevision identifies the account/configuration generation a load
// is allowed to publish into (Rust NetworkPolicyRevision).
type NetworkPolicyRevision struct {
	value uint64
}

// NetworkPolicy is read access to the application policy. Copies observe the
// same updates.
type NetworkPolicy struct {
	state     *networkPolicyState
	endpoints []*url.URL
	account   *uint64
}

// UnmanagedNetworkPolicy is the policy of a transport with no application
// policy owner.
func UnmanagedNetworkPolicy() NetworkPolicy { return NetworkPolicy{} }

// IsManaged reports whether this transport participates in the application
// policy lifecycle.
func (p NetworkPolicy) IsManaged() bool { return p.state != nil }

// ForCurrentAccount binds a content client to the current account so retained
// credentials cannot be used after another workspace's policy was installed.
func (p NetworkPolicy) ForCurrentAccount() NetworkPolicy {
	if p.state == nil {
		return p
	}
	p.state.mu.Lock()
	account := p.state.account
	p.state.mu.Unlock()
	p.account = &account
	return p
}

// Changes returns the channel closed on the next effective policy change;
// unmanaged policies have no owner. Callers re-read Changes for the next change.
func (p NetworkPolicy) Changes() <-chan struct{} {
	if p.state == nil {
		return nil
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	return p.state.changes
}

// Revision returns the current publication revision.
func (p NetworkPolicy) Revision() NetworkPolicyRevision {
	if p.state == nil {
		return NetworkPolicyRevision{}
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	return NetworkPolicyRevision{value: p.state.revision}
}

// Invalidate revokes outstanding operations before an account change or a
// failed requirements load. It only removes access; publication stays with the
// controller.
func (p NetworkPolicy) Invalidate() {
	if p.state == nil {
		return
	}
	state := p.state
	state.mu.Lock()
	defer state.mu.Unlock()
	state.revision++
	state.account++
	state.policy = nil
	state.notifyLocked()
	state.revokeAllLocked()
}

// Acquire checks a destination before proxy resolution, DNS, or transport work
// starts.
func (p NetworkPolicy) Acquire(target *url.URL) (*NetworkPermit, error) {
	if p.endpoints != nil && !containsEndpoint(p.endpoints, target) {
		return nil, ErrNetworkPolicyDestination
	}
	return p.acquireDestination(networkPermitDestination{target: target})
}

// AcquireForUnsupportedSDK disables SDKs without destination enforcement
// whenever restrictions apply.
func (p NetworkPolicy) AcquireForUnsupportedSDK() (*NetworkPermit, error) {
	if p.endpoints != nil {
		return nil, ErrNetworkPolicyUnsupportedTransport
	}
	return p.acquireDestination(networkPermitDestination{unrestrictedSDK: true})
}

func (p NetworkPolicy) acquireDestination(destination networkPermitDestination) (*NetworkPermit, error) {
	if p.state == nil {
		return &NetworkPermit{revoked: make(chan struct{})}, nil
	}
	state := p.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if p.account != nil && *p.account != state.account {
		return nil, ErrNetworkPolicyRevoked
	}
	if state.policy == nil {
		return nil, ErrNetworkPolicyUnavailable
	}
	if !destination.allowedBy(state.policy) {
		if destination.unrestrictedSDK {
			return nil, ErrNetworkPolicyUnsupportedTransport
		}
		return nil, ErrNetworkPolicyDestination
	}
	permit := &NetworkPermit{state: state, destination: destination, revoked: make(chan struct{})}
	state.permits = append(state.pruneReleasedLocked(), permit)
	return permit, nil
}

// NetworkPolicyController is the publication access retained by the
// account/configuration owner.
type NetworkPolicyController struct {
	state *networkPolicyState
}

// NewNetworkPolicyController creates a controller whose policy starts
// unavailable.
func NewNetworkPolicyController() *NetworkPolicyController {
	return &NetworkPolicyController{state: &networkPolicyState{changes: make(chan struct{})}}
}

// Policy returns the read handle transports bind to.
func (c *NetworkPolicyController) Policy() NetworkPolicy {
	return NetworkPolicy{state: c.state}
}

// Unavailable fails a requirements load without changing the current account
// identity.
func (c *NetworkPolicyController) Unavailable(revision NetworkPolicyRevision) {
	state := c.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.revision != revision.value {
		return
	}
	if state.policy != nil {
		state.policy = nil
		state.notifyLocked()
	}
	state.revokeAllLocked()
}

// Publish installs a successful load only when its account/configuration
// generation is still current, revoking permits the new policy no longer allows.
func (c *NetworkPolicyController) Publish(revision NetworkPolicyRevision, policy DestinationPolicy) bool {
	state := c.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.revision != revision.value {
		return false
	}
	kept := state.pruneReleasedLocked()
	for _, permit := range kept {
		if !permit.destination.allowedBy(&policy) {
			permit.markRevoked()
			continue
		}
	}
	state.permits = nil
	for _, permit := range kept {
		if permit.isRevoked() {
			continue
		}
		state.permits = append(state.permits, permit)
	}
	if state.policy == nil || !state.policy.Equal(policy) {
		next := policy
		state.policy = &next
		state.notifyLocked()
	}
	return true
}

// networkPermitDestination is the destination a permit authorizes.
type networkPermitDestination struct {
	target          *url.URL
	unrestrictedSDK bool
}

func (d networkPermitDestination) allowedBy(policy *DestinationPolicy) bool {
	if policy == nil {
		return false
	}
	if d.unrestrictedSDK {
		return !policy.restricted
	}
	return policy.Allows(d.target)
}

// NetworkPermit is the authorization retained for the entire operation,
// including response or WebSocket streaming.
type NetworkPermit struct {
	state       *networkPolicyState
	destination networkPermitDestination
	revoked     chan struct{}

	mu         sync.Mutex
	revokedSet bool
	released   bool
}

// Check reports whether the permit is still authorized.
func (p *NetworkPermit) Check() error {
	if p == nil || p.isRevoked() {
		return ErrNetworkPolicyRevoked
	}
	return nil
}

// Revoked returns a channel closed when the permit is revoked. An unmanaged
// permit never closes it.
func (p *NetworkPermit) Revoked() <-chan struct{} {
	if p == nil {
		channel := make(chan struct{})
		return channel
	}
	return p.revoked
}

// Release drops the permit from its controller's tracking list. Go has no weak
// references, so a transport that abandons a permit without revoking it must
// release it to keep the tracking list bounded (Rust relies on Drop).
func (p *NetworkPermit) Release() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.released = true
	p.mu.Unlock()
}

func (p *NetworkPermit) markRevoked() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.revokedSet {
		return
	}
	p.revokedSet = true
	close(p.revoked)
}

func (p *NetworkPermit) isRevoked() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.revokedSet
}

func (p *NetworkPermit) isReleased() bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.released
}

// RunWithNetworkPermit runs an operation under a permit, returning the
// revocation denial when the permit is revoked first (Rust NetworkPermit::run).
func RunWithNetworkPermit[T any](ctx context.Context, permit *NetworkPermit, operation func(context.Context) T) (T, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	var value T
	go func() {
		defer close(done)
		value = operation(ctx)
	}()
	select {
	case <-permit.Revoked():
		return zero, ErrNetworkPolicyRevoked
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-done:
		return value, nil
	}
}

// networkPolicyState is the shared policy state.
type networkPolicyState struct {
	mu       sync.Mutex
	policy   *DestinationPolicy
	revision uint64
	account  uint64
	permits  []*NetworkPermit
	changes  chan struct{}
}

func (s *networkPolicyState) notifyLocked() {
	close(s.changes)
	s.changes = make(chan struct{})
}

func (s *networkPolicyState) revokeAllLocked() {
	for _, permit := range s.permits {
		permit.markRevoked()
	}
	s.permits = nil
}

func (s *networkPolicyState) pruneReleasedLocked() []*NetworkPermit {
	kept := make([]*NetworkPermit, 0, len(s.permits))
	for _, permit := range s.permits {
		if permit.isReleased() {
			continue
		}
		kept = append(kept, permit)
	}
	s.permits = kept
	return kept
}

func containsEndpoint(endpoints []*url.URL, target *url.URL) bool {
	if target == nil {
		return false
	}
	for _, endpoint := range endpoints {
		if endpoint != nil && endpoint.String() == target.String() {
			return true
		}
	}
	return false
}
