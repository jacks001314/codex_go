package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"codex_go/utils"
)

// Mirrors Rust guardian-v3 tests/retrieval/delivery.rs: selected originals are
// admitted once in source order, admission failure leaves progress untouched,
// oversized originals retry with a larger budget, and a captured view never
// reports posts appended after it.
func TestGuardianDeliveryKeepsOrderedOriginalsOnceLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, reviewer, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	board, err := NewGuardianLocalBoard(sqlite, root).ReadWrite().Open(ctx, host)
	if err != nil {
		t.Fatalf("open guardian board: %v", err)
	}
	defer board.Close()
	for _, name := range []string{"a", "b", "older", "large"} {
		if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: name}); err != nil {
			t.Fatalf("create channel %q: %v", name, err)
		}
	}
	appendEvidence := func(ordinal uint64, channel string, output string) {
		t.Helper()
		requestID, err := GuardianEvidenceRequestID(ordinal, channel)
		if err != nil {
			t.Fatalf("GuardianEvidenceRequestID(%d, %q) error = %v", ordinal, channel, err)
		}
		if _, err := board.Append(ctx, root, AppendRequest{
			RequestID:   requestID,
			Destination: PostDestination{Kind: "channel", Name: channel},
			Text:        guardianEvidenceText(ordinal, output),
		}); err != nil {
			t.Fatalf("append evidence %d to %q: %v", ordinal, channel, err)
		}
	}
	large := strings.Repeat("é", 20_001)
	appendEvidence(3, "a", "two")
	appendEvidence(2, "b", "one")
	appendEvidence(2, "a", "one")
	appendEvidence(1, "older", "older")
	appendEvidence(4, "large", large)

	captured, err := board.CaptureReadView(ctx)
	if err != nil {
		t.Fatalf("capture view: %v", err)
	}
	view := &guardianCountingView{BoardReadView: captured}
	cutoff := GuardianEvidenceCutoff{ThreadID: root, EndOrdinalExclusive: 6, EndByteOffset: 64 * 1024}
	state := NewGuardianDeliveryState(reviewer, root)
	beforeAdmission := state.Clone()

	plan, err := state.Plan(ctx, view, []string{"a", "b"}, cutoff)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if _, err := plan.Deliver(ctx, utils.BytesPolicy(160), func(context.Context, GuardianEvidenceItem) error {
		return errors.New("admission unavailable")
	}); err == nil {
		t.Fatal("Deliver() accepted a failed admission")
	}
	if !reflect.DeepEqual(state, beforeAdmission) {
		t.Fatalf("failed admission changed delivery state: %#v", state)
	}

	plan, err = state.Plan(ctx, view, []string{"a", "b"}, cutoff)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	readsBefore := view.readPostTextsCalls
	var admitted []GuardianEvidenceItem
	admit := func(_ context.Context, item GuardianEvidenceItem) error {
		admitted = append(admitted, item)
		return nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		count, err := plan.Deliver(ctx, utils.BytesPolicy(160), admit)
		if err != nil {
			t.Fatalf("Deliver() attempt %d error = %v", attempt, err)
		}
		if count != 1 {
			t.Fatalf("Deliver() attempt %d count = %d, want 1", attempt, count)
		}
	}
	if reads := view.readPostTextsCalls - readsBefore; reads != 1 {
		t.Fatalf("two admissions used %d board read pages, want 1", reads)
	}

	older, err := state.Plan(ctx, view, []string{"older"}, cutoff)
	if err != nil {
		t.Fatalf("Plan(older) error = %v", err)
	}
	if count, err := older.Deliver(ctx, utils.BytesPolicy(160), admit); err != nil || count != 1 {
		t.Fatalf("Deliver(older) = %d, %v, want 1", count, err)
	}
	checkpoint := state.Clone()

	long, err := state.Plan(ctx, view, []string{"large"}, cutoff)
	if err != nil {
		t.Fatalf("Plan(large) error = %v", err)
	}
	if _, err := long.Deliver(ctx, utils.BytesPolicy(160), func(context.Context, GuardianEvidenceItem) error {
		t.Fatal("oversized originals must not reach admission")
		return nil
	}); err == nil {
		t.Fatal("Deliver(large) accepted an oversized source")
	}
	if !reflect.DeepEqual(state, checkpoint) {
		t.Fatalf("oversized source changed delivery state: %#v", state)
	}
	long, err = state.Plan(ctx, view, []string{"large"}, cutoff)
	if err != nil {
		t.Fatalf("Plan(large) error = %v", err)
	}
	if count, err := long.Deliver(ctx, utils.BytesPolicy(64*1024), admit); err != nil || count != 1 {
		t.Fatalf("Deliver(large, 64 KiB) = %d, %v, want 1", count, err)
	}

	appendEvidence(5, "a", "late")
	appendEvidence(3, "b", "two")
	repeated, err := state.Plan(ctx, view, []string{"a", "b"}, cutoff)
	if err != nil {
		t.Fatalf("Plan(repeated) error = %v", err)
	}
	if count, err := repeated.Deliver(ctx, utils.BytesPolicy(160), func(context.Context, GuardianEvidenceItem) error {
		t.Fatal("the captured view must not see posts appended after it")
		return nil
	}); err != nil || count != 0 {
		t.Fatalf("Deliver(repeated) = %d, %v, want 0", count, err)
	}
	freshView, err := board.CaptureReadView(ctx)
	if err != nil {
		t.Fatalf("capture fresh view: %v", err)
	}
	fresh, err := state.Plan(ctx, freshView, []string{"a", "b"}, cutoff)
	if err != nil {
		t.Fatalf("Plan(fresh) error = %v", err)
	}
	if count, err := fresh.Deliver(ctx, utils.BytesPolicy(160), admit); err != nil || count != 1 {
		t.Fatalf("Deliver(fresh) = %d, %v, want 1", count, err)
	}
	if count, err := fresh.Deliver(ctx, utils.BytesPolicy(160), func(context.Context, GuardianEvidenceItem) error {
		t.Fatal("a duplicate source must not be admitted again")
		return nil
	}); err != nil || count != 0 {
		t.Fatalf("Deliver(fresh duplicate) = %d, %v, want 0", count, err)
	}

	want := []struct {
		ordinal uint64
		output  string
	}{{2, "one"}, {3, "two"}, {1, "older"}, {4, large}, {5, "late"}}
	if len(admitted) != len(want) {
		t.Fatalf("admitted %d evidence items, want %d", len(admitted), len(want))
	}
	for index, expected := range want {
		ordinal, output := decodeGuardianEvidenceItem(t, admitted[index])
		if ordinal != expected.ordinal || output != expected.output {
			t.Fatalf("admitted[%d] = (%d, %d bytes), want (%d, %d bytes)",
				index, ordinal, len(output), expected.ordinal, len(expected.output))
		}
	}

	if _, err := state.Plan(ctx, view, []string{"a"}, GuardianEvidenceCutoff{
		ThreadID: root, EndOrdinalExclusive: 5,
	}); err == nil {
		t.Fatal("Plan() accepted a cutoff older than delivered evidence")
	}
}

