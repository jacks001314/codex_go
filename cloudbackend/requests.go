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
func (c *Client) handleRequest(
	ctx context.Context,
	method appserver.Method,
	params json.RawMessage,
	mode cloudclient.SubmissionMode,
	typedSubmission bool,
) (json.RawMessage, *RPCError) {
	switch method {
	case appserver.MethodInitialize:
		// Rust #59828: the hosted Cloud backend answers the app-server handshake
		// itself; there is no separate daemon to negotiate with.
		encoded, err := json.Marshal(appserver.NewInitializeResponse("", "codex-cloud-backend"))
		if err != nil {
			return nil, internalRPCError(err)
		}
		return encoded, nil
	case appserver.MethodGetAccount:
		if len(c.account) == 0 {
			return json.RawMessage("null"), nil
		}
		return cloneRawMessage(c.account), nil
	case appserver.MethodThreadList:
		return c.listThreads(ctx, params)
	case appserver.MethodThreadStart:
		return c.startThread(ctx, params)
	case appserver.MethodThreadRead:
		return c.readThread(params)
	case appserver.MethodThreadLoadedList:
		return c.loadedThreadList()
	case appserver.MethodThreadUnsubscribe:
		return c.unsubscribeThread(params)
	case appserver.MethodThreadResume:
		return c.resumeThread(ctx, params)
	case appserver.MethodThreadTurnsList:
		return c.listTurns(ctx, params)
	case appserver.MethodThreadItemsList:
		return c.listItems(ctx, params)
	case appserver.MethodTurnStart:
		return c.startTurn(ctx, params, mode, typedSubmission)
	case appserver.MethodTurnInterrupt:
		return c.interruptTurn(ctx, params)
	default:
		return nil, unsupportedOperation(method)
	}
}

