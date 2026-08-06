package codemode

import (
	"context"
	"fmt"
)

type SessionHost struct {
	runtime             *SessionRuntime
	cellExecutionLimits *CellExecutionLimits
}

func NewSessionHost(runtime *SessionRuntime) *SessionHost {
	if runtime == nil {
		runtime = NewSessionRuntime()
	}
	return &SessionHost{runtime: runtime}
}

func (h *SessionHost) Handle(ctx context.Context, request *HostRequest) (*HostResponse, *RuntimeResponse, error) {
	if h == nil || h.runtime == nil {
		return nil, nil, fmt.Errorf("code mode session host is nil")
	}
	if request == nil {
		return nil, nil, fmt.Errorf("host request is nil")
	}
	switch request.Method {
	case "session/open":
		// Rust 9d00bb01c0 (#37114): the session's cell execution limits are
		// captured at open and applied to every later execute/wait yield time
		// without terminating the running cell.
		h.cellExecutionLimits = request.CellExecutionLimits
		return &HostResponse{Type: "session/ready", SessionID: request.SessionID}, nil, nil
	case "session/execute":
		if request.Request == nil {
			return nil, nil, fmt.Errorf("session/execute request is required")
		}
		clamped := *request.Request
		if request.Request.YieldTimeMS != nil {
			value := h.cellExecutionLimits.ClampYieldTimeMS(*request.Request.YieldTimeMS)
			clamped.YieldTimeMS = &value
		}
		started, err := h.runtime.Execute(ctx, &clamped)
		if err != nil {
			return nil, nil, err
		}
		response := started.InitialResponse
		return &HostResponse{Type: "execution/started", CellID: started.CellID}, &response, nil
	case "session/wait":
		if request.Wait == nil {
			return nil, nil, fmt.Errorf("session/wait request is required")
		}
		clamped := *request.Wait
		clamped.YieldTimeMS = h.cellExecutionLimits.ClampYieldTimeMS(request.Wait.YieldTimeMS)
		outcome, err := h.runtime.Wait(ctx, &clamped)
		if err != nil {
			return nil, nil, err
		}
		return &HostResponse{Type: "wait/completed", Outcome: outcome}, nil, nil
	case "session/terminate":
		outcome, err := h.runtime.Terminate(request.CellID)
		if err != nil {
			return nil, nil, err
		}
		return &HostResponse{Type: "wait/completed", Outcome: outcome}, nil, nil
	case "session/shutdown":
		h.runtime.Shutdown()
		return &HostResponse{Type: "session/closed", SessionID: request.SessionID}, nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown code mode host method %s", request.Method)
	}
}
