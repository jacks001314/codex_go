package appserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

type tierHandoff struct {
	tier    *string
	receipt *ServiceTierAcceptance
}
type asyncTierHost struct {
	mu      sync.Mutex
	current *string
	readErr error
	blocked <-chan ServiceTierRead
	reads   chan struct{}
	updates chan tierHandoff
	count   int
}

func newAsyncTierHost() *asyncTierHost {
	return &asyncTierHost{reads: make(chan struct{}, 64), updates: make(chan tierHandoff, 64)}
}
func (h *asyncTierHost) Current(ctx context.Context) <-chan ServiceTierRead {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case h.reads <- struct{}{}:
	default:
	}
	if h.blocked != nil {
		return h.blocked
	}
	ch := make(chan ServiceTierRead, 1)
	ch <- ServiceTierRead{cloneString(h.current), h.readErr}
	return ch
}
func (h *asyncTierHost) Update(tier *string) *ServiceTierAcceptance {
	h.mu.Lock()
	h.count++
	h.mu.Unlock()
	receipt := NewServiceTierAcceptance()
	h.updates <- tierHandoff{cloneString(tier), receipt}
	return receipt
}
func (h *asyncTierHost) accept(update tierHandoff, err error) {
	if err == nil {
		h.mu.Lock()
		h.current = cloneString(update.tier)
		h.mu.Unlock()
	}
	update.receipt.Resolve(err)
}
func tierTestRouter(t *testing.T, h *asyncTierHost) (*RuntimeRouter, *session.Store) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	for _, rec := range []*session.Record{
		{ID: "root", SessionID: "root", Metadata: session.Metadata{CWD: t.TempDir(), Model: "host-model", ServiceTier: "flex", MultiAgentVersion: string(agent.VersionV2)}},
		{ID: "child", SessionID: "root", ParentThreadID: "root", Metadata: session.Metadata{CWD: t.TempDir(), Model: "host-model", ServiceTier: "flex"}},
		{ID: "detached", SessionID: "root", Metadata: session.Metadata{CWD: t.TempDir(), Model: "host-model"}},
	} {
		if err := store.Save(rec); err != nil {
			t.Fatal(err)
		}
	}
	models := model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug: "host-model", DisplayName: "Host", ServiceTiers: []string{"flex", "priority"},
	}}}))
	r := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Models: models, ServiceTier: h, ServiceTierTimeout: 2 * time.Second})
	for _, id := range []string{"root", "child", "detached"} {
		r.requireThreadStatus().UpsertThread(id, false)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, store
}
func tierRequest(t *testing.T, value *string) *Request {
	return requestWithParams(t, StringID("tier-update"), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID: "root", ServiceTier: &ThreadExtraOptionalString{Set: true, Value: value},
	})
}
func nextTierUpdate(t *testing.T, h *asyncTierHost) tierHandoff {
	t.Helper()
	select {
	case u := <-h.updates:
		return u
	case <-time.After(4 * time.Second):
		t.Fatal("host did not receive commit")
		return tierHandoff{}
	}
}
func tierResponse(t *testing.T, ch <-chan *Response) *Response {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(4 * time.Second):
		t.Fatal("settings acceptance hung")
		return nil
	}
}
func prepareTier(t *testing.T, r *RuntimeRouter, ctx context.Context) (*agent.PreparedChildContext, error) {
	t.Helper()
	_, guard, inv := activateHostInvocation(t, r, "root", "turn-tier", "call-"+string(newThreadID()))
	defer guard.Release()
	return r.PrepareChild(ctx, "root", inv)
}

