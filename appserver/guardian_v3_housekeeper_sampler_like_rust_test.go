package appserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/agentboard"
	"codex_go/model"
	"codex_go/utils"
)

// Rust parity: codex-rs/ext/guardian-v3/src/housekeeper_sampler.rs (#61798) and
// the routing wiring in housekeeper.rs (#61799). One tool-free conversation
// serves every routing batch of a root: completed batches stay in its history,
// a reply is retained only after the board and watermark are durable, pruning
// happens in whole batches, and a replaced account cancels the pass.

type guardianSamplerRecorder struct {
	mu      sync.Mutex
	prompts []string
	answers []string
	errs    []error
	calls   int
}

func (r *guardianSamplerRecorder) agent() model.AgentRunner {
	return guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		r.mu.Lock()
		index := r.calls
		r.calls++
		r.prompts = append(r.prompts, request.Prompt)
		answer := `{"routes":[]}`
		if index < len(r.answers) {
			answer = r.answers[index]
		}
		var err error
		if index < len(r.errs) {
			err = r.errs[index]
		}
		r.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return &model.AgentResponse{ResponseID: "resp", Message: answer}, nil
	})
}

func (r *guardianSamplerRecorder) promptAt(index int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index >= len(r.prompts) {
		return ""
	}
	return r.prompts[index]
}

