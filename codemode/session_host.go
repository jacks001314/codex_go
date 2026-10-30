package codemode

import (
	"context"
	"fmt"

	"codex_go/tool"
)

type SessionHost struct {
	runtime             *SessionRuntime
	cellExecutionLimits *CellExecutionLimits
	outputTruncation    OutputTruncation
}

func NewSessionHost(runtime *SessionRuntime) *SessionHost {
	if runtime == nil {
		runtime = NewSessionRuntime()
	}
	return &SessionHost{runtime: runtime, outputTruncation: OutputTruncationClient}
}

// OutputTruncation reports who applies output budgets for this session
// (Rust #57272). The runtime honors the setting so each observation is bounded
// before transmission.
func (h *SessionHost) OutputTruncation() OutputTruncation {
	if h == nil {
		return OutputTruncationClient
	}
	return h.outputTruncation
}

// Handle serves one host request. The trailing signal is the request's
// operation-yield token: cancelling it ends an execute or wait observation early
// while the cell keeps running (Rust #48123). A nil signal never preempts.
func (h *SessionHost) Handle(ctx context.Context, request *HostRequest, yield *tool.YieldSignal) (*HostResponse, *RuntimeResponse, error) {
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
		h.outputTruncation = OutputTruncationClient
		if request.OutputTruncation != nil {
			h.outputTruncation = *request.OutputTruncation
		}
		h.runtime.SetOutputTruncation(h.outputTruncation)
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
		started, err := h.runtime.Execute(ctx, &clamped, yield)
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
		// The wire carries the budget on the enclosing host request (Rust #57870).
		if request.MaxOutputTokens != nil {
			clamped.MaxOutputTokens = request.MaxOutputTokens
		}
		outcome, err := h.runtime.Wait(ctx, &clamped, yield)
		if err != nil {
			return nil, nil, err
		}
		return &HostResponse{Type: "wait/completed", Outcome: outcome}, nil, nil
	case "session/terminate":
		outcome, err := h.runtime.Terminate(TerminateRequest{CellID: request.CellID, MaxOutputTokens: request.MaxOutputTokens})
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
