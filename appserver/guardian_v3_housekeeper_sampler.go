package appserver

// Rust parity: codex-rs/ext/guardian-v3/src/housekeeper_sampler.rs (#61798) and
// the routing wiring in housekeeper.rs (#61799). One tool-free conversation
// serves every routing batch of a root: completed batches stay in its history
// until the model input budget forces whole-batch eviction, a reply is retained
// only after the board writes and the routing watermark are durable, and a
// routing pass is cancelled when the owning account changes.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"codex_go/agentboard"
	"codex_go/model"
	"codex_go/utils"
)

// errGuardianSamplerAccountChanged reports that the account owning the sampler
// was replaced mid-batch, so the pass must not commit (Rust
// HousekeeperSampler::account_changed).
var errGuardianSamplerAccountChanged = errors.New("Guardian housekeeper account changed")

// guardianV3SamplerUncappedTokenLimit keeps token accounting on while the host
// knows no model input window, mirroring a conversation that never evicts.
const guardianV3SamplerUncappedTokenLimit = 1 << 30

// GuardianV3HousekeeperSamplerOptions configures one root's tool-free routing
// conversation.
type GuardianV3HousekeeperSamplerOptions struct {
	// InputLimit is the model input budget in tokens. Zero keeps every
	// completed batch because the host has no window to respect.
	InputLimit int
	// AccountWatch cancels a batch when the account that owns the sampler is
	// replaced. The zero value never reports a change.
	AccountWatch agentboard.GuardianSamplerAccountWatch
}

// guardianV3HousekeeperSampler owns one root's retained routing conversation.
// It is the routing sampler and the batch sink for that root, so the
// housekeeper can tell it when a batch became durable.
type guardianV3HousekeeperSampler struct {
	// resolve rebuilds the tool-free runner for each batch, so a renewed
	// credential is picked up without restarting the conversation (Rust
	// #61798 credential renewal).
	resolve      func() (model.AgentRunner, string, error)
	inputLimit   int
	watch        agentboard.GuardianSamplerAccountWatch
	conversation *agentboard.GuardianSamplerConversation

	mu      sync.Mutex
	pending []json.RawMessage
}

func newGuardianV3HousekeeperSampler(resolve func() (model.AgentRunner, string, error), options GuardianV3HousekeeperSamplerOptions) *guardianV3HousekeeperSampler {
	limit := options.InputLimit
	if limit <= 0 {
		limit = guardianV3SamplerUncappedTokenLimit
	}
	return &guardianV3HousekeeperSampler{
		resolve:      resolve,
		inputLimit:   limit,
		watch:        options.AccountWatch,
		conversation: agentboard.NewGuardianSamplerConversation(),
	}
}

// Sample runs one tool-free routing request over the retained history. The
// housekeeper instructions are rebuilt per request and never enter the history.
func (s *guardianV3HousekeeperSampler) Sample(ctx context.Context, instructions string, input string) (string, error) {
	if s == nil || s.resolve == nil {
		return "", errors.New("Guardian routing sampler is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runner, providerID, err := s.resolve()
	if err != nil {
		return "", err
	}
	if runner == nil {
		return "", errors.New("Guardian routing sampler is unavailable")
	}
	batch := json.RawMessage(input)
	if err := s.conversation.BeginBatch(
		batch,
		utils.ApproxTokenCount(input),
		utils.ApproxTokenCount(instructions),
		s.inputLimit,
	); err != nil {
		return "", err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	answer, err := s.runWithAccountWatch(runCtx, runner, providerID, instructions)
	if err != nil {
		s.conversation.AbandonBatch()
		return "", err
	}
	s.mu.Lock()
	s.pending = []json.RawMessage{guardianSamplerReplyItem(answer)}
	s.mu.Unlock()
	return answer, nil
}

// runWithAccountWatch sends the request and abandons it as soon as the account
// changes, so a replaced account never finishes a routing pass.
func (s *guardianV3HousekeeperSampler) runWithAccountWatch(ctx context.Context, runner model.AgentRunner, providerID string, instructions string) (string, error) {
	type sampleResult struct {
		message string
		err     error
	}
	prompt := guardianV3SamplerPrompt(s.conversation.History())
	done := make(chan sampleResult, 1)
	go func() {
		response, err := runner.Run(ctx, &model.AgentRequest{
			Instructions: instructions,
			Prompt:       prompt,
			ProviderID:   providerID,
		})
		switch {
		case err != nil:
			done <- sampleResult{err: err}
		case response == nil:
			done <- sampleResult{err: errors.New("Guardian routing sampler returned no response")}
		default:
			done <- sampleResult{message: response.Message}
		}
	}()
	changed := make(chan bool, 1)
	go func() { changed <- s.watch.Wait(ctx) }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case observed := <-changed:
		if observed {
			return "", errGuardianSamplerAccountChanged
		}
		result := <-done
		return result.message, result.err
	case result := <-done:
		return result.message, result.err
	}
}

// CommitRoutingBatch retains the sampled batch once its board writes and
// routing watermark are durable.
func (s *guardianV3HousekeeperSampler) CommitRoutingBatch() {
	if s == nil {
		return
	}
	s.mu.Lock()
	output := s.pending
	s.pending = nil
	s.mu.Unlock()
	if len(output) == 0 {
		// A batch that produced no retained reply keeps its input only when the
		// conversation still holds it; abandoning is the safe disposition.
		s.conversation.AbandonBatch()
		return
	}
	if err := s.conversation.Commit(output); err != nil {
		s.conversation.AbandonBatch()
	}
}

// AbandonRoutingBatch drops the in-flight batch after a failed pass.
func (s *guardianV3HousekeeperSampler) AbandonRoutingBatch(error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.pending = nil
	s.mu.Unlock()
	s.conversation.AbandonBatch()
}

// History reports the retained routing items, so a host can prove which
// completed batches still travel with the next request.
func (s *guardianV3HousekeeperSampler) History() []agentboard.GuardianSamplerHistoryItem {
	if s == nil {
		return nil
	}
	return s.conversation.History()
}

// guardianV3SamplerPrompt serializes the retained items as JSON lines; the
// tool-free instructions stay in the request's instruction slot.
func guardianV3SamplerPrompt(history []agentboard.GuardianSamplerHistoryItem) string {
	if len(history) == 0 {
		return ""
	}
	lines := make([]string, 0, len(history))
	for _, item := range history {
		if len(item.Item) == 0 {
			continue
		}
		lines = append(lines, string(item.Item))
	}
	return strings.Join(lines, "\n")
}

// guardianSamplerReplyItem keeps the model's answer as one retained history
// item, wrapping non-JSON text so the history stays valid JSON lines.
func guardianSamplerReplyItem(answer string) json.RawMessage {
	trimmed := strings.TrimSpace(answer)
	if trimmed != "" && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return json.RawMessage(encoded)
}
