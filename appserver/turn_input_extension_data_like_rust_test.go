package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

type turnLifecycleCompletionRecord struct {
	input TurnItemCompletedInput
	data  map[string]any
}

type turnLifecycleRecorder struct {
	mu              sync.Mutex
	baseCompletions []TurnItemCompletedInput
	completions     []turnLifecycleCompletionRecord
}

func (r *turnLifecycleRecorder) OnItemCompleted(input TurnItemCompletedInput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.baseCompletions = append(r.baseCompletions, input)
}

func (r *turnLifecycleRecorder) OnItemCompletedWithMetadata(input TurnItemCompletedInput, data map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completions = append(r.completions, turnLifecycleCompletionRecord{input: input, data: cloneAnyMap(data)})
}

func (r *turnLifecycleRecorder) snapshot() ([]TurnItemCompletedInput, []turnLifecycleCompletionRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TurnItemCompletedInput(nil), r.baseCompletions...), append([]turnLifecycleCompletionRecord(nil), r.completions...)
}

type baseOnlyTurnLifecycleRecorder struct {
	mu      sync.Mutex
	records []TurnItemCompletedInput
}

func (r *baseOnlyTurnLifecycleRecorder) OnItemCompleted(input TurnItemCompletedInput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, input)
}

func (r *baseOnlyTurnLifecycleRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

func TestTurnInputExtensionDataIsHostOnlyAndPerInput(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-input-extension")
	if err := store.Create(&session.Record{ID: threadID, SessionID: "session-input-extension"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), DefaultCWD: t.TempDir()})
	recorder := &turnLifecycleRecorder{}
	router.RegisterTurnLifecycleContributor(recorder)

	params := []*turn.TurnStartParams{
		{ThreadID: string(threadID), Prompt: "first message", ClientUserMessageID: "shared-id", UserInputExtensionData: map[string]any{"test": "extension-test-marker:first"}},
		{ThreadID: string(threadID), Prompt: "second message", ClientUserMessageID: "shared-id", UserInputExtensionData: map[string]any{"test": "extension-test-marker:second"}},
	}
	var wait sync.WaitGroup
	for i := range params {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			if !router.persistRuntimeTurnPrompt(string(threadID), fmt.Sprintf("turn-%d", index+1), params[index], time.Now().UTC()) {
				t.Errorf("persistRuntimeTurnPrompt(%d) = false", index)
			}
		}(i)
	}
	wait.Wait()

	base, completions := recorder.snapshot()
	if len(base) != 0 || len(completions) != 2 {
		t.Fatalf("lifecycle completions = base %d metadata %d, want 0/2", len(base), len(completions))
	}
	byText := map[string]turnLifecycleCompletionRecord{}
	for _, completion := range completions {
		byText[completion.input.Item.Text] = completion
		if completion.input.ThreadID != string(threadID) || completion.input.SessionStore == nil || completion.input.ThreadStore == nil || completion.input.TurnStore == nil {
			t.Fatalf("completion context = %#v", completion.input)
		}
	}
	for _, text := range []string{"first message", "second message"} {
		completion, ok := byText[text]
		if !ok {
			t.Fatalf("missing completion for %q: %#v", text, byText)
		}
		want := "extension-test-marker:" + strings.Fields(text)[0]
		if got := stringFromAny(completion.data["test"]); got != want {
			t.Fatalf("completion %q data = %#v, want %q", text, completion.data, want)
		}
	}

	record, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal(record) error = %v", err)
	}
	if strings.Contains(string(encoded), "extension-test-marker:") {
		t.Fatalf("host-only extension data leaked into persisted history: %s", encoded)
	}
	for _, value := range []any{params[0], params[1]} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("Marshal(params) error = %v", err)
		}
		if strings.Contains(string(encoded), "extension-test-marker:") {
			t.Fatalf("host-only extension data leaked onto the app-server wire: %s", encoded)
		}
	}
}

func TestTurnSteerCarriesEachInputExtensionDataToAcceptingTurn(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-steer-extension")
	if err := store.Create(&session.Record{ID: threadID}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	turns := turn.NewTurnService()
	started, err := turns.Start(&turn.TurnStartParams{ThreadID: string(threadID), Prompt: "start"})
	if err != nil {
		t.Fatalf("turn Start() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turns,
		SteerMailbox: turn.NewSteerMailbox(),
	})
	recorder := &turnLifecycleRecorder{}
	router.RegisterTurnLifecycleContributor(recorder)

	for _, text := range []string{"first steer", "second steer"} {
		params := turn.TurnSteerParams{
			ThreadID:               string(threadID),
			ExpectedTurnID:         started.Turn.ID,
			Prompt:                 text,
			ClientUserMessageID:    "shared-steer-id",
			UserInputExtensionData: map[string]any{"test": "extension-test-marker:" + text},
		}
		response := router.Handle(requestWithInternalParams(MethodTurnSteer, params))
		if response.Error != nil {
			t.Fatalf("turn/steer(%q) = %+v", text, response.Error)
		}
	}

	_, completions := recorder.snapshot()
	if len(completions) != 2 {
		t.Fatalf("metadata lifecycle completions = %d, want 2", len(completions))
	}
	for _, completion := range completions {
		if completion.input.TurnID != started.Turn.ID {
			t.Fatalf("completion turn = %q, want %q", completion.input.TurnID, started.Turn.ID)
		}
		want := "extension-test-marker:" + completion.input.Item.Text
		if got := stringFromAny(completion.data["test"]); got != want {
			t.Fatalf("steer completion %q data = %#v, want %q", completion.input.Item.Text, completion.data, want)
		}
	}
}

func TestTurnLifecycleFallsBackToBaseCallback(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-lifecycle-fallback")
	if err := store.Create(&session.Record{ID: threadID}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), DefaultCWD: t.TempDir()})
	recorder := &baseOnlyTurnLifecycleRecorder{}
	router.RegisterTurnLifecycleContributor(recorder)
	if !router.persistRuntimeTurnPrompt(string(threadID), "turn-fallback", &turn.TurnStartParams{
		ThreadID: string(threadID), Prompt: "fallback", UserInputExtensionData: map[string]any{"test": "hidden"},
	}, time.Now().UTC()) {
		t.Fatal("persistRuntimeTurnPrompt() = false")
	}
	if recorder.count() != 1 {
		t.Fatalf("base lifecycle completions = %d, want 1", recorder.count())
	}
}
