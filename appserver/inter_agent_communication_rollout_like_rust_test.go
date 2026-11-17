package appserver

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust's record_inter_agent_communication: an accepted agent mail
// message is persisted before the turn samples as the delivery boundary
// followed by the communication's response item, and the boundary carries the
// communication's trigger_turn flag.
func TestRuntimeRouterPersistsAgentCommunicationLikeRust(t *testing.T) {
	router, threadID := newAgentCommunicationRolloutRouter(t)
	createdAt := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	params := &turn.TurnStartParams{
		ThreadID:                       string(threadID),
		InitiatingAgentPath:            "/root/worker",
		AdditionalInputItems:           []any{agentCommunicationInputItem("/root/worker", "/root", "do it")},
		AgentCommunicationTriggerTurns: []bool{true},
	}

	if !router.persistRuntimeAgentCommunications(string(threadID), "turn-mail", params, createdAt) {
		t.Fatal("persistRuntimeAgentCommunications() recorded nothing")
	}

	lines := loadAgentCommunicationRolloutLines(t, router, threadID)
	index := agentCommunicationBoundaryIndex(t, lines)
	for i := 0; i < index; i++ {
		if lines[i].Type == "item" {
			t.Fatalf("rollout recorded an item before the delivery boundary: %+v", lines[:index])
		}
	}
	var boundary struct {
		TriggerTurn bool `json:"trigger_turn"`
	}
	if err := json.Unmarshal(lines[index].Payload, &boundary); err != nil {
		t.Fatalf("boundary payload = %s: %v", lines[index].Payload, err)
	}
	if !boundary.TriggerTurn {
		t.Fatalf("boundary trigger_turn = false, want the triggering communication")
	}
	if index+1 >= len(lines) || lines[index+1].Type != "item" {
		t.Fatalf("line after the boundary = %+v, want the communication's response item", lines[index+1:])
	}
	var communication struct {
		Type      string `json:"type"`
		Author    string `json:"author"`
		Recipient string `json:"recipient"`
	}
	if err := json.Unmarshal(lines[index+1].Item, &communication); err != nil {
		t.Fatalf("response item = %s: %v", lines[index+1].Item, err)
	}
	if communication.Type != "agent_message" || communication.Author != "/root/worker" || communication.Recipient != "/root" {
		t.Fatalf("response item = %+v, want the delivered agent_message", communication)
	}
	var metadata struct {
		UserInputOrder         *uint64 `json:"user_input_order"`
		HistorySequence        *uint64 `json:"history_sequence"`
		SampledThroughSequence *uint64 `json:"sampled_through_sequence"`
	}
	if err := json.Unmarshal(lines[index+1].ItemMetadata, &metadata); err != nil {
		t.Fatalf("item metadata = %s: %v", lines[index+1].ItemMetadata, err)
	}
	if metadata.UserInputOrder == nil || metadata.HistorySequence == nil || metadata.SampledThroughSequence == nil {
		t.Fatalf("item metadata = %s, want the acceptance order and history checkpoint", lines[index+1].ItemMetadata)
	}
}

// The recorded communication replays as a model input item, while the boundary
// itself contributes no history.
func TestAgentCommunicationReplaysAsModelInputLikeRust(t *testing.T) {
	router, threadID := newAgentCommunicationRolloutRouter(t)
	createdAt := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	queued := &turn.TurnStartParams{
		ThreadID:             string(threadID),
		AdditionalInputItems: []any{agentCommunicationInputItem("/root/worker", "/root", "queued mail")},
	}
	followup := &turn.TurnStartParams{
		ThreadID:                       string(threadID),
		InitiatingAgentPath:            "/root/reviewer",
		AdditionalInputItems:           []any{agentCommunicationInputItem("/root/reviewer", "/root", "follow up")},
		AgentCommunicationTriggerTurns: []bool{true},
	}
	if !router.persistRuntimeAgentCommunications(string(threadID), "turn-queued", queued, createdAt) {
		t.Fatal("queued communication was not recorded")
	}
	if !router.persistRuntimeAgentCommunications(string(threadID), "turn-followup", followup, createdAt.Add(time.Minute)) {
		t.Fatal("follow-up communication was not recorded")
	}

	lines := loadAgentCommunicationRolloutLines(t, router, threadID)
	boundaries := 0
	for i := range lines {
		if lines[i].Type == "inter_agent_communication_metadata" {
			boundaries++
		}
	}
	if boundaries != 2 {
		t.Fatalf("boundary lines = %d, want one per accepted communication", boundaries)
	}

	record, err := router.threadRecord(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord() = %v, %v", record, err)
	}
	agentMessages := 0
	for i := range record.Items {
		if record.Items[i].Type == "agent_message" {
			agentMessages++
		}
	}
	if agentMessages != 2 {
		t.Fatalf("history agent_message items = %d, want one per communication", agentMessages)
	}

	inputs := session.InputItemsFromItems(record.Items, &session.HistoryBuildOptions{IncludeToolOutputs: true})
	authors := []string{}
	for _, input := range inputs {
		item, ok := input.(map[string]any)
		if !ok || item["type"] != "agent_message" {
			continue
		}
		author, _ := item["author"].(string)
		authors = append(authors, author)
	}
	if len(authors) != 2 || authors[0] != "/root/worker" || authors[1] != "/root/reviewer" {
		t.Fatalf("replayed agent messages = %v, want both communications in delivery order", authors)
	}
}