func (c *Client) listThreads(ctx context.Context, params json.RawMessage) (json.RawMessage, *RPCError) {
	var list appserver.ThreadListParams
	if err := json.Unmarshal(params, &list); err != nil {
		return nil, internalRPCError(err)
	}
	query, err := cloudThreadListQuery(list)
	if err != nil {
		return nil, internalRPCError(err)
	}
	page, err := waitWhileListening(c, ctx, func() (cloudclient.ThreadPage, error) {
		return c.source.ListThreads(ctx, query)
	})
	if err != nil {
		return nil, internalRPCError(err)
	}
	response := appserver.ThreadListResponse{
		Data:            make([]appserver.Thread, 0, len(page.Data)),
		NextCursor:      cloneStringPointer(page.NextCursor),
		BackwardsCursor: cloneStringPointer(page.BackwardsCursor),
		ReadStates:      map[string]appserver.ThreadReadState{},
	}
	for _, snapshot := range page.Data {
		thread, err := cloudThreadSnapshot(snapshot)
		if err != nil {
			return nil, internalRPCError(err)
		}
		response.Data = append(response.Data, thread)
	}
	if len(page.ReadStates) > 0 {
		raw, err := json.Marshal(page.ReadStates)
		if err != nil {
			return nil, internalRPCError(err)
		}
		if err := json.Unmarshal(raw, &response.ReadStates); err != nil {
			return nil, internalRPCError(errors.New("The cloud returned unsupported read state."))
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, internalRPCError(err)
	}
	return encoded, nil
}

func cloudThreadListQuery(params appserver.ThreadListParams) (cloudclient.ThreadListQuery, error) {
	sources := params.SourceKinds
	validSources := len(sources) == 0 ||
		(len(sources) == 2 && ((sources[0] == appserver.ThreadSourceKindCli && sources[1] == appserver.ThreadSourceKindVsCode) ||
			(sources[0] == appserver.ThreadSourceKindVsCode && sources[1] == appserver.ThreadSourceKindCli)))
	if !validSources || (params.Archived != nil && *params.Archived) || params.CWD != nil ||
		params.SearchTerm != nil || params.SectionID.Set ||
		params.ParentThreadID != nil || params.AncestorThreadID != nil || params.AttachedToThreadID != nil ||
		len(params.ModelProviders) > 0 || (params.ExcludedThreadIDs != nil && len(*params.ExcludedThreadIDs) > 0) {
		return cloudclient.ThreadListQuery{}, errors.New("These cloud conversation filters are not supported yet.")
	}
	var limit *uint32
	if params.Limit != nil {
		if *params.Limit < 0 || uint64(*params.Limit) > uint64(^uint32(0)) {
			return cloudclient.ThreadListQuery{}, errors.New("limit must be a non-negative 32-bit integer")
		}
		value := uint32(*params.Limit)
		limit = &value
	}
	query := cloudclient.ThreadListQuery{
		Cursor:      cloneStringPointer(params.Cursor),
		Limit:       limit,
		Originators: append([]string(nil), params.Originators...),
	}
	switch params.SortKey {
	case "", appserver.SortCreatedAt:
		query.SortKey = cloudclient.ThreadSortCreatedAt
	case appserver.SortUpdatedAt:
		query.SortKey = cloudclient.ThreadSortUpdatedAt
	case appserver.SortRecencyAt:
		query.SortKey = cloudclient.ThreadSortRecencyAt
	case appserver.SortSectionPosition:
		return cloudclient.ThreadListQuery{}, errors.New("Cloud section ordering is not supported yet.")
	default:
		return cloudclient.ThreadListQuery{}, errors.New("Cloud conversation sort key is not supported yet.")
	}
	switch params.SortDirection {
	case "", appserver.SortDesc:
		query.SortDirection = cloudclient.ThreadSortDescending
	case appserver.SortAsc:
		query.SortDirection = cloudclient.ThreadSortAscending
	default:
		return cloudclient.ThreadListQuery{}, errors.New("Cloud conversation sort direction is not supported yet.")
	}
	return query, nil
}

func (c *Client) startThread(ctx context.Context, params json.RawMessage) (json.RawMessage, *RPCError) {
	var start appserver.ThreadStartParams
	if err := json.Unmarshal(params, &start); err != nil {
		return nil, internalRPCError(err)
	}
	model := start.Model
	provider := start.ModelProvider
	start.Model = ""
	start.ModelProvider = ""
	if start.Ephemeral {
		return nil, internalRPCError(errors.New("Cloud conversations start with server defaults. Custom settings are not supported yet."))
	}
	// Rust #59828: the TUI forwards its local backend settings and lets Cloud
	// own the conversation environment, so those fields are dropped instead of
	// rejecting the thread start.
	activation, err := waitWhileListening(c, ctx, func() (*cloudclient.ThreadActivation, error) {
		return c.source.PrewarmDefaultThread(ctx, model, provider)
	})
	if err != nil {
		return nil, internalRPCError(err)
	}
	startResponse, resumeResponse, err := resolveThreadActivation(activation)
	if err != nil {
		return nil, internalRPCError(err)
	}
	if err := c.replaceAttachment(ctx, startResponse.Thread.ID); err != nil {
		return nil, internalRPCError(err)
	}
	c.loaded = resumeResponse
	c.warnedPartialHistory = ""
	encoded, err := json.Marshal(startResponse)
	if err != nil {
		return nil, internalRPCError(err)
	}
	return encoded, nil
}

func (c *Client) resumeThread(ctx context.Context, params json.RawMessage) (json.RawMessage, *RPCError) {
	var resume appserver.ThreadResumeParams
	if err := json.Unmarshal(params, &resume); err != nil {
		return nil, internalRPCError(err)
	}
	supported := appserver.ThreadResumeParams{ThreadID: resume.ThreadID, ExcludeTurns: true}
	// Rust #59828: a local working-directory override is dropped; Cloud opens
	// the thread where it was created.
	normalized := resume
	normalized.CWD = nil
	if !reflect.DeepEqual(normalized, supported) {
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
	_, settings, err := resolveThreadActivation(activation)
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
	c.warnedPartialHistory = ""
	return encoded, nil
}

func resolveThreadActivation(activation *cloudclient.ThreadActivation) (*appserver.ThreadStartResponse, *appserver.ThreadResumeResponse, error) {
	unsupported := errors.New("The cloud returned unsupported conversation settings.")
	if activation == nil {
		return nil, nil, unsupported
	}
	normalized := *activation
	normalized.Thread = activation.Thread
	normalized.Thread.HistoryMode = "paginated"
	raw, err := json.Marshal(&normalized)
	if err != nil {
		return nil, nil, unsupported
	}
	var start appserver.ThreadStartResponse
	if err := json.Unmarshal(raw, &start); err != nil || start.Thread == nil || strings.TrimSpace(start.Thread.ID) == "" {
		return nil, nil, unsupported
	}
	var resume appserver.ThreadResumeResponse
	if err := json.Unmarshal(raw, &resume); err != nil || resume.Thread == nil {
		return nil, nil, unsupported
	}
	return &start, &resume, nil
}

func resolveThreadResumeResponse(activation *cloudclient.ThreadActivation) (*appserver.ThreadResumeResponse, error) {
	_, resume, err := resolveThreadActivation(activation)
	return resume, err
}

func cloudThreadSnapshot(snapshot cloudclient.ThreadSnapshot) (appserver.Thread, error) {
	snapshot.HistoryMode = "paginated"
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return appserver.Thread{}, errors.New("The cloud returned an unsupported thread.")
	}
	var thread appserver.Thread
	if err := json.Unmarshal(raw, &thread); err != nil || strings.TrimSpace(thread.ID) == "" {
		return appserver.Thread{}, errors.New("The cloud returned an unsupported thread.")
	}
	return thread, nil
}

func (c *Client) listTurns(ctx context.Context, params json.RawMessage) (json.RawMessage, *RPCError) {
	var list appserver.ThreadTurnsListParams
	if err := json.Unmarshal(params, &list); err != nil {
		return nil, internalRPCError(err)
	}
	if list.ItemsView != appserver.TurnItemsNotLoaded {
		return nil, internalRPCError(errors.New("Read cloud turn items separately with thread/items/list."))
	}
	limit, err := cloudLimit(list.Limit)
	if err != nil {
		return nil, internalRPCError(err)
	}
	direction := list.SortDirection
	if direction == "" {
		direction = appserver.SortDesc
	}
	order, err := cloudHistoryOrder(direction)
	if err != nil {
		return nil, internalRPCError(err)
	}
	page, err := waitWhileListening(c, ctx, func() (cloudclient.HistoryPage[cloudclient.HistoryTurn], error) {
		return c.source.ListTurns(ctx, cloudclient.ListTurnsRequest{
			ThreadID: list.ThreadID,
			Page: cloudclient.HistoryQuery{
				Cursor: cloneStringPointer(list.Cursor), Limit: limit, Order: order,
			},
		})
	})
	if err != nil {
		return nil, internalRPCError(err)
	}
	response := appserver.TurnsPage{
		Data:            make([]appserver.Turn, 0, len(page.Data)),
		NextCursor:      cloneStringPointer(page.NextCursor),
		BackwardsCursor: cloneStringPointer(page.BackwardsCursor),
	}
	for _, entry := range page.Data {
		value, err := cloudTurn(entry.Summary)
		if err != nil {
			return nil, internalRPCError(err)
		}
		response.Data = append(response.Data, value)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, internalRPCError(err)
	}
	return encoded, nil
}

func (c *Client) listItems(ctx context.Context, params json.RawMessage) (json.RawMessage, *RPCError) {
	var list appserver.ThreadItemsListParams
	if err := json.Unmarshal(params, &list); err != nil {
		return nil, internalRPCError(err)
	}
	var cursor *string
	if list.Cursor != nil {
		if list.Cursor.Anchor != nil {
			return nil, internalRPCError(errors.New("Cloud item anchors are not supported yet."))
		}
		cursor = cloneStringPointer(&list.Cursor.Opaque)
	}
	limit, err := cloudLimit(list.Limit)
	if err != nil {
		return nil, internalRPCError(err)
	}
	direction := list.SortDirection
	if direction == "" {
		direction = appserver.SortAsc
	}
	order, err := cloudHistoryOrder(direction)
	if err != nil {
		return nil, internalRPCError(err)
	}
	page, err := waitWhileListening(c, ctx, func() (cloudclient.HistoryPage[cloudclient.HistoryItem], error) {
		return c.source.ListItems(ctx, cloudclient.ListItemsRequest{
			ThreadID: list.ThreadID,
			TurnID:   cloneStringPointer(list.TurnID),
			Page: cloudclient.HistoryQuery{
				Cursor: cursor, Limit: limit, Order: order,
			},
		})
	})
	if err != nil {
		return nil, internalRPCError(err)
	}
	response := appserver.ThreadItemsListResponse{
		Data:            make([]appserver.ThreadItemEntry, 0, len(page.Data)),
		NextCursor:      cloneStringPointer(page.NextCursor),
		BackwardsCursor: cloneStringPointer(page.BackwardsCursor),
	}
	for _, entry := range page.Data {
		if entry.Summary == nil {
			continue
		}
		item, ok := threadItemFromSummary(*entry.Summary)
		if !ok {
			continue
		}
		response.Data = append(response.Data, appserver.ThreadItemEntry{
			TurnID:        entry.TurnID,
			Item:          item,
			StartedAtMS:   cloneInt64Pointer(entry.StartedAtMS),
			CompletedAtMS: cloneInt64Pointer(entry.CompletedAtMS),
		})
	}
	if len(response.Data) != len(page.Data) && c.warnedPartialHistory != list.ThreadID {
		c.warnedPartialHistory = list.ThreadID
		threadID := list.ThreadID
		c.enqueue(Notification{
			Method: appserver.NotificationWarning,
			Params: appserver.WarningNotification{
				ThreadID: &threadID,
				Message:  "Some Cloud history items cannot be displayed in this preview.",
			},
		})
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, internalRPCError(err)
	}
	return encoded, nil
}

func (c *Client) startTurn(ctx context.Context, params json.RawMessage, mode cloudclient.SubmissionMode, typed bool) (json.RawMessage, *RPCError) {
	var request turn.TurnStartParams
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, submissionRPCError(err, typed)
	}
	if err := c.admitTurn(request.ThreadID); err != nil {
		return nil, submissionRPCError(err, typed)
	}
	message, err := cloudMessage(request)
	if err != nil {
		return nil, submissionRPCError(err, typed)
	}
	message.Mode = mode
	admission, err := waitWhileListening(c, ctx, func() (*cloudclient.Admission, error) {
		return c.source.AddUserMessage(ctx, message)
	})
	if err != nil {
		return nil, submissionRPCError(err, typed)
	}
	if mode == cloudclient.SubmissionSteer {
		receipt, err := admittedReceipt(admission.Outcome)
		if err != nil {
			return nil, submissionRPCError(err, typed)
		}
		encoded, err := json.Marshal(receipt)
		if err != nil {
			return nil, submissionRPCError(err, typed)
		}
		return encoded, nil
	}
	value, err := admittedTurn(admission.Outcome)
	if err != nil {
		return nil, submissionRPCError(err, typed)
	}
	encoded, err := json.Marshal(struct {
		Turn appserver.Turn `json:"turn"`
	}{Turn: value})
	if err != nil {
		return nil, submissionRPCError(err, typed)
	}
	return encoded, nil
}

