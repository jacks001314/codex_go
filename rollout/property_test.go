package rollout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// TestRecorderRandomSequencesPreserveRustStructure is the djalign method-5
// property test for rollout writing: random operation sequences written by the
// paginated Recorder must yield a Rust-compatible rollout with contiguous
// ordinals, canonical event_msg payload shapes, the exact item byte round-trip
// and a consistent item lifecycle, and must stay appendable after Resume.
func TestRecorderRandomSequencesPreserveRustStructure(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 20260816} {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			for iter := 0; iter < 30; iter++ {
				verifyRandomRecorderSequence(t, rng, iter)
			}
		})
	}
}

type propertyOp struct {
	kind   string // turn_started / item_started / item_completed / turn_complete
	turnID string
	itemID string
	item   []byte
}

var propertyItemTypes = []string{"message", "function_call", "function_call_output", "custom_tool_call"}

func verifyRandomRecorderSequence(t *testing.T, rng *rand.Rand, iter int) {
	t.Helper()
	home := t.TempDir()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome:   home,
		SessionID:   "prop-session",
		ThreadID:    "prop-thread",
		HistoryMode: "paginated",
	})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()

	var ops []propertyOp
	turnCount := 1 + rng.Intn(3)
	for turn := 0; turn < turnCount; turn++ {
		turnID := fmt.Sprintf("turn-%d-%d", iter, turn)
		ops = append(ops, propertyOp{kind: "turn_started", turnID: turnID})
		itemCount := 1 + rng.Intn(4)
		for item := 0; item < itemCount; item++ {
			itemID := fmt.Sprintf("item-%d-%d-%d", iter, turn, item)
			itemType := propertyItemTypes[rng.Intn(len(propertyItemTypes))]
			text := randomItemText(rng)
			raw, err := json.Marshal(map[string]any{
				"id":   itemID,
				"type": itemType,
				"text": text,
			})
			if err != nil {
				t.Fatalf("marshal item: %v", err)
			}
			ops = append(ops, propertyOp{kind: "item_started", turnID: turnID, itemID: itemID, item: raw})
			if rng.Intn(5) != 0 {
				// Mostly complete the item; occasionally leave it in-progress.
				ops = append(ops, propertyOp{kind: "item_completed", turnID: turnID, itemID: itemID, item: raw})
			}
		}
		ops = append(ops, propertyOp{kind: "turn_complete", turnID: turnID})
	}

	appendPropertyOps(t, recorder, ops, now)
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	verifyPropertyRollout(t, path, ops)

	// Recoverability: Resume must continue with contiguous ordinals.
	resumed, err := Resume(path)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	extra := propertyOp{kind: "item_started", turnID: "turn-after-resume", itemID: "item-after-resume", item: []byte(`{"id":"item-after-resume","type":"message","text":"post"}`)}
	if err := resumed.AppendItemStarted(extra.item, extra.turnID, now); err != nil {
		t.Fatalf("resumed AppendItemStarted: %v", err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatalf("resumed Close: %v", err)
	}
	lines, parseErrors, err := Load(path)
	if err != nil {
		t.Fatalf("Load after resume: %v", err)
	}
	if parseErrors != 0 {
		t.Fatalf("parse errors after resume = %d", parseErrors)
	}
	expectedCount := len(ops) + 2 // session_meta + resumed item_started
	if len(lines) != expectedCount {
		t.Fatalf("lines after resume = %d, want %d", len(lines), expectedCount)
	}
	assertContiguousOrdinals(t, lines)
}

func appendPropertyOps(t *testing.T, recorder *Recorder, ops []propertyOp, now time.Time) {
	t.Helper()
	for _, op := range ops {
		var err error
		switch op.kind {
		case "turn_started":
			err = recorder.AppendTurnStarted(op.turnID, now)
		case "item_started":
			err = recorder.AppendItemStarted(op.item, op.turnID, now)
		case "item_completed":
			err = recorder.AppendItemCompleted(op.item, op.turnID, now, now)
		case "turn_complete":
			err = recorder.AppendTurnComplete(op.turnID, now, 0)
		}
		if err != nil {
			t.Fatalf("append %s: %v", op.kind, err)
		}
	}
}

func verifyPropertyRollout(t *testing.T, path string, ops []propertyOp) {
	t.Helper()
	lines, parseErrors, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if parseErrors != 0 {
		t.Fatalf("parse errors = %d", parseErrors)
	}
	if len(lines) != len(ops)+1 {
		t.Fatalf("line count = %d, want %d (ops + session_meta)", len(lines), len(ops)+1)
	}
	assertContiguousOrdinals(t, lines)
	if lines[0].Type != "session_meta" || lines[0].Meta == nil || lines[0].Meta.ID != "prop-thread" {
		t.Fatalf("first line = %#v, want session_meta for prop-thread", lines[0])
	}

	// Replay the ops in order against the persisted event_msg payloads.
	type payload struct {
		Type   string          `json:"type"`
		TurnID string          `json:"turn_id"`
		Item   json.RawMessage `json:"item"`
	}
	completed := map[string]bool{}
	started := map[string]bool{}
	itemIndex := 1 // skip session_meta
	for _, op := range ops {
		if itemIndex >= len(lines) {
			t.Fatalf("ran out of persisted lines replaying op %+v", op)
		}
		line := lines[itemIndex]
		itemIndex++
		if line.Type != "event_msg" {
			t.Fatalf("persisted line type = %q, want event_msg (op %+v)", line.Type, op)
		}
		var p payload
		if err := json.Unmarshal(line.Payload, &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		wantType := map[string]string{
			"turn_started": "task_started", "item_started": "item_started",
			"item_completed": "item_completed", "turn_complete": "task_complete",
		}[op.kind]
		if p.Type != wantType {
			t.Fatalf("payload type = %q, want %q (op %+v)", p.Type, wantType, op)
		}
		if p.TurnID != op.turnID {
			t.Fatalf("payload turn_id = %q, want %q (op %+v)", p.TurnID, op.turnID, op)
		}
		if op.kind == "item_started" || op.kind == "item_completed" {
			if !bytes.Equal(bytes.TrimSpace(p.Item), bytes.TrimSpace(op.item)) {
				t.Fatalf("item bytes do not round-trip for op %+v:\n got %s\nwant %s", op, p.Item, op.item)
			}
			if op.kind == "item_started" {
				started[op.itemID] = true
			} else {
				completed[op.itemID] = true
			}
		}
	}
	if itemIndex != len(lines) {
		t.Fatalf("consumed %d lines, want %d", itemIndex, len(lines))
	}
	for _, op := range ops {
		if op.kind != "item_completed" {
			continue
		}
		if !started[op.itemID] {
			t.Fatalf("item %q completed without a persisted start", op.itemID)
		}
		if !completed[op.itemID] {
			t.Fatalf("item %q started but no persisted completion", op.itemID)
		}
	}
}

func assertContiguousOrdinals(t *testing.T, lines []Line) {
	t.Helper()
	for i, line := range lines {
		if line.Ordinal == nil || *line.Ordinal != uint64(i) {
			t.Fatalf("line %d ordinal = %v, want %d", i, line.Ordinal, i)
		}
	}
}

func randomItemText(rng *rand.Rand) string {
	parts := []string{"hello", "wörld", "日本語", "line\nwith\nbreaks", "", "x = 1", "🎉 emoji"}
	n := 1 + rng.Intn(3)
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(parts[rng.Intn(len(parts))])
	}
	return b.String()
}