// Real host preparation and settings dispatch: commit is visible while pending,
// duplicate requests share one receipt, acceptance changes the next read.
func TestHostServiceTierAcceptanceAndConcurrentRetryLikeRust(t *testing.T) {
	h := newAsyncTierHost()
	r, _ := tierTestRouter(t, h)
	tier := "flex"
	first := make(chan *Response, 1)
	req := tierRequest(t, &tier)
	go func() { first <- r.HandleContext(context.Background(), req) }()
	update := nextTierUpdate(t, h)
	// The host read sees the prior default while the local configured tier is committed.
	prepared, err := prepareTier(t, r, context.Background())
	if err != nil || stringPtrValue(prepared.ServiceTier) != "default" {
		t.Fatalf("pending read = %#v, %v", prepared, err)
	}
	if got := stringPtrValue(r.threadSettingsForTurn("root").ServiceTier); got != "flex" {
		t.Fatalf("committed tier=%q", got)
	}
	const n = 8
	replies := make(chan *Response, n)
	for i := 0; i < n; i++ {
		go func() { replies <- r.HandleContext(context.Background(), req) }()
	}
	// A second different commit must reach the host while the first receipt is pending:
	// lock availability is also demonstrated by the child preparation above.
	h.accept(update, nil)
	if res := tierResponse(t, first); res.Error != nil {
		t.Fatal(res.Error)
	}
	for i := 0; i < n; i++ {
		if res := tierResponse(t, replies); res.Error != nil {
			t.Fatal(res.Error)
		}
	}
	h.mu.Lock()
	count := h.count
	h.mu.Unlock()
	if count != 1 {
		t.Fatalf("duplicate handoffs=%d", count)
	}
	prepared, err = prepareTier(t, r, context.Background())
	if err != nil || stringPtrValue(prepared.ServiceTier) != "flex" {
		t.Fatalf("accepted read=%#v,%v", prepared, err)
	}
	// Repeated host completion is idempotent and cannot replace the successful result.
	update.receipt.Resolve(ErrServiceTierRejected)
	if err := update.receipt.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHostServiceTierRejectAndFailedExplicitRetryLikeRust(t *testing.T) {
	h := newAsyncTierHost()
	r, store := tierTestRouter(t, h)
	old := "priority"
	h.current = &old
	requested := "flex"
	req := tierRequest(t, &requested)
	replies := make(chan *Response, 1)
	go func() { replies <- r.Handle(req) }()
	update := nextTierUpdate(t, h)
	h.accept(update, ErrServiceTierRejected)
	result := tierResponse(t, replies)
	if result.Error == nil || !strings.Contains(result.Error.Message, "host rejected") {
		t.Fatalf("reject=%#v", result)
	}
	saved, err := store.Load("root")
	if err != nil || saved.Metadata.ServiceTier != "flex" {
		t.Fatalf("configured tier rolled back: %#v,%v", saved, err)
	}
	prepared, err := prepareTier(t, r, context.Background())
	if err != nil || stringPtrValue(prepared.ServiceTier) != "priority" {
		t.Fatalf("host prior tier lost: %#v,%v", prepared, err)
	}
	// Non-reply turn-start publication does not retry an already failed receipt.
	r.applyTurnStartSettingsUpdate(&SettingsUpdateParams{ThreadID: "root", ServiceTier: &ThreadExtraOptionalString{Set: true, Value: &requested}})
	h.mu.Lock()
	count := h.count
	h.mu.Unlock()
	if count != 1 {
		t.Fatalf("implicit retry handoffs=%d", count)
	}
	go func() { replies <- r.Handle(req) }()
	retry := nextTierUpdate(t, h)
	h.accept(retry, nil)
	if res := tierResponse(t, replies); res.Error != nil {
		t.Fatal(res.Error)
	}
	// A nil update explicitly restores default, not the prior configured flex.
	go func() { replies <- r.Handle(tierRequest(t, nil)) }()
	clear := nextTierUpdate(t, h)
	if clear.tier != nil {
		t.Fatalf("default handoff=%v", *clear.tier)
	}
	h.accept(clear, nil)
	if res := tierResponse(t, replies); res.Error != nil {
		t.Fatal(res.Error)
	}
	prepared, err = prepareTier(t, r, context.Background())
	if err != nil || stringPtrValue(prepared.ServiceTier) != "default" {
		t.Fatalf("default read=%#v,%v", prepared, err)
	}
}

func TestHostServiceTierReadErrorsTimeoutAndCancelLikeRust(t *testing.T) {
	for _, mode := range []string{"error", "closed", "timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			h := newAsyncTierHost()
			r, _ := tierTestRouter(t, h)
			r.services.ServiceTierTimeout = 25 * time.Millisecond
			ctx := context.Background()
			want := errors.New("host read unavailable")
			switch mode {
			case "error":
				h.readErr = want
			case "closed":
				ch := make(chan ServiceTierRead)
				close(ch)
				h.blocked = ch
			case "timeout":
				h.blocked = make(chan ServiceTierRead)
				want = context.DeadlineExceeded
			case "cancel":
				h.blocked = make(chan ServiceTierRead)
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			_, err := prepareTier(t, r, ctx)
			if err == nil {
				t.Fatal("read failure silently fell back")
			}
			if mode != "closed" && !errors.Is(err, want) {
				t.Fatalf("read error=%v,want %v", err, want)
			}
			if mode != "cancel" {
				var hostErr *ServiceTierHostError
				if !errors.As(err, &hostErr) || hostErr.Operation != "read" {
					t.Fatalf("read classified as accept: %v", err)
				}
			}
		})
	}
}