// Mail steered into a running turn is recorded when the turn admits it, with a
// non-triggering boundary: Rust `hook_runtime` carries the communication's own
// `trigger_turn`, which is false for steering mail because the delivery did not
// start the turn.
func TestRuntimeRouterPersistsSteerAgentCommunicationLikeRust(t *testing.T) {
	router, threadID := newAgentCommunicationRolloutRouter(t)
	createdAt := time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)
	items := []any{
		agentCommunicationInputItem("/root/worker", "/root", "steered mail"),
		map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": "plain steer"}},
		},
	}

	if !router.persistRuntimeSteerAgentCommunications(string(threadID), "turn-steer", items, createdAt) {
		t.Fatal("persistRuntimeSteerAgentCommunications() recorded nothing")
	}

	lines := loadAgentCommunicationRolloutLines(t, router, threadID)
	boundaries := 0
	for i := range lines {
		if lines[i].Type == "inter_agent_communication_metadata" {
			boundaries++
		}
	}
	if boundaries != 1 {
		t.Fatalf("boundary lines = %d, want one for the steered communication only", boundaries)
	}
	index := agentCommunicationBoundaryIndex(t, lines)
	var boundary struct {
		TriggerTurn bool `json:"trigger_turn"`
	}
	if err := json.Unmarshal(lines[index].Payload, &boundary); err != nil {
		t.Fatalf("boundary payload = %s: %v", lines[index].Payload, err)
	}
	if boundary.TriggerTurn {
		t.Fatal("steer boundary trigger_turn = true, want false")
	}
	if index+1 >= len(lines) || lines[index+1].Type != "item" {
		t.Fatalf("line after the boundary = %+v, want the communication's response item", lines[index+1:])
	}

	record, err := router.threadRecord(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord() = %v, %v", record, err)
	}
	agentMessages := 0
	for i := range record.Items {
		if record.Items[i].Type == "agent_message" {
			agentMessages++
		}
		if record.Items[i].Text == "plain steer" {
			t.Fatal("plain steer text was recorded as an agent communication")
		}
	}
	if agentMessages != 1 {
		t.Fatalf("history agent_message items = %d, want the steered communication", agentMessages)
	}
}

// Rust consumes a communication when it records it, so an interrupted turn does
// not deliver admitted mail a second time. Go re-queues a turn's input on
// interrupt, so recording is keyed by the communication's own `amsg_` identity:
// the second admission is a no-op, while a genuinely different communication
// still records its own item and boundary.
func TestRuntimeRouterRecordsRequeuedAgentCommunicationOnceLikeRust(t *testing.T) {
	router, threadID := newAgentCommunicationRolloutRouter(t)
	createdAt := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	mail := runtimeAgentCommunicationInputItem("/root/worker", "/root", "requeued update", false, true)
	admitted := &turn.TurnStartParams{ThreadID: string(threadID), AdditionalInputItems: []any{mail}}
	if !router.persistRuntimeAgentCommunications(string(threadID), "turn-requeued", admitted, createdAt) {
		t.Fatal("first admission recorded nothing")
	}
	// The turn was interrupted after admission, so the same item reaches the
	// next turn under a new turn id.
	if router.persistRuntimeAgentCommunications(string(threadID), "turn-requeued-next", admitted, createdAt.Add(time.Minute)) {
		t.Fatal("re-delivered mail was recorded twice")
	}

	// A different communication (no carried identity) still records normally.
	other := &turn.TurnStartParams{
		ThreadID:             string(threadID),
		AdditionalInputItems: []any{agentCommunicationInputItem("/root/worker", "/root", "different update")},
	}
	if !router.persistRuntimeAgentCommunications(string(threadID), "turn-other", other, createdAt.Add(2*time.Minute)) {
		t.Fatal("a distinct communication was skipped")
	}

	lines := loadAgentCommunicationRolloutLines(t, router, threadID)
	boundaries := 0
	for i := range lines {
		if lines[i].Type == "inter_agent_communication_metadata" {
			boundaries++
		}
	}
	if boundaries != 2 {
		t.Fatalf("boundary lines = %d, want one per recorded communication", boundaries)
	}
	record, err := router.threadRecord(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord() = %v, %v", record, err)
	}
	agentMessages := 0
	for i := range record.Items {
		if record.Items[i].Type == "agent_message" {
			agentMessages++
		}
	}
	if agentMessages != 2 {
		t.Fatalf("history agent_message items = %d, want two recorded communications", agentMessages)
	}
}

