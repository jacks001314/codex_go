package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/review"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

const interactiveReviewConnectionID = "local-tui-review"

type interactiveReviewRouter interface {
	Handle(request *appserver.Request) *appserver.Response
	SetNotificationSink(sink appserver.NotificationSink)
	Close() error
}

type interactiveReviewRouterFactory func() interactiveReviewRouter

func interactiveLocalReviewStartCommand(ctx context.Context, state *codextui.State, interrupts *interactiveInterruptController, factory interactiveReviewRouterFactory) codextea.ReviewStartCommandFunc {
	if factory == nil {
		factory = func() interactiveReviewRouter {
			return appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
		}
	}
	return func(params review.StartParams) bubbletea.Cmd {
		return func() bubbletea.Msg {
			messages := make(chan bubbletea.Msg, 256)
			go runInteractiveLocalReview(ctx, state, interrupts, factory, params, messages)
			return codextea.StreamStartedMsg{Messages: messages}
		}
	}
}

func runInteractiveLocalReview(ctx context.Context, state *codextui.State, interrupts *interactiveInterruptController, factory interactiveReviewRouterFactory, params review.StartParams, messages chan<- bubbletea.Msg) {
	defer close(messages)
	if ctx == nil {
		ctx = context.Background()
	}
	reviewCtx := ctx
	doneInterrupt := func() {}
	if interrupts != nil {
		reviewCtx, doneInterrupt = interrupts.begin(ctx)
	}
	defer doneInterrupt()

	router := factory()
	if router == nil {
		messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Err: errors.New("review runtime is unavailable")}
		return
	}
	defer router.Close()
	if err := initializeLocalTUIConnection(router.Handle, interactiveReviewConnectionID); err != nil {
		messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Err: err}
		return
	}

	completed := make(chan struct{}, 1)
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	router.SetNotificationSink(appserver.NotificationSinkFunc(func(notification *appserver.Notification) {
		if notification == nil {
			return
		}
		raw, err := json.Marshal(notification.Params)
		if err != nil {
			return
		}
		_ = client.handleNotification(remoteAppServerMessage{Method: string(notification.Method), Params: raw})
		if notification.Method == appserver.NotificationTurnCompleted && reviewNotificationThreadID(notification.Params) == strings.TrimSpace(params.ThreadID) {
			select {
			case completed <- struct{}{}:
			default:
			}
		}
	}))

	response, err := localReviewStart(router, params)
	if err != nil {
		messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Err: err}
		return
	}
	messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Response: response}

	select {
	case <-completed:
		if !client.turnInterrupted {
			messages <- codextea.TurnCompletedMsg{ThreadID: strings.TrimSpace(params.ThreadID)}
		}
	case <-reviewCtx.Done():
		_ = localReviewInterrupt(router, params.ThreadID, response.Turn.ID)
	}
}

func localReviewStart(router interactiveReviewRouter, params review.StartParams) (review.StartResponse, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return review.StartResponse{}, err
	}
	response := router.Handle(&appserver.Request{
		JSONRPC:      "2.0",
		ID:           appserver.IntID(2),
		Method:       appserver.MethodReviewStart,
		Params:       raw,
		ConnectionID: interactiveReviewConnectionID,
	})
	if response == nil {
		return review.StartResponse{}, errors.New("review/start returned no response")
	}
	if response.Error != nil {
		return review.StartResponse{}, errors.New(strings.TrimSpace(response.Error.Message))
	}
	switch value := response.Result.(type) {
	case *review.StartResponse:
		if value == nil {
			return review.StartResponse{}, errors.New("review/start returned an empty result")
		}
		return *value, nil
	case review.StartResponse:
		return value, nil
	default:
		return review.StartResponse{}, errors.New("review/start returned an invalid result")
	}
}

