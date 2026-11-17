package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/utils"
)

// fakeGuardianChannelSampler records the bounded selection call and returns the
// scripted answer, standing in for the host's isolated reviewer runtime.
type fakeGuardianChannelSampler struct {
	calls       []GuardianChannelSelectionCall
	answer      json.RawMessage
	disposition GuardianSessionDisposition
	err         error
}

func (f *fakeGuardianChannelSampler) SampleGuardianChannelSelection(_ context.Context, call GuardianChannelSelectionCall) (json.RawMessage, GuardianSessionDisposition, error) {
	f.calls = append(f.calls, call)
	return f.answer, f.disposition, f.err
}

type guardianSelectionFixture struct {
	ctx           context.Context
	t             *testing.T
	root          string
	writer        *LocalAgentMessageBoard
	reviewer      string
	reviewerPath  agent.AgentPath
	subscriptions *ReviewerSubscriptions
}

func newGuardianSelectionFixture(t *testing.T, descriptions map[string]string, subscribed []string) *guardianSelectionFixture {
	t.Helper()
	ctx := context.Background()
	host, root, reviewer, reviewerPath := testTree(t)
	sqlite := openBoardSqliteConfig(t)
	writer, err := NewGuardianLocalBoard(sqlite, root).ReadWrite().Open(ctx, host)
	if err != nil {
		t.Fatalf("open guardian board: %v", err)
	}
	t.Cleanup(func() { writer.Close() })
	for name, description := range descriptions {
		if err := writer.UpsertChannel(ctx, root, UpsertChannelRequest{ChannelName: name, Description: description}); err != nil {
			t.Fatalf("upsert channel %q: %v", name, err)
		}
	}
	if len(subscribed) > 0 {
		if _, err := writer.SetSubscriptions(ctx, root, BulkSubscriptionsRequest{Channels: subscribed, TargetAgent: &reviewerPath}); err != nil {
			t.Fatalf("seed reviewer subscriptions: %v", err)
		}
	}
	board, err := NewGuardianLocalBoard(sqlite, root).ReadOnly().Open(ctx, host)
	if err != nil {
		t.Fatalf("open reviewer board: %v", err)
	}
	t.Cleanup(func() { board.Close() })
	subscriptions, err := CaptureReviewerSubscriptions(ctx, board, reviewer, reviewerPath)
	if err != nil {
		t.Fatalf("CaptureReviewerSubscriptions() error = %v", err)
	}
	return &guardianSelectionFixture{
		ctx:           ctx,
		t:             t,
		root:          root,
		writer:        writer,
		reviewer:      reviewer,
		reviewerPath:  reviewerPath,
		subscriptions: subscriptions,
	}
}

