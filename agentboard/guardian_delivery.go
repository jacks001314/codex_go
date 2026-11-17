package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"codex_go/utils"
)

const (
	// GuardianEvidenceContextName is the function-call-output name that carries
	// routed originals into a reviewer conversation (Rust
	// guardian-v3 sampling::context_input("guardian_original_evidence")).
	GuardianEvidenceContextName = "guardian_original_evidence"
	// GuardianEvidenceNamespace is the namespace of that evidence item.
	GuardianEvidenceNamespace = "guardian"
	// guardianEvidenceRequestIDMaxBytes bounds the generated board idempotency
	// key (Rust EvidenceId::request_id uses 512 bytes).
	guardianEvidenceRequestIDMaxBytes = 512
)

// GuardianEvidenceID identifies one routed original by its source ordinal.
// Several channels may carry the same ordinal; delivery admits it once.
type GuardianEvidenceID struct {
	Ordinal uint64 `json:"ordinal"`
}

// GuardianEvidenceRequestID renders the board idempotency key the housekeeper
// and delivery share (Rust EvidenceId::request_id): a JSON [ordinal, channel].
func GuardianEvidenceRequestID(ordinal uint64, channel string) (string, error) {
	encoded, err := json.Marshal([]any{ordinal, channel})
	if err != nil {
		return "", err
	}
	if len(encoded) > guardianEvidenceRequestIDMaxBytes {
		return "", fmt.Errorf("evidence request ID exceeds %d bytes", guardianEvidenceRequestIDMaxBytes)
	}
	return string(encoded), nil
}

// ParseGuardianEvidenceRequestID reads the source ordinal out of a board
// request ID and rejects a key minted for a different channel (Rust
// EvidenceId::from_request_id).
func ParseGuardianEvidenceRequestID(request string, channel string) (GuardianEvidenceID, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal([]byte(request), &parts); err != nil {
		return GuardianEvidenceID{}, fmt.Errorf("decode evidence request ID: %w", err)
	}
	if len(parts) != 2 {
		return GuardianEvidenceID{}, errors.New("evidence request ID must be [ordinal, channel]")
	}
	var ordinal uint64
	if err := json.Unmarshal(parts[0], &ordinal); err != nil {
		return GuardianEvidenceID{}, fmt.Errorf("decode evidence request ID ordinal: %w", err)
	}
	var destination string
	if err := json.Unmarshal(parts[1], &destination); err != nil {
		return GuardianEvidenceID{}, fmt.Errorf("decode evidence request ID channel: %w", err)
	}
	if destination != channel {
		return GuardianEvidenceID{}, errors.New("evidence request ID channel mismatch")
	}
	return GuardianEvidenceID{Ordinal: ordinal}, nil
}

// GuardianEvidenceCutoff is the captured source window a reviewer may read:
// the owner thread and the exclusive end of its evidence ordinals (Rust
// HistoryPosition).
type GuardianEvidenceCutoff struct {
	ThreadID            string
	EndOrdinalExclusive uint64
	EndByteOffset       int64
}

// GuardianEvidenceView is the captured board surface delivery needs. The
// production implementation is *BoardReadView; tests wrap it to observe reads.
type GuardianEvidenceView interface {
	Identity() string
	StorageNamespace() string
	Channels() []string
	AppendedRequestIDs(ctx context.Context, caller string, channel string, after int64) ([]ReadViewPosition, error)
	ReadPostTexts(ctx context.Context, caller string, positions []int64) ([]string, error)
}

var _ GuardianEvidenceView = (*BoardReadView)(nil)

// GuardianDeliveryState is the in-memory progress of one retained reviewer
// conversation. A fresh conversation starts with fresh state (Rust
// DeliveryState).
type GuardianDeliveryState struct {
	reviewer         string
	board            string
	delivered        map[uint64]struct{}
	channelPositions map[string]int64
}

// NewGuardianDeliveryState scopes delivery progress to one reviewer on its
// owner's Guardian board.
func NewGuardianDeliveryState(reviewer string, board string) *GuardianDeliveryState {
	return &GuardianDeliveryState{
		reviewer:         strings.TrimSpace(reviewer),
		board:            strings.TrimSpace(board),
		delivered:        map[uint64]struct{}{},
		channelPositions: map[string]int64{},
	}
}

