package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"codex_go/appserver"
	appserverdaemon "codex_go/appserverdaemon"
	"codex_go/model"
)

// interactiveRemoteModelProviderList browses the connected app server's
// provider catalog (Rust #59541 app::provider). It opens a short-lived session
// so browsing never disturbs the running TUI connection.
func interactiveRemoteModelProviderList(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) func() (model.ProviderListResponse, error) {
	return func() (model.ProviderListResponse, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return model.ProviderListResponse{}, errors.New("Reconnect to the app server to browse model providers.")
		}
		defer client.close()
		var response model.ProviderListResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodModelProviderList, model.ProviderListParams{}, &response); err != nil {
			return model.ProviderListResponse{}, remoteTUIModelProviderListError(err)
		}
		return response, nil
	}
}

// remoteTUIModelProviderListError mirrors Rust provider::request_error: an
// older app server that does not know the method gets an update hint.
func remoteTUIModelProviderListError(err error) error {
	var rpcErr *remoteRPCError
	if errors.As(err, &rpcErr) && rpcErr != nil {
		method := string(appserver.MethodModelProviderList)
		if rpcErr.Code == -32601 ||
			(rpcErr.Code == -32600 && strings.HasPrefix(rpcErr.Message, fmt.Sprintf("Invalid request: unknown variant `%s`", method))) {
			return errors.New("Update the connected app server to browse model providers.")
		}
	}
	return fmt.Errorf("Could not load model providers: %w", err)
}