func (f *guardianSelectionFixture) rawSubscriptions(caller string) []string {
	f.t.Helper()
	names := map[string]struct{}{}
	page := PageRequest{}
	for {
		result, err := f.writer.SubscribedChannels(f.ctx, caller, page)
		if err != nil {
			f.t.Fatalf("SubscribedChannels(%q) error = %v", caller, err)
		}
		if result == nil {
			break
		}
		for _, name := range result.Results {
			names[name] = struct{}{}
		}
		if result.NextCursor == nil {
			break
		}
		page.Cursor = result.NextCursor
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (f *guardianSelectionFixture) addLaterChannel(t *testing.T) {
	t.Helper()
	if err := f.writer.UpsertChannel(f.ctx, f.root, UpsertChannelRequest{ChannelName: "later"}); err != nil {
		t.Fatalf("upsert later channel: %v", err)
	}
	if _, err := f.writer.SetSubscriptions(f.ctx, f.root, BulkSubscriptionsRequest{Channels: []string{"release", "later"}, TargetAgent: &f.reviewerPath}); err != nil {
		t.Fatalf("subscribe reviewer to later channel: %v", err)
	}
}

// Mirrors Rust guardian-v3 tests/retrieval/selection.rs: the selection turn sees
// the captured catalog, current membership and task, and the returned channel set
// becomes the reviewer's catalog membership atomically.
func TestGuardianChannelSelectionReplacesCatalogMembershipLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{
		"release": "Release evidence",
		"build":   "Build evidence",
	}, []string{"release"})
	sampler := &fakeGuardianChannelSampler{
		answer:      json.RawMessage(`{"channels":["build"]}`),
		disposition: GuardianSessionReusable,
	}
	result, err := fixture.subscriptions.Prepare(fixture.ctx, sampler, "review the deploy", utils.BytesPolicy(1<<20))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !reflect.DeepEqual(result.Channels, []string{"build"}) {
		t.Fatalf("Prepare() channels = %v, want [build]", result.Channels)
	}
	if result.Disposition != GuardianSessionReusable {
		t.Fatalf("Prepare() disposition = %q, want reusable", result.Disposition)
	}
	if len(sampler.calls) != 1 {
		t.Fatalf("sampler calls = %d, want 1", len(sampler.calls))
	}
	call := sampler.calls[0]
	if call.InputName != GuardianChannelSelectionInputName || call.Label != GuardianChannelSelectionLabel {
		t.Fatalf("sample call names = %q/%q", call.InputName, call.Label)
	}
	if string(call.Schema) != string(GuardianChannelSelectionSchema) {
		t.Fatalf("sample schema = %s", call.Schema)
	}
	var input GuardianChannelSelectionInput
	if err := json.Unmarshal(call.Input, &input); err != nil {
		t.Fatalf("decode selection input: %v", err)
	}
	if input.Task != "review the deploy" {
		t.Fatalf("input task = %q", input.Task)
	}
	if len(input.Catalog) != 2 || input.Catalog["build"] == nil || *input.Catalog["build"] != "Build evidence" || input.Catalog["release"] == nil {
		t.Fatalf("input catalog = %#v", input.Catalog)
	}
	if !reflect.DeepEqual(input.Subscriptions, []string{"release"}) {
		t.Fatalf("input subscriptions = %v", input.Subscriptions)
	}
	if raw := fixture.rawSubscriptions(fixture.reviewer); !reflect.DeepEqual(raw, []string{"build"}) {
		t.Fatalf("reviewer membership = %v, want [build]", raw)
	}
	channels, err := fixture.subscriptions.Channels(fixture.ctx)
	if err != nil || !reflect.DeepEqual(channels, []string{"build"}) {
		t.Fatalf("Channels() = %v, %v", channels, err)
	}
}

// Channels outside the captured catalog keep their subscriptions and opt-outs
// (Rust set_subscriptions only touches the channels it names).
func TestGuardianChannelSelectionPreservesLaterChannelsLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence"}, []string{"release"})
	fixture.addLaterChannel(t)
	sampler := &fakeGuardianChannelSampler{answer: json.RawMessage(`{"channels":[]}`)}
	result, err := fixture.subscriptions.Prepare(fixture.ctx, sampler, "review", utils.BytesPolicy(1<<20))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(result.Channels) != 0 {
		t.Fatalf("Prepare() channels = %v, want empty", result.Channels)
	}
	if raw := fixture.rawSubscriptions(fixture.reviewer); !reflect.DeepEqual(raw, []string{"later"}) {
		t.Fatalf("reviewer membership = %v, want the out-of-catalog [later]", raw)
	}
	channels, err := fixture.subscriptions.Channels(fixture.ctx)
	if err != nil || len(channels) != 0 {
		t.Fatalf("catalog Channels() = %v, %v, want empty", channels, err)
	}
}

func TestGuardianChannelSelectionEmptySetUnsubscribesCatalogLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence", "build": "Build evidence"}, []string{"release", "build"})
	sampler := &fakeGuardianChannelSampler{answer: json.RawMessage(`{"channels":[]}`)}
	result, err := fixture.subscriptions.Prepare(fixture.ctx, sampler, "review", utils.BytesPolicy(1<<20))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(result.Channels) != 0 {
		t.Fatalf("Prepare() channels = %v, want empty", result.Channels)
	}
	if raw := fixture.rawSubscriptions(fixture.reviewer); len(raw) != 0 {
		t.Fatalf("reviewer membership = %v, want empty", raw)
	}
}