// Clone copies delivery progress without sharing mutable maps.
func (s *GuardianDeliveryState) Clone() *GuardianDeliveryState {
	if s == nil {
		return nil
	}
	clone := &GuardianDeliveryState{
		reviewer:         s.reviewer,
		board:            s.board,
		delivered:        make(map[uint64]struct{}, len(s.delivered)),
		channelPositions: make(map[string]int64, len(s.channelPositions)),
	}
	for ordinal := range s.delivered {
		clone.delivered[ordinal] = struct{}{}
	}
	for channel, position := range s.channelPositions {
		clone.channelPositions[channel] = position
	}
	return clone
}

// GuardianDeliveryPlan is exclusive to one selection and captured view.
// Dropping a partially delivered plan preserves successful admissions; the
// next plan resumes from the delivered ordinals and completed channel ranges
// (Rust DeliveryPlan).
type GuardianDeliveryPlan struct {
	state            *GuardianDeliveryState
	view             GuardianEvidenceView
	pending          map[uint64]int64
	prefetched       []string
	channelPositions map[string]int64
}

// Plan collects the undelivered originals of the selected channels within the
// board capture and the review cutoff, reading one board page per request.
func (s *GuardianDeliveryState) Plan(ctx context.Context, view GuardianEvidenceView, selected []string, cutoff GuardianEvidenceCutoff) (*GuardianDeliveryPlan, error) {
	if s == nil || view == nil {
		return nil, errors.New("delivery requires a state and a captured board view")
	}
	if s.board == "" || s.board != view.Identity() || s.board != cutoff.ThreadID {
		return nil, errors.New("delivery board and source cutoff must belong to the reviewer's root")
	}
	if view.StorageNamespace() != GuardianBoardStorageNamespace {
		return nil, errors.New("delivery requires the Guardian board")
	}
	for ordinal := range s.delivered {
		if ordinal >= cutoff.EndOrdinalExclusive {
			return nil, errors.New("review cutoff predates evidence already in the conversation")
		}
	}
	catalog := make(map[string]struct{}, len(view.Channels()))
	for _, channel := range view.Channels() {
		catalog[channel] = struct{}{}
	}
	pending := map[uint64]int64{}
	channelPositions := map[string]int64{}
	for _, channel := range guardianSelectedChannels(selected) {
		if _, ok := catalog[channel]; !ok {
			return nil, errors.New("selected channel is outside the captured board view")
		}
		previous := s.channelPositions[channel]
		position := previous
		for {
			page, err := view.AppendedRequestIDs(ctx, s.reviewer, channel, position)
			if err != nil {
				return nil, err
			}
			lastPage := len(page) < DefaultPageLimit
			for _, entry := range page {
				evidence, err := ParseGuardianEvidenceRequestID(entry.RequestID, channel)
				if err != nil {
					return nil, err
				}
				if evidence.Ordinal >= cutoff.EndOrdinalExclusive {
					return nil, errors.New("board source exceeds review cutoff")
				}
				if _, delivered := s.delivered[evidence.Ordinal]; !delivered {
					if _, exists := pending[evidence.Ordinal]; !exists {
						pending[evidence.Ordinal] = entry.Sequence
					}
				}
				position = entry.Sequence
			}
			if lastPage {
				break
			}
		}
		if position != previous {
			channelPositions[channel] = position
		}
	}
	return &GuardianDeliveryPlan{
		state:            s,
		view:             view,
		pending:          pending,
		channelPositions: channelPositions,
	}, nil
}

// GuardianEvidenceItem is the model-visible function-call output that admits
// one batch of routed originals.
type GuardianEvidenceItem struct {
	Name      string
	Namespace string
	Text      string
}

