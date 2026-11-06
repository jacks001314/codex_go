package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	bubbletea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

const interactiveCompactConnectionID = "local-tui-compact"

type interactiveCompactRouter interface {
	Handle(request *appserver.Request) *appserver.Response
	SetNotificationSink(sink appserver.NotificationSink)
	Close() error
}

type interactiveCompactRouterFactory func() interactiveCompactRouter

func interactiveLocalCompactStartCommand(ctx context.Context, state *codextui.State, factory interactiveCompactRouterFactory) codextea.CompactStartCommandFunc {
	if factory == nil {
		factory = func() interactiveCompactRouter {
			return appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
		}
	}
	return func(threadID string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			messages := make(chan bubbletea.Msg, 64)
			go runInteractiveLocalCompact(ctx, state, factory, threadID, messages)
			return codextea.StreamStartedMsg{Messages: messages}
		}
	}
}

func runInteractiveLocalCompact(ctx context.Context, state *codextui.State, factory interactiveCompactRouterFactory, threadID string, messages chan<- bubbletea.Msg) {
	defer close(messages)
	if ctx == nil {
		ctx = context.Background()
	}
	router := factory()
	if router == nil {
		messages <- codextea.CompactStartResultMsg{Err: errors.New("compaction runtime is unavailable")}
		return
	}
	defer router.Close()
	if err := initializeLocalTUIConnection(router.Handle, interactiveCompactConnectionID); err != nil {
		messages <- codextea.CompactStartResultMsg{Err: err}
		return
	}
	if err := localCompactEnsureThreadLoaded(router, threadID); err != nil {
		messages <- codextea.CompactStartResultMsg{Err: err}
		return
	}
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
	}))
	messages <- codextea.CompactStartResultMsg{Err: localCompactStart(router, threadID)}
}

func localCompactEnsureThreadLoaded(router interactiveCompactRouter, threadID string) error {
	return localCompactRequest(router, appserver.IntID(2), appserver.MethodThreadResume, appserver.ThreadResumeParams{ThreadID: strings.TrimSpace(threadID)})
}

func localCompactStart(router interactiveCompactRouter, threadID string) error {
	return localCompactRequest(router, appserver.IntID(3), appserver.MethodThreadCompactStart, appserver.ThreadCompactStartParams{ThreadID: strings.TrimSpace(threadID)})
}

func localCompactRequest(router interactiveCompactRouter, id appserver.RequestID, method appserver.Method, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	response := router.Handle(&appserver.Request{JSONRPC: "2.0", ID: id, Method: method, Params: raw, ConnectionID: interactiveCompactConnectionID})
	if response == nil {
		return errors.New(string(method) + " returned no response")
	}
	if response.Error != nil {
		return errors.New(strings.TrimSpace(response.Error.Message))
	}
	return nil
}

func interactiveRemoteCompactStartCommand(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, brokers remoteTUIBrokers) codextea.CompactStartCommandFunc {
	return func(threadID string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			messages := make(chan bubbletea.Msg, 64)
			go runInteractiveRemoteCompact(ctx, root, endpoint, state, brokers, threadID, messages)
			return codextea.StreamStartedMsg{Messages: messages}
		}
	}
}

func runInteractiveRemoteCompact(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, brokers remoteTUIBrokers, threadID string, messages chan<- bubbletea.Msg) {
	defer close(messages)
	if ctx == nil {
		ctx = context.Background()
	}
	client := &remoteAppServerTUIClient{endpoint: endpoint, root: root, state: state, messages: messages, brokers: brokers, dial: websocket.Dial}
	if err := client.connect(ctx); err != nil {
		messages <- codextea.CompactStartResultMsg{Err: err}
		return
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		messages <- codextea.CompactStartResultMsg{Err: err}
		return
	}
	if err := remoteCompactStart(ctx, client, threadID); err != nil {
		// The daemon evicts an idle thread runtime; the persisted thread must be
		// resumed before manual compaction can run against it.
		if !remoteTUIThreadSettingsThreadMissing(err, threadID) {
			messages <- codextea.CompactStartResultMsg{Err: err}
			return
		}
		var resumed appserver.ThreadResumeResponse
		if resumeErr := remoteSessionRequest(ctx, client, appserver.MethodThreadResume, appserver.ThreadResumeParams{
			ThreadID: strings.TrimSpace(threadID),
		}, &resumed); resumeErr != nil {
			messages <- codextea.CompactStartResultMsg{Err: fmt.Errorf("failed to resume unloaded thread before compacting: %w", resumeErr)}
			return
		}
		if err := remoteCompactStart(ctx, client, threadID); err != nil {
			messages <- codextea.CompactStartResultMsg{Err: err}
			return
		}
	}
	messages <- codextea.CompactStartResultMsg{}
}

func remoteCompactStart(ctx context.Context, client *remoteAppServerTUIClient, threadID string) error {
	id, err := client.sendRequest(ctx, appserver.MethodThreadCompactStart, appserver.ThreadCompactStartParams{ThreadID: strings.TrimSpace(threadID)})
	if err != nil {
		return err
	}
	var response appserver.ThreadCompactStartResponse
	return client.waitResponse(ctx, id, &response)
}