func TestGuardianChannelSelectionRejectsCatalogEscapeLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence"}, []string{"release"})
	fixture.addLaterChannel(t)
	sampler := &fakeGuardianChannelSampler{answer: json.RawMessage(`{"channels":["later"]}`)}
	result, err := fixture.subscriptions.Prepare(fixture.ctx, sampler, "review", utils.BytesPolicy(1<<20))
	if err == nil || !strings.Contains(err.Error(), "unique names from the captured catalog") {
		t.Fatalf("Prepare() error = %v, want catalog rejection", err)
	}
	if result.Disposition != GuardianSessionRollback {
		t.Fatalf("Prepare() disposition = %q, want rollback", result.Disposition)
	}
	if raw := fixture.rawSubscriptions(fixture.reviewer); !reflect.DeepEqual(raw, []string{"later", "release"}) {
		t.Fatalf("membership changed despite rejection: %v", raw)
	}
}

func TestGuardianChannelSelectionRejectsDuplicateChannelsLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence"}, []string{"release"})
	sampler := &fakeGuardianChannelSampler{answer: json.RawMessage(`{"channels":["release","release"]}`)}
	result, err := fixture.subscriptions.Prepare(fixture.ctx, sampler, "review", utils.BytesPolicy(1<<20))
	if err == nil {
		t.Fatal("Prepare() accepted duplicate channels")
	}
	if result.Disposition != GuardianSessionRollback {
		t.Fatalf("Prepare() disposition = %q, want rollback", result.Disposition)
	}
	if raw := fixture.rawSubscriptions(fixture.reviewer); !reflect.DeepEqual(raw, []string{"release"}) {
		t.Fatalf("membership changed despite rejection: %v", raw)
	}
}

func TestGuardianChannelSelectionRejectsUnknownFieldsLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence"}, []string{"release"})
	sampler := &fakeGuardianChannelSampler{answer: json.RawMessage(`{"channels":[],"approve":true}`)}
	result, err := fixture.subscriptions.Prepare(fixture.ctx, sampler, "review", utils.BytesPolicy(1<<20))
	if err == nil {
		t.Fatal("Prepare() accepted an unknown selection field")
	}
	if result.Disposition != GuardianSessionRollback {
		t.Fatalf("Prepare() disposition = %q, want rollback", result.Disposition)
	}
}

func TestGuardianChannelSelectionHonorsCancellationLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence"}, []string{"release"})
	ctx, cancel := context.WithCancel(fixture.ctx)
	cancel()
	sampler := &fakeGuardianChannelSampler{answer: json.RawMessage(`{"channels":["release"]}`)}
	result, err := fixture.subscriptions.Prepare(ctx, sampler, "review", utils.BytesPolicy(1<<20))
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("Prepare() error = %v, want cancellation", err)
	}
	if len(sampler.calls) != 0 {
		t.Fatalf("sampler started after cancellation: %d calls", len(sampler.calls))
	}
	if result.Disposition != GuardianSessionReusable {
		t.Fatalf("Prepare() disposition = %q, want reusable before sampling", result.Disposition)
	}
	if raw := fixture.rawSubscriptions(fixture.reviewer); !reflect.DeepEqual(raw, []string{"release"}) {
		t.Fatalf("membership changed despite cancellation: %v", raw)
	}
}

func TestGuardianChannelSelectionHonorsDeadlineLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence"}, []string{"release"})
	ctx, cancel := context.WithDeadline(fixture.ctx, time.Now().Add(-time.Second))
	defer cancel()
	sampler := &fakeGuardianChannelSampler{answer: json.RawMessage(`{"channels":["release"]}`)}
	_, err := fixture.subscriptions.Prepare(ctx, sampler, "review", utils.BytesPolicy(1<<20))
	if err == nil || !strings.Contains(err.Error(), "deadline expired") {
		t.Fatalf("Prepare() error = %v, want deadline expiry", err)
	}
	if len(sampler.calls) != 0 {
		t.Fatalf("sampler started after the deadline: %d calls", len(sampler.calls))
	}
}

