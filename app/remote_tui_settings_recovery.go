package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"codex_go/appserver"
)

// remoteTUIUpdateThreadSettings applies a thread setting and recovers a thread
// that the app server has evicted after its idle-unload delay. The server
// reports an evicted runtime as "thread not found"; resuming the persisted
// thread first restores the runtime so the setting can be applied.
func remoteTUIUpdateThreadSettings(ctx context.Context, client *remoteAppServerTUIClient, params appserver.SettingsUpdateParams) error {
	err := remoteTUISendThreadSettingsUpdate(ctx, client, params)
	if err == nil || !remoteTUIThreadSettingsThreadMissing(err, params.ThreadID) {
		return err
	}
	var resumed appserver.ThreadResumeResponse
	if resumeErr := remoteSessionRequest(ctx, client, appserver.MethodThreadResume, appserver.ThreadResumeParams{
		ThreadID: strings.TrimSpace(params.ThreadID),
	}, &resumed); resumeErr != nil {
		return fmt.Errorf("failed to resume unloaded thread before updating settings: %w", resumeErr)
	}
	return remoteTUISendThreadSettingsUpdate(ctx, client, params)
}

func remoteTUISendThreadSettingsUpdate(ctx context.Context, client *remoteAppServerTUIClient, params appserver.SettingsUpdateParams) error {
	var response appserver.SettingsUpdateResponse
	return remoteSessionRequest(ctx, client, appserver.MethodThreadSettingsUpdate, params, &response)
}

// remoteTUIThreadSettingsThreadMissing recognizes the app server's response for
// an idle thread whose persisted session exists but whose runtime was evicted.
func remoteTUIThreadSettingsThreadMissing(err error, threadID string) bool {
	var rpc *remoteRPCError
	if !errors.As(err, &rpc) || rpc.Code != appserver.JSONRPCInvalidRequestErrorCode {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	message := strings.TrimSpace(rpc.Message)
	return message == "thread not found: "+threadID || message == "thread not loaded: "+threadID
}
