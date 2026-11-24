package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/agentboard"
	"codex_go/session"
)

// Rust parity: codex-rs/thread-store/src/local/original_history.rs and
// codex-rs/ext/guardian-v3/src/evidence.rs. Guardian routing reads a root's
// committed originals through the thread store: a tail request only reports the
// durable boundary, an after request reads everything past the intake cursor,
// and a range request reads exactly the cutoff a review captured. The JSON
// thread store has no rollout byte offsets, so positions use the stable item
// history sequence as their ordinal and leave the byte offset at zero.

// GuardianOriginalHistoryRequestKind selects how much of the root's stored
// history a read returns (Rust OriginalHistoryRequest).
type GuardianOriginalHistoryRequestKind int

const (
	// GuardianOriginalHistoryTail reports the durable end without reading
	// records.
	GuardianOriginalHistoryTailKind GuardianOriginalHistoryRequestKind = iota + 1
	// GuardianOriginalHistoryAfter reads everything past Start.
	GuardianOriginalHistoryAfterKind
	// GuardianOriginalHistoryRange reads the half-open Start..End range.
	GuardianOriginalHistoryRangeKind
)

// GuardianOriginalHistoryRequest is one original-history read request.
type GuardianOriginalHistoryRequest struct {
	Kind  GuardianOriginalHistoryRequestKind
	Start agentboard.GuardianHistoryPosition
	End   agentboard.GuardianHistoryPosition
}

// GuardianOriginalHistoryTail asks for the root's durable end only.
func GuardianOriginalHistoryTail(threadID string) GuardianOriginalHistoryRequest {
	return GuardianOriginalHistoryRequest{
		Kind:  GuardianOriginalHistoryTailKind,
		Start: agentboard.GuardianHistoryPosition{ThreadID: strings.TrimSpace(threadID)},
	}
}

// GuardianOriginalHistoryAfter reads every committed record past start.
func GuardianOriginalHistoryAfter(start agentboard.GuardianHistoryPosition) GuardianOriginalHistoryRequest {
	return GuardianOriginalHistoryRequest{Kind: GuardianOriginalHistoryAfterKind, Start: start}
}

// GuardianOriginalHistoryRange reads the half-open range start..end.
func GuardianOriginalHistoryRange(start agentboard.GuardianHistoryPosition, end agentboard.GuardianHistoryPosition) GuardianOriginalHistoryRequest {
	return GuardianOriginalHistoryRequest{Kind: GuardianOriginalHistoryRangeKind, Start: start, End: end}
}

// GuardianOriginalHistoryItem is one record with the durable position it
// reached (Rust OriginalHistoryItem).
type GuardianOriginalHistoryItem struct {
	Item     session.Item
	Position agentboard.GuardianHistoryPosition
}

// GuardianOriginalHistory is the requested records plus the exclusive durable
// end of the read (Rust OriginalHistoryRead).
type GuardianOriginalHistory struct {
	Items []GuardianOriginalHistoryItem
	End   agentboard.GuardianHistoryPosition
}

// ReadGuardianOriginalHistory reads one root's stored originals. A thread that
// references another history, a fork, an attached thread, or a subagent has no
// original history of its own, mirroring Rust's paginated-root requirement.
func ReadGuardianOriginalHistory(store *session.Store, threadID string, request GuardianOriginalHistoryRequest) (GuardianOriginalHistory, error) {
	threadID = strings.TrimSpace(threadID)
	if store == nil {
		return GuardianOriginalHistory{}, errors.New("original history requires a thread store")
	}
	if threadID == "" {
		return GuardianOriginalHistory{}, errors.New("original history requires a root thread")
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		return GuardianOriginalHistory{}, err
	}
	if record == nil {
		return GuardianOriginalHistory{}, errors.New("original history requires a root thread")
	}
	if record.HistoryBase != nil || record.ForkedFromID != "" || record.ParentThreadID != "" || record.AttachedThreadParentID != "" {
		return GuardianOriginalHistory{}, errors.New("original history requires a paginated root")
	}
	items := make([]session.Item, len(record.Items))
	copy(items, record.Items)
	lastSequence := session.EnsureHistorySequences(items)
	// Positions count consumed records, matching Rust's HistoryPosition: a
	// record whose history sequence is N moves the position to N.
	tail := agentboard.GuardianHistoryPosition{ThreadID: threadID, EndOrdinalExclusive: lastSequence}

	switch request.Kind {
	case GuardianOriginalHistoryTailKind:
		return GuardianOriginalHistory{End: tail}, nil
	case GuardianOriginalHistoryAfterKind:
		return readGuardianOriginalRange(threadID, items, request.Start, tail)
	case GuardianOriginalHistoryRangeKind:
		if request.End.EndOrdinalExclusive > tail.EndOrdinalExclusive {
			return GuardianOriginalHistory{}, errors.New("original history cutoff no longer applies")
		}
		return readGuardianOriginalRange(threadID, items, request.Start, request.End)
	default:
		return GuardianOriginalHistory{}, errors.New("original history request is invalid")
	}
}