// Deliver admits one batch of selected board posts. The callback must record
// the item in the retained conversation before reporting success; only then
// are the ordinals marked delivered. Returns zero when nothing is pending
// (Rust DeliveryPlan::deliver).
func (p *GuardianDeliveryPlan) Deliver(ctx context.Context, policy utils.TruncationPolicy, admit func(context.Context, GuardianEvidenceItem) error) (int, error) {
	if p == nil || p.state == nil || p.view == nil {
		return 0, errors.New("delivery plan is unavailable")
	}
	if admit == nil {
		return 0, errors.New("evidence admission callback is required")
	}
	ordinals := make([]uint64, 0, len(p.pending))
	for ordinal := range p.pending {
		ordinals = append(ordinals, ordinal)
	}
	sort.Slice(ordinals, func(left int, right int) bool { return ordinals[left] < ordinals[right] })

	input := guardianDeliveryInput{Sources: []guardianRoutedOriginal{}}
	empty, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	budget := policy.ByteBudget()
	bytes := len(empty)
	unread := len(p.prefetched)
	count := 0
	for count < len(ordinals) {
		if count == len(p.prefetched) {
			end := min(unread+DefaultPageLimit, len(ordinals))
			positions := make([]int64, 0, end-unread)
			for _, ordinal := range ordinals[unread:end] {
				positions = append(positions, p.pending[ordinal])
			}
			texts, err := p.view.ReadPostTexts(ctx, p.state.reviewer, positions)
			if err != nil {
				return 0, err
			}
			if len(texts) != len(positions) {
				return 0, errors.New("board returned a different number of evidence posts than requested")
			}
			p.prefetched = append(p.prefetched, texts...)
			unread = end
		}
		additional := len(p.prefetched[count])
		if count != 0 {
			additional++
		}
		if additional > budget-bytes {
			if count == 0 {
				return 0, errors.New("original evidence exceeds the model's tool-output budget")
			}
			break
		}
		bytes += additional
		count++
	}
	for index := 0; index < count; index++ {
		source, err := parseGuardianRoutedOriginal(p.prefetched[index])
		if err != nil {
			return 0, err
		}
		if source.ID.Ordinal != ordinals[index] {
			return 0, errors.New("selected board post has a different source ordinal")
		}
		input.Sources = append(input.Sources, source)
	}
	if count != 0 {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		item := GuardianEvidenceItem{
			Name:      GuardianEvidenceContextName,
			Namespace: GuardianEvidenceNamespace,
			Text:      utils.TruncateText(string(encoded), policy),
		}
		if err := admit(ctx, item); err != nil {
			return 0, err
		}
		p.prefetched = append([]string(nil), p.prefetched[count:]...)
		for index := 0; index < count; index++ {
			delete(p.pending, ordinals[index])
			p.state.delivered[ordinals[index]] = struct{}{}
		}
	}
	if len(p.pending) == 0 {
		for channel, position := range p.channelPositions {
			p.state.channelPositions[channel] = position
		}
	}
	return count, nil
}

// guardianRoutedOriginal is one stored evidence post: the source ordinal plus
// the exact original model-visible item (Rust RoutedOriginal, deny_unknown_fields).
type guardianRoutedOriginal struct {
	ID       GuardianEvidenceID `json:"id"`
	Original json.RawMessage    `json:"original"`
}

// guardianDeliveryInput is the payload of the admitted context item.
type guardianDeliveryInput struct {
	Sources []guardianRoutedOriginal `json:"sources"`
}

func parseGuardianRoutedOriginal(text string) (guardianRoutedOriginal, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var source guardianRoutedOriginal
	if err := decoder.Decode(&source); err != nil {
		return guardianRoutedOriginal{}, fmt.Errorf("decode routed original: %w", err)
	}
	if decoder.More() {
		return guardianRoutedOriginal{}, errors.New("decode routed original: trailing JSON value")
	}
	if source.Original == nil {
		return guardianRoutedOriginal{}, errors.New("decode routed original: missing original item")
	}
	return source, nil
}

func guardianSelectedChannels(selected []string) []string {
	seen := make(map[string]struct{}, len(selected))
	channels := make([]string, 0, len(selected))
	for _, channel := range selected {
		if _, ok := seen[channel]; ok {
			continue
		}
		seen[channel] = struct{}{}
		channels = append(channels, channel)
	}
	sort.Strings(channels)
	return channels
}