// A communication recorded by another path (an injected `agent_message` is
// recorded when the injection lands and then steered into the running turn)
// must not gain a second history item or boundary when the running turn admits
// it without a carried identity. Rust has one recording path per input; Go's
// two paths are reconciled by the recorded payload.
func TestRuntimeRouterSkipsAlreadyRecordedIdentitylessSteerMailLikeRust(t *testing.T) {
	router, threadID := newAgentCommunicationRolloutRouter(t)
	createdAt := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	mail := agentCommunicationInputItem("/root/worker", "/root", "injected update")
	if !router.persistRuntimeAgentCommunications(string(threadID), "turn-injected", &turn.TurnStartParams{
		ThreadID:             string(threadID),
		AdditionalInputItems: []any{mail},
	}, createdAt) {
		t.Fatal("injected communication was not recorded")
	}

	// The same payload reaches the running turn through the steer mailbox.
	if router.persistRuntimeSteerAgentCommunications(string(threadID), "turn-injected", []any{mail}, createdAt.Add(time.Minute)) {
		t.Fatal("already recorded communication was recorded again by the steer path")
	}

	lines := loadAgentCommunicationRolloutLines(t, router, threadID)
	boundaries := 0
	for i := range lines {
		if lines[i].Type == "inter_agent_communication_metadata" {
			boundaries++
		}
	}
	if boundaries != 1 {
		t.Fatalf("boundary lines = %d, want one for the single communication", boundaries)
	}

	// Identical text from a real mail producer still carries its own identity and
	// is recorded as a separate communication.
	repeat := runtimeAgentCommunicationInputItem("/root/worker", "/root", "injected update", false, true)
	if !router.persistRuntimeSteerAgentCommunications(string(threadID), "turn-injected", []any{repeat}, createdAt.Add(2*time.Minute)) {
		t.Fatal("identified communication was skipped")
	}
	record, err := router.threadRecord(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord() = %v, %v", record, err)
	}
	agentMessages := 0
	for i := range record.Items {
		if record.Items[i].Type == "agent_message" {
			agentMessages++
		}
	}
	if agentMessages != 2 {
		t.Fatalf("history agent_message items = %d, want the injected item and the identified repeat", agentMessages)
	}
}

func newAgentCommunicationRolloutRouter(t *testing.T) (*RuntimeRouter, session.ThreadID) {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	threadID := session.ThreadID("thread-agent-mail")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID)}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	return router, threadID
}

func agentCommunicationInputItem(author string, recipient string, text string) map[string]any {
	return map[string]any{
		"type":      "agent_message",
		"author":    author,
		"recipient": recipient,
		"content":   []any{map[string]any{"type": "input_text", "text": text}},
	}
}

func loadAgentCommunicationRolloutLines(t *testing.T, router *RuntimeRouter, threadID session.ThreadID) []rollout.Line {
	t.Helper()
	record, err := router.threadRecord(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord() = %v, %v", record, err)
	}
	path := router.services.ThreadRouter.threadRolloutPath(record)
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("rollout.Load(%s) error = %v", path, err)
	}
	return lines
}

func agentCommunicationBoundaryIndex(t *testing.T, lines []rollout.Line) int {
	t.Helper()
	for i := range lines {
		if lines[i].Type == "inter_agent_communication_metadata" {
			return i
		}
	}
	t.Fatalf("rollout has no inter_agent_communication_metadata line: %+v", lines)
	return -1
}