func TestHostServiceTierAcceptanceTimeoutCancelAndContinuedPropagationLikeRust(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			h := newAsyncTierHost()
			r, _ := tierTestRouter(t, h)
			r.services.ServiceTierTimeout = 40 * time.Millisecond
			value := "flex"
			req := tierRequest(t, &value)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			replies := make(chan *Response, 1)
			go func() { replies <- r.HandleContext(ctx, req) }()
			update := nextTierUpdate(t, h)
			if mode == "cancel" {
				cancel()
			}
			result := tierResponse(t, replies)
			if result.Error == nil || !(strings.Contains(result.Error.Message, "context deadline exceeded") || strings.Contains(result.Error.Message, "context canceled")) {
				t.Fatalf("wait result=%#v", result)
			}
			// The host operation outlives the cancelled receipt wait.
			h.accept(update, nil)
			if res := r.Handle(req); res.Error != nil {
				t.Fatal(res.Error)
			}
			h.mu.Lock()
			count := h.count
			h.mu.Unlock()
			if count != 1 {
				t.Fatalf("cancelled wait caused another handoff: %d", count)
			}
		})
	}
}

func TestHostServiceTierCommitOrderAndDetachedChildLikeRust(t *testing.T) {
	h := newAsyncTierHost()
	r, _ := tierTestRouter(t, h)
	a, b := "flex", "priority"
	first := make(chan *Response, 1)
	second := make(chan *Response, 1)
	reqA, reqB := tierRequest(t, &a), tierRequest(t, &b)
	go func() { first <- r.Handle(reqA) }()
	ua := nextTierUpdate(t, h)
	go func() { second <- r.Handle(reqB) }()
	ub := nextTierUpdate(t, h)
	if stringPtrValue(ua.tier) != "flex" || stringPtrValue(ub.tier) != "priority" {
		t.Fatal("handoff order differs from commit order")
	}
	h.accept(ua, nil)
	h.accept(ub, nil)
	if res := tierResponse(t, first); res.Error != nil {
		t.Fatal(res.Error)
	}
	if res := tierResponse(t, second); res.Error != nil {
		t.Fatal(res.Error)
	}
	// A detached child is identified by session ID and cannot publish or override.
	res := r.Handle(requestWithParams(t, IntID(4), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID: "detached", ServiceTier: &ThreadExtraOptionalString{Set: true, Value: &a},
	}))
	if res.Error != nil {
		t.Fatal(res.Error)
	}
	h.mu.Lock()
	count := h.count
	h.mu.Unlock()
	if count != 2 {
		t.Fatalf("child published shared tier: %d", count)
	}
	_, err := r.handleTurnStart(requestWithParams(t, IntID(5), MethodTurnStart, turn.TurnStartParams{ThreadID: "detached", ServiceTierForTurn: &a}))
	if err == nil || !strings.Contains(err.Error(), "cannot override") {
		t.Fatalf("child override=%v", err)
	}
}

type tierPrewarmAgent struct {
	*recordingRuntimeAgent
	calls chan string
}

