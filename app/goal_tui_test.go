package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/session"
	codextui "codex_go/tui"
)

type recordingInteractiveGoalRouter struct {
	requests []*appserver.Request
	closed   int
}

func (r *recordingInteractiveGoalRouter) Handle(request *appserver.Request) *appserver.Response {
	r.requests = append(r.requests, request)
	switch request.Method {
	case appserver.MethodInitialize:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Result: &appserver.InitializeResponse{}}
	case appserver.MethodThreadGoalGet:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Result: &appserver.GoalGetResponse{Goal: &appserver.Goal{ThreadID: "thread-goal", Objective: "ship parity", Status: appserver.GoalActive}}}
	case appserver.MethodThreadGoalSet:
		var params appserver.GoalSetParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Error: &appserver.ResponseError{Code: -32602, Message: err.Error()}}
		}
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Result: &appserver.GoalSetResponse{Goal: appserver.Goal{ThreadID: params.ThreadID, Objective: *params.Objective, Status: *params.Status, TokenBudget: params.TokenBudget}}}
	case appserver.MethodThreadGoalClear:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Result: &appserver.GoalClearResponse{Cleared: true}}
	default:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Error: &appserver.ResponseError{Code: -32601, Message: "unexpected method"}}
	}
}

func (r *recordingInteractiveGoalRouter) Close() error {
	r.closed++
	return nil
}

func TestInteractiveLocalGoalCallbacksUseAppServerProtocol(t *testing.T) {
	router := &recordingInteractiveGoalRouter{}
	read, set, clear, editText, _ := interactiveLocalGoalCallbacks(func() interactiveGoalRouter { return router })
	goal, err := read(" thread-goal ")
	if err != nil || goal == nil || goal.Objective != "ship parity" {
		t.Fatalf("read goal=%#v err=%v", goal, err)
	}
	objective := " finish parity "
	budget := int64(80_000)
	status := appserver.GoalPaused
	updated, err := set(" thread-goal ", &objective, &budget, &status)
	if err != nil || updated.Objective != "finish parity" || updated.Status != appserver.GoalPaused || updated.TokenBudget == nil || *updated.TokenBudget != budget {
		t.Fatalf("set goal=%#v err=%v", updated, err)
	}
	cleared, err := clear(" thread-goal ")
	if err != nil || !cleared {
		t.Fatalf("clear goal=%v err=%v", cleared, err)
	}
	text, err := editText(" thread-goal ", "non-reference objective")
	if err != nil || text != "non-reference objective" {
		t.Fatalf("editText plain objective=%q err=%v", text, err)
	}
	wantPairs := []struct {
		init appserver.Method
		op   appserver.Method
	}{
		{appserver.MethodInitialize, appserver.MethodThreadGoalGet},
		{appserver.MethodInitialize, appserver.MethodThreadGoalSet},
		{appserver.MethodInitialize, appserver.MethodThreadGoalClear},
	}
	// editText additionally initializes its own router (no fs call for a
	// non-reference objective).
	if len(router.requests) != 2*len(wantPairs)+1 || router.closed != len(wantPairs)+1 {
		t.Fatalf("requests=%d closed=%d", len(router.requests), router.closed)
	}
	for i, pair := range wantPairs {
		if router.requests[2*i].Method != pair.init || router.requests[2*i+1].Method != pair.op {
			t.Fatalf("request[%d]=%#v request[%d]=%#v", 2*i, router.requests[2*i], 2*i+1, router.requests[2*i+1])
		}
		for _, idx := range []int{2 * i, 2*i + 1} {
			if router.requests[idx].ConnectionID != interactiveGoalConnectionID {
				t.Fatalf("request[%d]=%#v", idx, router.requests[idx])
			}
		}
	}
}

func TestInteractiveLocalGoalCallbacksUseDefaultRuntimeRouter(t *testing.T) {
	codexHome := t.TempDir()
	store := session.NewStore(filepath.Join(codexHome, "sessions"))
	threadID := startLocalGoalTestThread(t, store)

	factory := func() interactiveGoalRouter {
		return appserver.NewDefaultRuntimeRouter(store, codexHome)
	}
	read, set, clear, editText, _ := interactiveLocalGoalCallbacks(factory)

	goal, err := read(threadID)
	if err != nil || goal != nil {
		t.Fatalf("initial read goal=%#v err=%v", goal, err)
	}
	objective := "ship parity"
	status := appserver.GoalActive
	updated, err := set(threadID, &objective, nil, &status)
	if err != nil || updated.Objective != "ship parity" || updated.Status != appserver.GoalActive {
		t.Fatalf("set goal=%#v err=%v", updated, err)
	}
	goal, err = read(threadID)
	if err != nil || goal == nil || goal.Objective != "ship parity" || goal.Status != appserver.GoalActive {
		t.Fatalf("read after set goal=%#v err=%v", goal, err)
	}
	cleared, err := clear(threadID)
	if err != nil || !cleared {
		t.Fatalf("clear goal=%v err=%v", cleared, err)
	}
	if _, err := editText(threadID, "plain"); err != nil {
		t.Fatalf("editText plain: %v", err)
	}
	goal, err = read(threadID)
	if err != nil || goal != nil {
		t.Fatalf("read after clear goal=%#v err=%v", goal, err)
	}
}