func (r *guardianSamplerRecorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func newGuardianSamplerForTest(recorder *guardianSamplerRecorder, options GuardianV3HousekeeperSamplerOptions) (*guardianV3HousekeeperSampler, *int) {
	resolved := 0
	sampler := newGuardianV3HousekeeperSampler(func() (model.AgentRunner, string, error) {
		resolved++
		return recorder.agent(), "guardian", nil
	}, options)
	return sampler, &resolved
}

// A completed batch stays in the conversation, so the next routing request
// carries the previous input and reply; the tool-free instructions never enter
// the history.
func TestGuardianV3SamplerRetainsCommittedBatchesLikeRust(t *testing.T) {
	recorder := &guardianSamplerRecorder{answers: []string{`{"routes":[["ops"]]}`, `{"routes":[["ops"]]}`}}
	sampler, resolved := newGuardianSamplerForTest(recorder, GuardianV3HousekeeperSamplerOptions{InputLimit: 100000})
	ctx := context.Background()

	first, err := sampler.Sample(ctx, "route the evidence", `{"batch":1}`)
	if err != nil {
		t.Fatalf("first Sample() error = %v", err)
	}
	if strings.Contains(recorder.promptAt(0), "route the evidence") {
		t.Fatal("instructions entered the retained history")
	}
	sampler.CommitRoutingBatch()
	if sampler.History() == nil || len(sampler.History()) != 2 {
		t.Fatalf("history after commit = %#v", sampler.History())
	}

	if _, err := sampler.Sample(ctx, "route the evidence", `{"batch":2}`); err != nil {
		t.Fatalf("second Sample() error = %v", err)
	}
	second := recorder.promptAt(1)
	if !strings.Contains(second, `{"batch":1}`) || !strings.Contains(second, first) || !strings.Contains(second, `{"batch":2}`) {
		t.Fatalf("second prompt did not carry the retained batch: %q", second)
	}
	if *resolved != 2 {
		t.Fatalf("runner resolutions = %d, want one per batch (credential renewal)", *resolved)
	}
}

// A batch that never reached the board must not become reusable context.
func TestGuardianV3SamplerAbandonsUncommittedBatchLikeRust(t *testing.T) {
	recorder := &guardianSamplerRecorder{}
	sampler, _ := newGuardianSamplerForTest(recorder, GuardianV3HousekeeperSamplerOptions{InputLimit: 100000})
	ctx := context.Background()

	if _, err := sampler.Sample(ctx, "instructions", `{"batch":1}`); err != nil {
		t.Fatalf("first Sample() error = %v", err)
	}
	sampler.AbandonRoutingBatch(errors.New("board write failed"))
	if _, err := sampler.Sample(ctx, "instructions", `{"batch":2}`); err != nil {
		t.Fatalf("second Sample() error = %v", err)
	}
	if prompt := recorder.promptAt(1); strings.Contains(prompt, `{"batch":1}`) {
		t.Fatalf("abandoned batch stayed in the history: %q", prompt)
	}
}

// A failed inference forgets its in-flight input so the next attempt starts from
// the last committed history.
func TestGuardianV3SamplerDropsFailedBatchLikeRust(t *testing.T) {
	recorder := &guardianSamplerRecorder{errs: []error{errors.New("transient inference failure")}}
	sampler, _ := newGuardianSamplerForTest(recorder, GuardianV3HousekeeperSamplerOptions{InputLimit: 100000})
	ctx := context.Background()

	if _, err := sampler.Sample(ctx, "instructions", `{"batch":1}`); err == nil {
		t.Fatal("failed sample reported no error")
	}
	if _, err := sampler.Sample(ctx, "instructions", `{"batch":2}`); err != nil {
		t.Fatalf("second Sample() error = %v", err)
	}
	if prompt := recorder.promptAt(1); strings.Contains(prompt, `{"batch":1}`) {
		t.Fatalf("failed batch stayed in the history: %q", prompt)
	}
}

// Under input-window pressure the conversation drops whole batches, oldest
// first, and never splits one.
func TestGuardianV3SamplerEvictsWholeBatchesLikeRust(t *testing.T) {
	instructions := "instructions"
	inputOne := `{"batch":1}`
	answer := `{"routes":[["ops"]]}`
	fixedTokens := utils.ApproxTokenCount(instructions)
	inputTokens := utils.ApproxTokenCount(inputOne)
	retainedAfterFirst := inputTokens + utils.ApproxTokenCount(answer)
	recorder := &guardianSamplerRecorder{answers: []string{answer, answer}}
	sampler, _ := newGuardianSamplerForTest(recorder, GuardianV3HousekeeperSamplerOptions{
		InputLimit: fixedTokens + inputTokens + retainedAfterFirst - 1,
	})
	ctx := context.Background()

	if _, err := sampler.Sample(ctx, instructions, inputOne); err != nil {
		t.Fatalf("first Sample() error = %v", err)
	}
	sampler.CommitRoutingBatch()
	if _, err := sampler.Sample(ctx, instructions, inputOne); err != nil {
		t.Fatalf("second Sample() error = %v", err)
	}
	prompt := recorder.promptAt(1)
	if strings.Contains(prompt, answer) {
		t.Fatalf("retained batch survived window pressure: %q", prompt)
	}
	if got := sampler.History(); len(got) != 1 {
		t.Fatalf("history = %#v, want only the in-flight input", got)
	}
	sampler.AbandonRoutingBatch(nil)
}

// Rust housekeeper_sampler.rs account_changed: a replaced account cancels the
// batch instead of committing it.
func TestGuardianV3SamplerCancelsOnAccountChangeLikeRust(t *testing.T) {
	current := true
	changes := make(chan struct{})
	watch := agentboard.NewGuardianSamplerAccountWatch(func() bool { return current }, changes)
	blocked := &guardianSamplerRecorder{}
	runner := guardianAgentFunc(func(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	_ = blocked
	sampler := newGuardianV3HousekeeperSampler(func() (model.AgentRunner, string, error) {
		return runner, "guardian", nil
	}, GuardianV3HousekeeperSamplerOptions{InputLimit: 100000, AccountWatch: watch})

	go func() {
		time.Sleep(20 * time.Millisecond)
		current = false
		close(changes)
	}()
	_, err := sampler.Sample(context.Background(), "instructions", `{"batch":1}`)
	if !errors.Is(err, errGuardianSamplerAccountChanged) {
		t.Fatalf("Sample() error = %v, want the account-change error", err)
	}
	if history := sampler.History(); len(history) != 0 {
		t.Fatalf("cancelled batch stayed in the history: %#v", history)
	}
}
