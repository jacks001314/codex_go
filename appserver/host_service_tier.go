package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// ServiceTierRead is a host's asynchronous current-tier answer. A nil Tier
// restores the default; Err is a read failure, never an acceptance rejection.
type ServiceTierRead struct {
	Tier *string
	Err  error
}

// AgentServiceTier is installed by an embedding host before startup. Methods
// must hand off quickly without blocking or reentering the router. Current may
// perform I/O independently and must respect ctx. Update must preserve handoff
// order and drive propagation independently of callers waiting on its receipt.
// Current may return the prior tier until propagation reaches this runtime.
type AgentServiceTier interface {
	Current(context.Context) <-chan ServiceTierRead
	Update(*string) *ServiceTierAcceptance
}

// ErrServiceTierRejected marks an explicit host rejection. Other receipt
// errors leave acceptance unconfirmed (for example a host transport failure).
var ErrServiceTierRejected = errors.New("host rejected service tier")

// ServiceTierAcceptance is a shared, repeatable result. Cancelling a wait does
// not cancel host propagation. Create with NewServiceTierAcceptance.
// Resolve is safe for concurrent/duplicate calls.
type ServiceTierAcceptance struct {
	once sync.Once
	done chan struct{}
	err  error
}

func NewServiceTierAcceptance() *ServiceTierAcceptance {
	return &ServiceTierAcceptance{done: make(chan struct{})}
}

func (a *ServiceTierAcceptance) Resolve(err error) {
	a.once.Do(func() { a.err = err; close(a.done) })
}

func (a *ServiceTierAcceptance) result() (error, bool) {
	if a == nil || a.done == nil {
		return errors.New("missing host acceptance receipt"), true
	}
	select {
	case <-a.done:
		return a.err, true
	default:
		return nil, false
	}
}

func (a *ServiceTierAcceptance) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err, done := a.result(); done {
		return err
	}
	select {
	case <-a.done:
		return a.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ServiceTierHostError distinguishes read/accept failures and preserves
// errors.Is for rejection, timeout and cancellation.
type ServiceTierHostError struct {
	Operation string
	Err       error
}

func (e *ServiceTierHostError) Error() string {
	return fmt.Sprintf("host service tier %s: %v", e.Operation, e.Err)
}
func (e *ServiceTierHostError) Unwrap() error { return e.Err }

type hostServiceTierCommit struct {
	tier    string
	receipt *ServiceTierAcceptance
}
type hostServiceTierState struct {
	mu      sync.Mutex
	commits map[string]hostServiceTierCommit
}

func (r *RuntimeRouter) hostTierContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := r.services.ServiceTierTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func hostTierValue(tier *string) string {
	if tier == nil || strings.TrimSpace(*tier) == "" || *tier == model.ServiceTierDefaultRequestValue {
		return model.ServiceTierDefaultRequestValue
	}
	return model.NormalizeServiceTierRequestValue(*tier)
}
func hostTierPointer(value string) *string {
	if value == "" || value == model.ServiceTierDefaultRequestValue {
		return nil
	}
	return cloneString(&value)
}

func (r *RuntimeRouter) readHostServiceTier(ctx context.Context) (string, error) {
	ctx, cancel := r.hostTierContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", &ServiceTierHostError{"read", err}
	}
	answer := r.services.ServiceTier.Current(ctx)
	select {
	case <-ctx.Done():
		return "", &ServiceTierHostError{"read", ctx.Err()}
	case result, ok := <-answer:
		if !ok {
			return "", &ServiceTierHostError{"read", errors.New("host closed current-tier answer without a result")}
		}
		if result.Err != nil {
			return "", &ServiceTierHostError{"read", result.Err}
		}
		return hostTierValue(result.Tier), nil
	}
}

func (r *RuntimeRouter) waitHostServiceTier(ctx context.Context, receipt *ServiceTierAcceptance) error {
	if receipt == nil {
		return nil
	}
	ctx, cancel := r.hostTierContext(ctx)
	defer cancel()
	if err := receipt.Wait(ctx); err != nil {
		return &ServiceTierHostError{"accept", err}
	}
	return nil
}