func (a *tierPrewarmAgent) Prewarm(ctx context.Context, req *model.AgentRequest) (*model.AgentResponse, error) {
	a.calls <- req.ServiceTier
	return &model.AgentResponse{ResponseID: "host-tier-warm"}, nil
}
func TestHostServiceTierPrewarmAndStepCaptureLikeRust(t *testing.T) {
	h := newAsyncTierHost()
	r, _ := tierTestRouter(t, h)
	value := "flex"
	h.current = &value
	runner := &tierPrewarmAgent{newRecordingRuntimeAgent("ok"), make(chan string, 1)}
	r.services.Agent = runner
	job, ok := r.beginStartupPrewarm("child", startupPrewarmInputBase, nil, "")
	if !ok {
		t.Fatal("prewarm not scheduled")
	}
	job.modelID = "host-model"
	job.serviceTier = "priority"
	job.params = &turn.TurnStartParams{ThreadID: "child", Model: "host-model"}
	r.runStartupPrewarm(job)
	select {
	case got := <-runner.calls:
		if got != "flex" {
			t.Fatalf("prewarm tier=%q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no prewarm request")
	}
	params := &turn.TurnStartParams{ThreadID: "child", Model: "host-model"}
	if err := r.registerActiveRuntimeTurn("child", "step-turn", func() {}, time.Now().UnixMilli(), params); err != nil {
		t.Fatal(err)
	}
	current := r.hostTurnStepSettingsProvider("child", "step-turn")
	settings, err := current(context.Background())
	if err != nil || settings == nil || settings.ServiceTier != "flex" {
		t.Fatalf("step=%#v,%v", settings, err)
	}
	h.mu.Lock()
	h.current = nil
	h.mu.Unlock()
	settings, err = current(context.Background())
	if err != nil || settings.ServiceTier != "" {
		t.Fatalf("default step=%#v,%v", settings, err)
	}
	h.mu.Lock()
	h.blocked = make(chan ServiceTierRead)
	h.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = current(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("step cancellation=%v", err)
	}
}

func TestHostServiceTierStartupWaitLeavesRegisteredRuntimeLikeRust(t *testing.T) {
	for _, mode := range []string{"accept", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			h := newAsyncTierHost()
			r, store := tierTestRouter(t, h)
			req := requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Model: "host-model"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			replies := make(chan *Response, 1)
			go func() { replies <- r.HandleContext(ctx, req) }()
			update := nextTierUpdate(t, h)
			// No startup locks remain held: the registered record is already visible.
			page, err := store.List(session.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			registered := ""
			for _, record := range page.Records {
				if record.ID != "root" && record.ID != "child" && record.ID != "detached" {
					registered = string(record.ID)
				}
			}
			if registered == "" {
				t.Fatal("startup handoff occurred before registration")
			}
			select {
			case res := <-replies:
				t.Fatalf("startup replied before acceptance: %#v", res)
			default:
			}
			if mode == "cancel" {
				cancel()
			} else {
				h.accept(update, nil)
			}
			res := tierResponse(t, replies)
			if (res.Error != nil) != (mode == "cancel") {
				t.Fatalf("startup result=%#v", res)
			}
			if _, err := store.Load(session.ThreadID(registered)); err != nil {
				t.Fatalf("cancelled wait removed runtime: %v", err)
			}
			if mode == "cancel" {
				h.accept(update, nil)
			}
		})
	}
}

func TestHostServiceTierDelayedReadAndControllerChildLikeRust(t *testing.T) {
	h := newAsyncTierHost()
	r, store := tierTestRouter(t, h)
	answer := make(chan ServiceTierRead, 1)
	h.blocked = answer
	_, guard, inv := activateHostInvocation(t, r, "root", "read-turn", "read-call")
	defer guard.Release()
	type result struct {
		prepared *agent.PreparedChildContext
		err      error
	}
	ready := make(chan result, 1)
	go func() { p, err := r.PrepareChild(context.Background(), "root", inv); ready <- result{p, err} }()
	select {
	case <-h.reads:
	case <-time.After(3 * time.Second):
		t.Fatal("async read not started")
	}
	select {
	case res := <-ready:
		t.Fatalf("read did not wait: %#v", res)
	default:
	}
	tier := "flex"
	answer <- ServiceTierRead{Tier: &tier}
	var prepared *agent.PreparedChildContext
	select {
	case res := <-ready:
		if res.err != nil {
			t.Fatal(res.err)
		}
		prepared = res.prepared
	case <-time.After(3 * time.Second):
		t.Fatal("async read never resumed")
	}
	// A captured host tier is applied by the real reserved child controller;
	// it does not initiate a second read from the now-empty answer channel.
	child, err := r.createReservedChild(context.Background(), "root", prepared, agent.ThreadCreateRequest{TaskName: "tier-worker"}, agent.HostThreadSpawnReservation{ThreadID: "host-tier-worker"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Load(session.ThreadID(child.AgentID))
	if err != nil || record.Metadata.ServiceTier != "flex" {
		t.Fatalf("child tier=%#v,%v", record, err)
	}
}
