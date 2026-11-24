package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"codex_go/appserver"
	appserverdaemon "codex_go/appserverdaemon"
	"codex_go/config"
	"codex_go/model"
	codextea "codex_go/tui/tea"
)

// interactiveRemoteDefaultModelProvider resolves the provider a new thread
// uses on the connected app server (Rust #63553 startup_catalog_provider).
// Servers from 0.169 answer provider/list with their default provider id;
// Cloud, older servers, and failed lookups keep the configured provider.
func interactiveRemoteDefaultModelProvider(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) string {
	if endpoint == nil || threadParamsModeForEndpoint(endpoint) == ThreadParamsModeCloud {
		return ""
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return ""
	}
	defer client.close()
	if !remoteServerSupportsProviderCatalog(client.serverVersion) {
		return ""
	}
	var response model.ProviderListResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodModelProviderList, model.ProviderListParams{}, &response); err != nil {
		return ""
	}
	return strings.TrimSpace(response.DefaultProviderID)
}

// remoteServerSupportsProviderCatalog reports whether a server advertises
// modelProvider/list without ignoring the selector (Rust 0.169.0).
func remoteServerSupportsProviderCatalog(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	if index := strings.IndexAny(version, "-+"); index >= 0 {
		version = version[:index]
	}
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	numbers := [3]int{}
	for i := 0; i < len(parts) && i < len(numbers); i++ {
		value, err := strconv.Atoi(parts[i])
		if err != nil || value < 0 {
			return false
		}
		numbers[i] = value
	}
	for i, threshold := range [3]int{0, 169, 0} {
		if numbers[i] != threshold {
			return numbers[i] > threshold
		}
	}
	return true
}

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

// interactiveRemoteModelProviderWriter applies a provider patch through the
// connected app server.
func interactiveRemoteModelProviderWriter(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.ModelProviderConfigWriteFunc {
	return func(params model.ProviderConfigWriteParams) (model.ProviderConfigWriteResponse, error) {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return model.ProviderConfigWriteResponse{}, err
		}
		defer client.close()
		var response model.ProviderConfigWriteResponse
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodModelProviderConfigWrite, params, &response); err != nil {
			return model.ProviderConfigWriteResponse{}, fmt.Errorf("modelProvider/config/write failed: %w", err)
		}
		return response, nil
	}
}

// interactiveRemoteModelProviderDefaultSetter persists the new-chat provider
// through the connected app server's dedicated method (Rust #61438). An older
// server reports the method as unsupported so the TUI can prompt for an update,
// while a lost reply stays retryable without claiming the write failed.
func interactiveRemoteModelProviderDefaultSetter(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.ModelProviderDefaultSetFunc {
	return func(params model.ProviderDefaultSetParams) (config.ConfigWriteResponse, error) {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return config.ConfigWriteResponse{}, fmt.Errorf("%w: %v", codextea.ErrModelProviderDefaultUnconfirmed, err)
		}
		defer client.close()
		var response config.ConfigWriteResponse
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodModelProviderDefaultSet, params, &response); err != nil {
			if remoteTUIModelProviderDefaultUnsupported(err) {
				return config.ConfigWriteResponse{}, codextea.ErrModelProviderDefaultUnsupported
			}
			var rpcErr *remoteRPCError
			if errors.As(err, &rpcErr) && rpcErr != nil {
				return config.ConfigWriteResponse{}, err
			}
			return config.ConfigWriteResponse{}, fmt.Errorf("%w: %v", codextea.ErrModelProviderDefaultUnconfirmed, err)
		}
		return response, nil
	}
}

// remoteTUIModelProviderDefaultUnsupported reports an app server that predates
// modelProvider/default/set.
func remoteTUIModelProviderDefaultUnsupported(err error) bool {
	var rpcErr *remoteRPCError
	if !errors.As(err, &rpcErr) || rpcErr == nil {
		return false
	}
	method := string(appserver.MethodModelProviderDefaultSet)
	return rpcErr.Code == -32601 ||
		(rpcErr.Code == -32600 && strings.HasPrefix(rpcErr.Message, fmt.Sprintf("Invalid request: unknown variant `%s`", method)))
}