// A detached host child may have no parent edge. Session identity, rather than
// the structural tree alone, determines who can publish shared settings.
func (r *RuntimeRouter) hostTierChild(threadID string) bool {
	if r == nil || r.services.ServiceTier == nil {
		return false
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	return err == nil && record != nil && (record.ParentThreadID != "" ||
		(record.SessionID != "" && record.SessionID != threadID))
}

func (r *RuntimeRouter) hostTierInherits(threadID string) bool {
	if !r.hostTierChild(threadID) {
		return false
	}
	record, _ := r.threadRecord(session.ThreadID(threadID), true, false)
	return record != nil && strings.TrimSpace(stringFromMap(record.Metadata.Extra, spawnServiceTierOverrideExtraKey)) == ""
}

// Called only under hostServiceTiers.mu, after committing settings. The host
// handoff is ordered; no state/registry/commit lock is held during Wait.
func (r *RuntimeRouter) handoffHostTierLocked(threadID, tier string, explicit bool) *ServiceTierAcceptance {
	if r.hostServiceTiers.commits == nil {
		r.hostServiceTiers.commits = map[string]hostServiceTierCommit{}
	}
	previous, exists := r.hostServiceTiers.commits[threadID]
	retry := false
	if exists && explicit {
		err, done := previous.receipt.result()
		retry = done && err != nil
	}
	if !exists || previous.tier != tier || retry {
		receipt := r.services.ServiceTier.Update(hostTierPointer(tier))
		if receipt == nil {
			receipt = NewServiceTierAcceptance()
			receipt.Resolve(errors.New("host returned no acceptance receipt"))
		}
		previous = hostServiceTierCommit{tier, receipt}
		r.hostServiceTiers.commits[threadID] = previous
	}
	return previous.receipt
}

func (r *RuntimeRouter) commitSettingsWithHostTier(service *ThreadExtraService, params *SettingsUpdateParams, hostAcceptanceInReply bool) (*SettingsUpdateResponse, *Settings, *ServiceTierAcceptance, error) {
	if r.services.ServiceTier == nil || r.hostTierChild(params.ThreadID) {
		response, committed, err := service.UpdateSettingsWithCommit(params)
		return response, committed, nil, err
	}
	r.hostServiceTiers.mu.Lock()
	defer r.hostServiceTiers.mu.Unlock()
	response, committed, err := service.UpdateSettingsWithCommit(params)
	if err != nil || committed == nil {
		return response, committed, nil, err
	}
	explicit := params.ServiceTier != nil && params.ServiceTier.Set
	tier := hostTierValue(committed.ServiceTier)
	previous, exists := r.hostServiceTiers.commits[params.ThreadID]
	if !explicit && (!exists || previous.tier == tier) {
		return response, committed, nil, nil
	}
	receipt := r.handoffHostTierLocked(params.ThreadID, tier, explicit && hostAcceptanceInReply)
	return response, committed, receipt, nil
}

func (r *RuntimeRouter) startHostServiceTier(ctx context.Context, response *ThreadStartResponse) error {
	if r.services.ServiceTier == nil || response == nil || response.Thread == nil || r.hostTierChild(response.Thread.ID) {
		return nil
	}
	r.hostServiceTiers.mu.Lock()
	receipt := r.handoffHostTierLocked(response.Thread.ID, hostTierValue(response.ServiceTier), false)
	r.hostServiceTiers.mu.Unlock()
	return r.waitHostServiceTier(ctx, receipt)
}

// HandleContext supplies cancellation for host startup/settings acceptance.
// Transport callers using Handle retain a bounded default wait.
func (r *RuntimeRouter) HandleContext(ctx context.Context, request *Request) *Response {
	if request == nil {
		return r.Handle(request)
	}
	copy := *request
	copy.hostContext = ctx
	return r.Handle(&copy)
}
func hostRequestContext(request *Request) context.Context {
	if request != nil && request.hostContext != nil {
		return request.hostContext
	}
	return context.Background()
}

// The first step is captured by appTurnConfig. Subsequent steps await the
// latest shared tier with the actual loop cancellation context.
func (r *RuntimeRouter) hostTurnStepSettingsProvider(threadID, turnID string) func(context.Context) (*turn.AgentStepSettings, error) {
	current := r.turnStepSettingsProvider(threadID, turnID)
	return func(ctx context.Context) (*turn.AgentStepSettings, error) {
		settings := current()
		if settings == nil || !r.hostTierInherits(threadID) {
			return settings, nil
		}
		tier, err := r.readHostServiceTier(ctx)
		if err != nil {
			return nil, err
		}
		cfg, err := r.effectiveConfigForTurn(r.activeTurnParams(threadID))
		if err != nil {
			return nil, err
		}
		settings.ServiceTier = model.ServiceTierForConfiguredRequest(r.modelInfoForRuntimeWithConfig(settings.Model, cfg), tier, cfg.FeatureSettings())
		return settings, nil
	}
}
