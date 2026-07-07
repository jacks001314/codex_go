package codemode

import (
	"context"
	"encoding/json"

	"codex_go/internal/tool"
)

type WaitHandler struct {
	cells *CellStore
}

func NewWaitHandler(cells *CellStore) *WaitHandler {
	if cells == nil {
		cells = NewCellStore()
	}
	return &WaitHandler{cells: cells}
}

func (h *WaitHandler) Spec() tool.Spec {
	return tool.Spec{Name: tool.PlainName(WaitToolName), Description: BuildWaitToolDescription()}
}

func (h *WaitHandler) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	_ = ctx
	params, err := ParseWaitParams(invocation.Payload.Arguments)
	if err != nil {
		return nil, err
	}
	var cell *Cell
	if params.Terminate {
		cell, err = h.cells.Terminate(params.CellID)
	} else {
		var ok bool
		cell, ok = h.cells.Get(params.CellID)
		if !ok {
			err = ErrCellNotFound
		}
	}
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(cell)
	if err != nil {
		return nil, err
	}
	return &tool.Output{Success: true, Body: string(body)}, nil
}
