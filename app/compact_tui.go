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
	return localCompactRequest(router, appserver.IntID(1), appserver.MethodThreadResume, appserver.ThreadResumeParams{ThreadID: strings.TrimSpace(threadID)})
}

func localCompactStart(router interactiveCompactRouter, threadID string) error {
	return localCompactRequest(router, appserver.IntID(2), appserver.MethodThreadCompactStart, appserver.ThreadCompactStartParams{ThreadID: strings.TrimSpace(threadID)})
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
	id, err := client.sendRequest(ctx, appserver.MethodThreadCompactStart, appserver.ThreadCompactStartParams{ThreadID: strings.TrimSpace(threadID)})
	if err != nil {
		messages <- codextea.CompactStartResultMsg{Err: err}
		return
	}
	var response appserver.ThreadCompactStartResponse
	if err := client.waitResponse(ctx, id, &response); err != nil {
		messages <- codextea.CompactStartResultMsg{Err: err}
		return
	}
	messages <- codextea.CompactStartResultMsg{}
}