func readGuardianOriginalRange(threadID string, items []session.Item, start agentboard.GuardianHistoryPosition, end agentboard.GuardianHistoryPosition) (GuardianOriginalHistory, error) {
	if strings.TrimSpace(start.ThreadID) != threadID || strings.TrimSpace(end.ThreadID) != threadID {
		return GuardianOriginalHistory{}, errors.New("original history range belongs to another root")
	}
	if start.EndOrdinalExclusive > end.EndOrdinalExclusive {
		return GuardianOriginalHistory{}, errors.New("original history range no longer applies")
	}
	position := start
	read := GuardianOriginalHistory{End: end}
	for index := range items {
		sequence := session.ItemHistorySequence(&items[index])
		if sequence <= start.EndOrdinalExclusive {
			continue
		}
		if sequence > end.EndOrdinalExclusive {
			break
		}
		if sequence != position.EndOrdinalExclusive+1 {
			return GuardianOriginalHistory{}, errors.New("original history ordinal changed")
		}
		position = agentboard.GuardianHistoryPosition{ThreadID: threadID, EndOrdinalExclusive: sequence}
		read.Items = append(read.Items, GuardianOriginalHistoryItem{Item: items[index], Position: position})
	}
	if position != end {
		return GuardianOriginalHistory{}, errors.New("original history ended before its cutoff")
	}
	return read, nil
}

// GuardianStoreEvidenceReader adapts the thread store to the Guardian intake:
// a nil through flushes and reads after the cursor, and a cutoff reads exactly
// that durable range (Rust ThreadStore::flush_thread plus OriginalHistoryRequest).
func GuardianStoreEvidenceReader(store *session.Store) agentboard.GuardianEvidenceReader {
	return func(ctx context.Context, start agentboard.GuardianHistoryPosition, through *agentboard.GuardianHistoryPosition) (*agentboard.GuardianEvidenceSnapshot, error) {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		request := GuardianOriginalHistoryAfter(start)
		if through != nil {
			request = GuardianOriginalHistoryRange(start, *through)
		}
		read, err := ReadGuardianOriginalHistory(store, start.ThreadID, request)
		if err != nil {
			return nil, &agentboard.GuardianEvidenceRejection{Reason: err.Error()}
		}
		snapshot := &agentboard.GuardianEvidenceSnapshot{Start: start, End: read.End}
		for _, item := range read.Items {
			original, err := guardianOriginalPayload(item.Item)
			if err != nil {
				return nil, &agentboard.GuardianEvidenceRejection{Reason: err.Error()}
			}
			snapshot.Sources = append(snapshot.Sources, agentboard.GuardianCapturedEvidence{
				ID:       agentboard.GuardianEvidenceID{Ordinal: item.Position.EndOrdinalExclusive},
				Original: original,
				End:      item.Position,
			})
		}
		return snapshot, nil
	}
}

// guardianOriginalPayload keeps the stored response payload when the item has
// one, so a routed original is the record the model saw rather than a host
// projection. Items without a payload fall back to their stored fields.
func guardianOriginalPayload(item session.Item) (json.RawMessage, error) {
	if raw := bytes.TrimSpace(item.Raw); len(raw) > 0 && raw[0] == '{' && json.Valid(raw) {
		return json.RawMessage(raw), nil
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("encode original history item: %w", err)
	}
	trimmed := bytes.TrimSpace(encoded)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("original history item is not a JSON object")
	}
	return json.RawMessage(trimmed), nil
}