func TestGuardianEvidenceRequestIDMatchesRustShape(t *testing.T) {
	requestID, err := GuardianEvidenceRequestID(7, "release")
	if err != nil {
		t.Fatalf("GuardianEvidenceRequestID() error = %v", err)
	}
	if requestID != `[7,"release"]` {
		t.Fatalf("request ID = %q, want [7,\"release\"]", requestID)
	}
	parsed, err := ParseGuardianEvidenceRequestID(requestID, "release")
	if err != nil || parsed.Ordinal != 7 {
		t.Fatalf("ParseGuardianEvidenceRequestID() = %#v, %v", parsed, err)
	}
	if _, err := ParseGuardianEvidenceRequestID(requestID, "other"); err == nil {
		t.Fatal("ParseGuardianEvidenceRequestID() accepted a different channel")
	}
}

type guardianCountingView struct {
	*BoardReadView
	readPostTextsCalls int
}

func (v *guardianCountingView) ReadPostTexts(ctx context.Context, caller string, positions []int64) ([]string, error) {
	v.readPostTextsCalls++
	return v.BoardReadView.ReadPostTexts(ctx, caller, positions)
}

func guardianEvidenceText(ordinal uint64, output string) string {
	return fmt.Sprintf(
		`{"id":{"ordinal":%d},"original":{"type":"response_item","payload":{"type":"function_call_output","output":%q}}}`,
		ordinal, output,
	)
}

func decodeGuardianEvidenceItem(t *testing.T, item GuardianEvidenceItem) (uint64, string) {
	t.Helper()
	if item.Name != GuardianEvidenceContextName || item.Namespace != GuardianEvidenceNamespace {
		t.Fatalf("evidence item = %#v, want %s/%s", item, GuardianEvidenceNamespace, GuardianEvidenceContextName)
	}
	var payload struct {
		Sources []struct {
			ID       GuardianEvidenceID `json:"id"`
			Original json.RawMessage    `json:"original"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(item.Text), &payload); err != nil {
		t.Fatalf("decode evidence item: %v", err)
	}
	if len(payload.Sources) != 1 {
		t.Fatalf("evidence sources = %d, want 1", len(payload.Sources))
	}
	var original struct {
		Payload struct {
			Output string `json:"output"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(payload.Sources[0].Original, &original); err != nil {
		t.Fatalf("decode evidence original: %v", err)
	}
	return payload.Sources[0].ID.Ordinal, original.Payload.Output
}

// Mirrors Rust's rejection of evidence beyond the review's source-history
// cutoff: planning fails before any original can be admitted.
func TestGuardianDeliveryRejectsEvidenceBeyondCutoffLikeRust(t *testing.T) {
	ctx := context.Background()
	host, root, reviewer, _ := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	board, err := NewGuardianLocalBoard(sqlite, root).ReadWrite().Open(ctx, host)
	if err != nil {
		t.Fatalf("open guardian board: %v", err)
	}
	defer board.Close()
	if _, err := board.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "a"}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	for _, ordinal := range []uint64{1, 3} {
		requestID, err := GuardianEvidenceRequestID(ordinal, "a")
		if err != nil {
			t.Fatalf("GuardianEvidenceRequestID(%d) error = %v", ordinal, err)
		}
		if _, err := board.Append(ctx, root, AppendRequest{
			RequestID:   requestID,
			Destination: PostDestination{Kind: "channel", Name: "a"},
			Text:        guardianEvidenceText(ordinal, "output"),
		}); err != nil {
			t.Fatalf("append evidence %d: %v", ordinal, err)
		}
	}
	view, err := board.CaptureReadView(ctx)
	if err != nil {
		t.Fatalf("capture view: %v", err)
	}
	state := NewGuardianDeliveryState(reviewer, root)
	_, err = state.Plan(ctx, view, []string{"a"}, GuardianEvidenceCutoff{
		ThreadID:            root,
		EndOrdinalExclusive: 2,
		EndByteOffset:       64 * 1024,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds review cutoff") {
		t.Fatalf("Plan() error = %v, want the review cutoff rejection", err)
	}
}