func TestGuardianChannelSelectionEnforcesInputBudgetLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": strings.Repeat("description ", 40)}, []string{"release"})
	sampler := &fakeGuardianChannelSampler{answer: json.RawMessage(`{"channels":["release"]}`)}
	_, err := fixture.subscriptions.Prepare(fixture.ctx, sampler, strings.Repeat("task ", 200), utils.BytesPolicy(16))
	if err == nil || !strings.Contains(err.Error(), "exceeds 16 bytes") {
		t.Fatalf("Prepare() error = %v, want the byte budget failure", err)
	}
	if len(sampler.calls) != 0 {
		t.Fatalf("sampler started with an oversized input: %d calls", len(sampler.calls))
	}
}

func TestGuardianChannelSelectionFailedSamplingRollsBackLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence"}, []string{"release"})
	sampler := &fakeGuardianChannelSampler{disposition: GuardianSessionReusable, err: errors.New("sampling failed")}
	result, err := fixture.subscriptions.Prepare(fixture.ctx, sampler, "review", utils.BytesPolicy(1<<20))
	if err == nil {
		t.Fatal("Prepare() ignored the sampler failure")
	}
	if result.Disposition != GuardianSessionRollback {
		t.Fatalf("Prepare() disposition = %q, want rollback", result.Disposition)
	}
	if raw := fixture.rawSubscriptions(fixture.reviewer); !reflect.DeepEqual(raw, []string{"release"}) {
		t.Fatalf("membership changed despite sampling failure: %v", raw)
	}

	discard := &fakeGuardianChannelSampler{disposition: GuardianSessionDiscard, err: errors.New("turn did not start")}
	result, err = fixture.subscriptions.Prepare(fixture.ctx, discard, "review", utils.BytesPolicy(1<<20))
	if err == nil {
		t.Fatal("Prepare() ignored the discard failure")
	}
	if result.Disposition != GuardianSessionDiscard {
		t.Fatalf("Prepare() disposition = %q, want discard", result.Disposition)
	}
}

func TestGuardianChannelSelectionKeepsOtherCallersLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence", "build": "Build evidence"}, nil)
	if _, err := fixture.writer.SetSubscriptions(fixture.ctx, fixture.root, BulkSubscriptionsRequest{Channels: []string{"release"}}); err != nil {
		t.Fatalf("subscribe root: %v", err)
	}
	if _, err := fixture.writer.SetSubscriptions(fixture.ctx, fixture.root, BulkSubscriptionsRequest{Channels: []string{"release"}, TargetAgent: &fixture.reviewerPath}); err != nil {
		t.Fatalf("subscribe reviewer: %v", err)
	}
	sampler := &fakeGuardianChannelSampler{answer: json.RawMessage(`{"channels":["build"]}`)}
	if _, err := fixture.subscriptions.Prepare(fixture.ctx, sampler, "review", utils.BytesPolicy(1<<20)); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if raw := fixture.rawSubscriptions(fixture.root); !reflect.DeepEqual(raw, []string{"release"}) {
		t.Fatalf("root membership = %v, want unchanged [release]", raw)
	}
	if raw := fixture.rawSubscriptions(fixture.reviewer); !reflect.DeepEqual(raw, []string{"build"}) {
		t.Fatalf("reviewer membership = %v, want [build]", raw)
	}
}

// Replace mirrors the membership half of Prepare, so it must leave channels
// outside the captured catalog subscribed.
func TestGuardianSubscriptionsReplacePreservesOutsideChannelsLikeRust(t *testing.T) {
	fixture := newGuardianSelectionFixture(t, map[string]string{"release": "Release evidence"}, []string{"release"})
	fixture.addLaterChannel(t)
	if _, err := fixture.subscriptions.Replace(fixture.ctx, []string{"release"}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if raw := fixture.rawSubscriptions(fixture.reviewer); !reflect.DeepEqual(raw, []string{"later", "release"}) {
		t.Fatalf("membership = %v, want [later release]", raw)
	}
	if _, err := fixture.subscriptions.Replace(fixture.ctx, []string{"outside"}); err == nil {
		t.Fatal("Replace() accepted a channel outside the captured catalog")
	}
}