func localReviewInterrupt(router interactiveReviewRouter, threadID string, turnID string) error {
	raw, err := json.Marshal(turn.TurnInterruptParams{ThreadID: strings.TrimSpace(threadID), TurnID: strings.TrimSpace(turnID)})
	if err != nil {
		return err
	}
	response := router.Handle(&appserver.Request{
		JSONRPC:      "2.0",
		ID:           appserver.IntID(3),
		Method:       appserver.MethodTurnInterrupt,
		Params:       raw,
		ConnectionID: interactiveReviewConnectionID,
	})
	if response == nil {
		return errors.New("turn/interrupt returned no response")
	}
	if response.Error != nil {
		return errors.New(strings.TrimSpace(response.Error.Message))
	}
	return nil
}

func reviewNotificationThreadID(params any) string {
	switch value := params.(type) {
	case *appserver.TurnCompletedNotification:
		if value != nil {
			return strings.TrimSpace(value.ThreadID)
		}
	case appserver.TurnCompletedNotification:
		return strings.TrimSpace(value.ThreadID)
	}
	return ""
}

func interactiveRemoteReviewStartCommand(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, brokers remoteTUIBrokers, interrupts *remoteTUIInterruptController) codextea.ReviewStartCommandFunc {
	return func(params review.StartParams) bubbletea.Cmd {
		return func() bubbletea.Msg {
			messages := make(chan bubbletea.Msg, 256)
			go runInteractiveRemoteReview(ctx, root, endpoint, state, brokers, interrupts, params, messages)
			return codextea.StreamStartedMsg{Messages: messages}
		}
	}
}

func runInteractiveRemoteReview(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, brokers remoteTUIBrokers, interrupts *remoteTUIInterruptController, params review.StartParams, messages chan<- bubbletea.Msg) {
	defer close(messages)
	if ctx == nil {
		ctx = context.Background()
	}
	client := &remoteAppServerTUIClient{
		endpoint: endpoint,
		root:     root,
		state:    state,
		messages: messages,
		brokers:  brokers,
		dial:     websocket.Dial,
	}
	if err := client.connect(ctx); err != nil {
		messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Err: err}
		return
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Err: err}
		return
	}
	id, err := client.sendRequest(ctx, appserver.MethodReviewStart, params)
	if err != nil {
		messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Err: err}
		return
	}
	var response review.StartResponse
	if err := client.waitResponse(ctx, id, &response); err != nil {
		messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Err: err}
		return
	}
	turnID := strings.TrimSpace(response.Turn.ID)
	if turnID == "" {
		messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Err: errors.New("review/start response did not include a turn id")}
		return
	}
	messages <- codextea.ReviewStartResultMsg{Target: reviewChatTarget(params.Target), Response: response}
	if interrupts != nil {
		interrupts.setActive(params.ThreadID, turnID)
		defer interrupts.clearActive(params.ThreadID, turnID)
	}
	if err := client.readUntilTurnCompleted(ctx); err != nil {
		sendRemoteTurnError(messages, err)
		return
	}
	if !client.turnInterrupted {
		messages <- codextea.TurnCompletedMsg{ThreadID: strings.TrimSpace(params.ThreadID)}
	}
}

func reviewChatTarget(target review.APITarget) chatwidget.ReviewTarget {
	switch strings.TrimSpace(target.Type) {
	case "base", "baseBranch":
		return chatwidget.ReviewTarget{Kind: chatwidget.ReviewTargetBaseBranch, Branch: firstNonEmptyLocal(strings.TrimSpace(target.Branch), strings.TrimSpace(target.Base))}
	case "commit":
		title := ""
		if target.Title != nil {
			title = strings.TrimSpace(*target.Title)
		}
		return chatwidget.ReviewTarget{Kind: chatwidget.ReviewTargetCommit, SHA: firstNonEmptyLocal(strings.TrimSpace(target.SHA), strings.TrimSpace(target.Commit)), Title: title}
	case "custom":
		return chatwidget.ReviewTarget{Kind: chatwidget.ReviewTargetCustom, Instructions: firstNonEmptyLocal(strings.TrimSpace(target.Instructions), strings.TrimSpace(target.Prompt))}
	default:
		return chatwidget.ReviewTarget{Kind: chatwidget.ReviewTargetUncommitted}
	}
}
