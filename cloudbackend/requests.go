package cloudbackend

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"codex_go/appserver"
	"codex_go/cloudclient"
	"codex_go/turn"
)

// handleRequest maps one app-server request onto the native cloud client.
func (c *Client) handleRequest(ctx context.Context, method appserver.Method, params json.RawMessage) (json.RawMessage, *RPCError) {
	switch method {
	case appserver.MethodGetAccount:
		if len(c.account) == 0 {
			return json.RawMessage("null"), nil
		}
		return cloneRawMessage(c.account), nil
	case appserver.MethodThreadRead:
		return c.readThread(params)
	case appserver.MethodThreadLoadedList:
		return c.loadedThreadList()
	case appserver.MethodThreadUnsubscribe:
		return c.unsubscribeThread(params)
	case appserver.MethodThreadResume:
		return c.resumeThread(ctx, params)
	case appserver.MethodTurnStart:
		// A turn is admitted only while its conversation's attachment is
		// connected, even though sending it is not supported yet.
		if err := c.admitTurnRequest(params); err != nil {
			return nil, internalRPCError(err)
		}
		return nil, unsupportedOperation(method)
	default:
		return nil, unsupportedOperation(method)
	}
}

func (c *Client) resumeThread(ctx context.Context, params json.RawMessage) (json.RawMessage, *RPCError) {
	var resume appserver.ThreadResumeParams
	if err := json.Unmarshal(params, &resume); err != nil {
		return nil, internalRPCError(err)
	}
	supported := appserver.ThreadResumeParams{ThreadID: resume.ThreadID, ExcludeTurns: true}
	if !reflect.DeepEqual(resume, supported) {
		return nil, internalRPCError(errors.New("Cloud conversations resume by ID with saved settings. Set excludeTurns to true and load history separately."))
	}
	if strings.TrimSpace(resume.ThreadID) == "" {
		return nil, internalRPCError(errors.New("threadId is required"))
	}
	activation, err := waitWhileListening(c, ctx, func() (*cloudclient.ThreadActivation, error) {
		return c.source.Activate(ctx, resume.ThreadID)
	})
	if err != nil {
		return nil, internalRPCError(err)
	}
	settings, err := resolveThreadResumeResponse(activation)
	if err != nil {
		return nil, internalRPCError(err)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, internalRPCError(err)
	}
	if err := c.replaceAttachment(ctx, resume.ThreadID); err != nil {
		return nil, internalRPCError(err)
	}
	c.loaded = settings
	return encoded, nil
}

// resolveThreadResumeResponse converts the cloud's resolved settings into the
// app-server resume shape; anything the protocol cannot express is rejected.
func resolveThreadResumeResponse(activation *cloudclient.ThreadActivation) (*appserver.ThreadResumeResponse, error) {
	unsupported := errors.New("The cloud returned unsupported conversation settings.")
	if activation == nil {
		return nil, unsupported
	}
	raw, err := json.Marshal(activation)
	if err != nil {
		return nil, unsupported
	}
	var settings appserver.ThreadResumeResponse
	if err := json.Unmarshal(raw, &settings); err != nil || settings.Thread == nil {
		return nil, unsupported
	}
	return &settings, nil
}

func (c *Client) readThread(params json.RawMessage) (json.RawMessage, *RPCError) {
	var read appserver.ThreadReadParams
	if err := json.Unmarshal(params, &read); err != nil {
		return nil, internalRPCError(err)
	}
	if read.IncludeTurns {
		return nil, internalRPCError(errors.New("Use paginated history for cloud conversations."))
	}
	if c.loaded == nil || c.loaded.Thread == nil || c.loaded.Thread.ID != read.ThreadID {
		return nil, internalRPCError(errors.New("Open this cloud conversation before reading its transcript."))
	}
	thread := *c.loaded.Thread
	if status, ok := c.status[read.ThreadID]; ok && status != nil {
		if encoded, err := json.Marshal(status); err == nil {
			var decoded appserver.ThreadStatus
			if json.Unmarshal(encoded, &decoded) == nil {
				thread.Status = decoded
			}
		}
	}
	encoded, err := json.Marshal(&appserver.ThreadReadResponse{Thread: &thread})
	if err != nil {
		return nil, internalRPCError(err)
	}
	return encoded, nil
}

func (c *Client) loadedThreadList() (json.RawMessage, *RPCError) {
	ids := []string{}
	if c.loaded != nil && c.loaded.Thread != nil {
		ids = append(ids, c.loaded.Thread.ID)
	}
	encoded, err := json.Marshal(&appserver.ThreadLoadedListResponse{Data: ids})
	if err != nil {
		return nil, internalRPCError(err)
	}
	return encoded, nil
}

func (c *Client) unsubscribeThread(params json.RawMessage) (json.RawMessage, *RPCError) {
	var request appserver.ThreadUnsubscribeParams
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, internalRPCError(err)
	}
	status := appserver.ThreadUnsubscribeStatusNotSubscribed
	if c.loaded != nil && c.loaded.Thread != nil && c.loaded.Thread.ID == request.ThreadID {
		c.detach(request.ThreadID)
		c.loaded = nil
		status = appserver.ThreadUnsubscribeStatusUnsubscribed
	}
	encoded, err := json.Marshal(appserver.ThreadUnsubscribeResponse{Status: status})
	if err != nil {
		return nil, internalRPCError(err)
	}
	return encoded, nil
}

func (c *Client) admitTurnRequest(params json.RawMessage) error {
	var request turn.TurnStartParams
	if err := json.Unmarshal(params, &request); err != nil {
		return err
	}
	return c.admitTurn(request.ThreadID)
}

func unsupportedOperation(method appserver.Method) *RPCError {
	return &RPCError{Code: methodNotFoundCode, Message: string(method) + " is not supported by the cloud backend yet."}
}

func internalRPCError(err error) *RPCError {
	if err == nil {
		return nil
	}
	return &RPCError{Code: internalErrorCode, Message: err.Error()}
}
