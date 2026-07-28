package app

import (
	"encoding/json"
	"testing"

	"codex_go/appserver"
)

type recordingInteractiveGoalRouter struct {
	requests []*appserver.Request
	closed   int
}

func (r *recordingInteractiveGoalRouter) Handle(request *appserver.Request) *appserver.Response {
	r.requests = append(r.requests, request)
	switch request.Method {
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
	methods := []appserver.Method{appserver.MethodThreadGoalGet, appserver.MethodThreadGoalSet, appserver.MethodThreadGoalClear}
	if len(router.requests) != len(methods) || router.closed != len(methods) {
		t.Fatalf("requests=%d closed=%d", len(router.requests), router.closed)
	}
	for i, method := range methods {
		if router.requests[i].Method != method || router.requests[i].ConnectionID != interactiveGoalConnectionID {
			t.Fatalf("request[%d]=%#v", i, router.requests[i])
		}
	}
}
