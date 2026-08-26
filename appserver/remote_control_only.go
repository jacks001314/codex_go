package appserver

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"codex_go/remotecontrol"
	"codex_go/session"
)

type RemoteControlOnlyOptions struct {
	CodexHome      string
	StoreRoot      string
	RuntimeOptions *RuntimeRouterOptions
	LoopOptions    *remotecontrol.RemoteControlWebsocketLoopOptions
}

func ServeRemoteControlOnly(ctx context.Context, options *RemoteControlOnlyOptions) error {
	if options == nil {
		options = &RemoteControlOnlyOptions{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	codexHome := strings.TrimSpace(options.CodexHome)
	if codexHome == "" {
		codexHome = ".gcode"
	}
	storeRoot := strings.TrimSpace(options.StoreRoot)
	if storeRoot == "" {
		storeRoot = filepath.Join(codexHome, "sessions")
	}
	preparedRuntimeOptions, ownedStateRuntime, err := prepareSharedStateRuntime(ctx, codexHome, options.RuntimeOptions)
	if err != nil {
		return err
	}
	if ownedStateRuntime != nil {
		defer ownedStateRuntime.Close()
	}
	if preparedRuntimeOptions.logDBInstallation != nil {
		defer preparedRuntimeOptions.logDBInstallation.Close(context.Background())
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(storeRoot), codexHome, preparedRuntimeOptions)
	defer router.Close()
	if err := router.StartupError(); err != nil {
		return err
	}
	manager := router.requireRemote()
	if manager.StatusChanged().Status == remotecontrol.StatusDisabled {
		manager.PublishConnectionStatus(remotecontrol.StatusConnecting)
	}
	loopOptions := remoteControlOnlyLoopOptions(options.LoopOptions, router.authRevisionSnapshot)
	loop := remotecontrol.NewRemoteControlWebsocketLoop(manager, loopOptions)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	go func() {
		errCh <- loop.Run(runCtx)
	}()
	go func() {
		errCh <- ServeRemoteControlTransport(runCtx, router, loop.TransportEvents())
	}()

	err = <-errCh
	cancel()
	waitForRemoteControlOnlyPeer(errCh, 2*time.Second)
	return err
}

func remoteControlOnlyLoopOptions(options *remotecontrol.RemoteControlWebsocketLoopOptions, authRevision remotecontrol.RemoteControlAuthRevisionFunc) *remotecontrol.RemoteControlWebsocketLoopOptions {
	if authRevision == nil {
		return options
	}
	if options == nil {
		return &remotecontrol.RemoteControlWebsocketLoopOptions{AuthRevision: authRevision}
	}
	clone := *options
	if clone.AuthRevision == nil {
		clone.AuthRevision = authRevision
	}
	return &clone
}

func waitForRemoteControlOnlyPeer(errCh <-chan error, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-errCh:
	case <-timer.C:
	}
}