func TestInteractiveLocalGoalCallbacksMaterializeOversizedObjective(t *testing.T) {
	codexHome := t.TempDir()
	store := session.NewStore(filepath.Join(codexHome, "sessions"))
	threadID := startLocalGoalTestThread(t, store)

	factory := func() interactiveGoalRouter {
		return appserver.NewDefaultRuntimeRouter(store, codexHome)
	}
	read, set, _, editText, _ := interactiveLocalGoalCallbacks(factory)

	longObjective := strings.Repeat("goal objective line\n", 400) // > 4000 runes
	if len([]rune(longObjective)) <= codextui.MaxGoalObjectiveRune {
		t.Fatalf("fixture objective must exceed the %d rune limit", codextui.MaxGoalObjectiveRune)
	}
	// The TUI setter trims the objective before materialization, matching Rust
	// goal_files::materialize_goal_draft's expanded_objective.trim().
	wantObjective := strings.TrimSpace(longObjective)
	status := appserver.GoalActive
	updated, err := set(threadID, &longObjective, nil, &status)
	if err != nil {
		t.Fatalf("set oversized goal: %v", err)
	}
	refPath, ok := codextui.GoalObjectiveFilePath(updated.Objective, codexHome)
	if !ok {
		t.Fatalf("objective is not a managed goal file reference: %q", updated.Objective)
	}
	data, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read materialized goal file: %v", err)
	}
	if string(data) != wantObjective {
		t.Fatalf(
			"materialized goal file content mismatch: len=%d want %d; first diff at %d",
			len(data),
			len(wantObjective),
			firstDiffIndex(data, []byte(wantObjective)),
		)
	}

	// The goal as persisted holds the reference; edit resolution restores the
	// original objective text from the managed file.
	goal, err := read(threadID)
	if err != nil || goal == nil || goal.Objective != updated.Objective {
		t.Fatalf("read after set goal=%#v err=%v", goal, err)
	}
	text, err := editText(threadID, updated.Objective)
	if err != nil || text != wantObjective {
		t.Fatalf("editText resolved %d runes err=%v", len([]rune(text)), err)
	}
}

func TestInteractiveLocalGoalCallbacksMaterializeImageDraft(t *testing.T) {
	codexHome := t.TempDir()
	store := session.NewStore(filepath.Join(codexHome, "sessions"))
	threadID := startLocalGoalTestThread(t, store)
	imagePath := filepath.Join(codexHome, "local-image.png")
	if err := os.WriteFile(imagePath, []byte("png bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	factory := func() interactiveGoalRouter {
		return appserver.NewDefaultRuntimeRouter(store, codexHome)
	}
	read, set, _, _, materialize := interactiveLocalGoalCallbacks(factory)

	draft := codextui.GoalDraft{
		Objective:   "describe [Image #1]",
		LocalImages: []codextui.GoalLocalImage{{Placeholder: "[Image #1]", Path: imagePath}},
	}
	objective, err := materialize(draft)
	if err != nil {
		t.Fatalf("materialize image draft: %v", err)
	}
	imageFile := strings.TrimPrefix(objective, "describe image file: ")
	if imageFile == objective {
		t.Fatalf("objective = %q", objective)
	}
	data, err := os.ReadFile(imageFile)
	if err != nil || string(data) != "png bytes" {
		t.Fatalf("image file = %q err=%v", data, err)
	}

	status := appserver.GoalActive
	updated, err := set(threadID, &objective, nil, &status)
	if err != nil || updated.Objective != objective {
		t.Fatalf("set goal = %#v err=%v", updated, err)
	}
	goal, err := read(threadID)
	if err != nil || goal == nil || goal.Objective != objective {
		t.Fatalf("read after set = %#v err=%v", goal, err)
	}
}

func firstDiffIndex(left, right []byte) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			return index
		}
	}
	if len(left) != len(right) {
		return limit
	}
	return -1
}

func startLocalGoalTestThread(t *testing.T, store *session.Store) string {
	t.Helper()
	params, err := json.Marshal(appserver.ThreadStartParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("marshal thread/start params: %v", err)
	}
	router := appserver.NewRouter(store)
	defer router.Close()
	response := router.Handle(&appserver.Request{
		JSONRPC: "2.0",
		ID:      appserver.IntID(1),
		Method:  appserver.MethodThreadStart,
		Params:  params,
	})
	if response.Error != nil {
		t.Fatalf("thread/start failed: %s", response.Error.Message)
	}
	result, ok := response.Result.(*appserver.ThreadStartResponse)
	if !ok || result == nil || result.Thread == nil || result.Thread.ID == "" {
		t.Fatalf("thread/start result=%#v", response.Result)
	}
	return result.Thread.ID
}
