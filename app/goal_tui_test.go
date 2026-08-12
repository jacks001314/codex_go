package app

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"codex_go/appserver"
	"codex_go/session"
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
	read, set, clear := interactiveLocalGoalCallbacks(func() interactiveGoalRouter { return router })
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
	wantPairs := []struct {
		init appserver.Method
		op   appserver.Method
	}{
		{appserver.MethodInitialize, appserver.MethodThreadGoalGet},
		{appserver.MethodInitialize, appserver.MethodThreadGoalSet},
		{appserver.MethodInitialize, appserver.MethodThreadGoalClear},
	}
	if len(router.requests) != 2*len(wantPairs) || router.closed != len(wantPairs) {
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
	read, set, clear := interactiveLocalGoalCallbacks(factory)

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
	goal, err = read(threadID)
	if err != nil || goal != nil {
		t.Fatalf("read after clear goal=%#v err=%v", goal, err)
	}
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