func (c *Client) interruptTurn(ctx context.Context, params json.RawMessage) (json.RawMessage, *RPCError) {
	var interrupt turn.TurnInterruptParams
	if err := json.Unmarshal(params, &interrupt); err != nil {
		return nil, internalRPCError(err)
	}
	_, err := waitWhileListening(c, ctx, func() (struct{}, error) {
		return struct{}{}, c.source.InterruptTurn(ctx, cloudclient.InterruptTurnRequest{
			ThreadID: interrupt.ThreadID, TurnID: interrupt.TurnID,
		})
	})
	if err != nil {
		return nil, internalRPCError(err)
	}
	return json.RawMessage(`{}`), nil
}

func cloudLimit(limit *int) (*uint32, error) {
	if limit == nil {
		return nil, nil
	}
	if *limit < 0 || uint64(*limit) > uint64(^uint32(0)) {
		return nil, errors.New("limit must be a non-negative 32-bit integer")
	}
	value := uint32(*limit)
	return &value, nil
}

func cloudHistoryOrder(direction appserver.SortDirection) (cloudclient.HistoryOrder, error) {
	switch direction {
	case appserver.SortAsc:
		return cloudclient.HistoryAscending, nil
	case appserver.SortDesc:
		return cloudclient.HistoryDescending, nil
	default:
		return 0, errors.New("Cloud history sort direction is not supported yet.")
	}
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

func submissionRPCError(err error, typed bool) *RPCError {
	if typed {
		return typedSubmissionError(err)
	}
	return internalRPCError(err)
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

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
