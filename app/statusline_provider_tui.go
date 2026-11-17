package app

// Adapter between the TUI's provider footer hooks and the statusline provider
// resolver (Rust #61979 statusline_reader.rs -> ResolvedStatuslineProvider).
//
// The TUI resolves a saved provider, then snapshots its own state and asks for
// the advertised resource. Both steps run against the same app-server surface
// the built-in surfaces use: the embedded runtime router locally and the remote
// app-server session for a hosted backend.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/statuslineprovider"
	codextea "codex_go/tui/tea"
)

const interactiveStatuslineProviderConnectionID = "local-tui-statusline-provider"

// localStatuslineRequestClient issues statusline provider RPCs through the
// embedded runtime router and hands back decoded JSON objects, matching the
// statuslineprovider.AppServer contract.
type localStatuslineRequestClient struct {
	handle       func(request *appserver.Request) *appserver.Response
	connectionID string
	nextID       int
}

func (c *localStatuslineRequestClient) Request(ctx context.Context, method string, params any) (map[string]any, error) {
	if c == nil || c.handle == nil {
		return nil, errors.New("statusline provider requires an app-server connection")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	c.nextID++
	response := c.handle(&appserver.Request{
		JSONRPC:      "2.0",
		ID:           appserver.IntID(int64(c.nextID)),
		Method:       appserver.Method(method),
		Params:       body,
		ConnectionID: c.connectionID,
	})
	if response == nil {
		return nil, errors.New(method + " returned no response")
	}
	if response.Error != nil {
		message := strings.TrimSpace(response.Error.Message)
		if message == "" {
			message = method + " failed"
		}
		return nil, errors.New(message)
	}
	return jsonObject(response.Result)
}

// remoteStatuslineRequestClient issues the same RPCs against a remote
// app-server session.
type remoteStatuslineRequestClient struct {
	ctx    context.Context
	client *remoteAppServerTUIClient
}

func (c *remoteStatuslineRequestClient) Request(ctx context.Context, method string, params any) (map[string]any, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("statusline provider requires an app-server connection")
	}
	requestCtx := ctx
	if requestCtx == nil {
		requestCtx = c.ctx
	}
	var out map[string]any
	if err := remoteSessionRequest(requestCtx, c.client, appserver.Method(method), params, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func jsonObject(value any) (map[string]any, error) {
	if out, ok := value.(map[string]any); ok {
		return out, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// interactiveStatuslineLayerReader exposes the effective configuration layer
// stack for a working directory. Rust's resolver rejects providers that the
// project layer configures, so the layer origins must be complete.
func interactiveStatuslineLayerReader(root *cli.RootOptions) statuslineprovider.LayerReader {
	return func(ctx context.Context, cwd string) ([]config.Layer, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		params := &config.ConfigReadParams{IncludeLayers: true}
		if trimmed := strings.TrimSpace(cwd); trimmed != "" {
			params.CWD = &trimmed
		}
		read, err := interactiveConfigService(root).Read(params)
		if err != nil {
			return nil, err
		}
		if read == nil {
			return nil, errors.New("config/read returned no layers")
		}
		return read.Layers, nil
	}
}

// interactiveLocalStatuslineProviderHooks wires the embedded app server.
func interactiveLocalStatuslineProviderHooks(root *cli.RootOptions) (
	func(context.Context, codextea.StatuslineProviderReadRequest) (codextea.StatuslineProvider, error),
	func(context.Context, codextea.StatuslineProviderResourceRequest) ([]codextea.StatuslineProviderContent, error),
) {
	layers := interactiveStatuslineLayerReader(root)
	connect := func(ctx context.Context) (statuslineprovider.AppServer, func(), error) {
		router := appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
		if err := initializeLocalTUIConnection(router.Handle, interactiveStatuslineProviderConnectionID); err != nil {
			router.Close()
			return nil, nil, err
		}
		return &localStatuslineRequestClient{handle: router.Handle, connectionID: interactiveStatuslineProviderConnectionID},
			func() { router.Close() }, nil
	}
	return statuslineProviderHooks(layers, connect)
}

// interactiveRemoteStatuslineProviderHooks wires a remote app-server session.
func interactiveRemoteStatuslineProviderHooks(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, root *cli.RootOptions) (
	func(context.Context, codextea.StatuslineProviderReadRequest) (codextea.StatuslineProvider, error),
	func(context.Context, codextea.StatuslineProviderResourceRequest) ([]codextea.StatuslineProviderContent, error),
) {
	layers := interactiveStatuslineLayerReader(root)
	connect := func(context.Context) (statuslineprovider.AppServer, func(), error) {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		return &remoteStatuslineRequestClient{ctx: reqCtx, client: client}, func() {
			client.close()
			cancel()
		}, nil
	}
	return statuslineProviderHooks(layers, connect)
}

// statuslineProviderHooks builds the two TUI hooks over one connection factory.
// Each hook opens its own short-lived connection, so a resolve that outlives the
// displayed thread never keeps a stale session open.
func statuslineProviderHooks(
	layers statuslineprovider.LayerReader,
	connect func(context.Context) (statuslineprovider.AppServer, func(), error),
) (
	func(context.Context, codextea.StatuslineProviderReadRequest) (codextea.StatuslineProvider, error),
	func(context.Context, codextea.StatuslineProviderResourceRequest) ([]codextea.StatuslineProviderContent, error),
) {
	resolve := func(ctx context.Context, request codextea.StatuslineProviderReadRequest) (codextea.StatuslineProvider, error) {
		client, closeClient, err := connect(ctx)
		if err != nil {
			return codextea.StatuslineProvider{}, err
		}
		defer closeClient()
		id := statuslineprovider.ProviderID{Server: request.Server, Tool: request.Tool}
		resolved, err := statuslineprovider.Lookup(ctx, client, layers, request.ThreadID, request.WorkingDirectory, statuslineprovider.Selection{Saved: &id})
		if err != nil {
			return codextea.StatuslineProvider{}, err
		}
		if resolved == nil {
			return codextea.StatuslineProvider{}, nil
		}
		return codextea.StatuslineProvider{
			Found: true,
			Title: resolved.Provider.Title,
			URI:   resolved.Provider.URI,
		}, nil
	}
	read := func(ctx context.Context, request codextea.StatuslineProviderResourceRequest) ([]codextea.StatuslineProviderContent, error) {
		client, closeClient, err := connect(ctx)
		if err != nil {
			return nil, err
		}
		defer closeClient()
		raw, err := statuslineprovider.ReadResource(ctx, client, request.ThreadID, request.Server, request.URI, request.Snapshot)
		if err != nil {
			return nil, err
		}
		return statuslineProviderContents(raw), nil
	}
	return resolve, read
}

// interactiveLocalStatuslineProviderDiscovery discovers /statusline providers
// through the embedded app server (Rust #61988 refresh_statusline_providers).
func interactiveLocalStatuslineProviderDiscovery(root *cli.RootOptions) codextea.StatuslineProviderDiscoveryFunc {
	connect := func(ctx context.Context) (statuslineprovider.AppServer, func(), error) {
		router := appserver.NewDefaultRuntimeRouter(newSessionStore(), auth.DefaultCodexHome())
		if err := initializeLocalTUIConnection(router.Handle, interactiveStatuslineProviderConnectionID); err != nil {
			router.Close()
			return nil, nil, err
		}
		return &localStatuslineRequestClient{handle: router.Handle, connectionID: interactiveStatuslineProviderConnectionID},
			func() { router.Close() }, nil
	}
	return statuslineProviderDiscovery(connect)
}

// interactiveRemoteStatuslineProviderDiscovery discovers providers against the
// remote app server session.
func interactiveRemoteStatuslineProviderDiscovery(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, root *cli.RootOptions) codextea.StatuslineProviderDiscoveryFunc {
	connect := func(context.Context) (statuslineprovider.AppServer, func(), error) {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		return &remoteStatuslineRequestClient{ctx: reqCtx, client: client}, func() {
			client.close()
			cancel()
		}, nil
	}
	return statuslineProviderDiscovery(connect)
}

// statuslineProviderDiscovery streams discovery updates over one connection so
// the picker can show rows as servers answer.
func statuslineProviderDiscovery(connect func(context.Context) (statuslineprovider.AppServer, func(), error)) codextea.StatuslineProviderDiscoveryFunc {
	return func(ctx context.Context, request codextea.StatuslineProviderDiscoveryRequest) <-chan codextea.StatuslineProviderDiscoveryUpdate {
		updates := make(chan codextea.StatuslineProviderDiscoveryUpdate)
		go func() {
			defer close(updates)
			client, closeClient, err := connect(ctx)
			if err != nil {
				select {
				case updates <- codextea.StatuslineProviderDiscoveryUpdate{Err: err}:
				case <-ctx.Done():
				}
				return
			}
			defer closeClient()
			for update := range statuslineprovider.Refresh(ctx, client, request.Known) {
				converted := codextea.StatuslineProviderDiscoveryUpdate{Server: update.Server, Err: update.Err}
				for _, provider := range update.Providers {
					converted.Providers = append(converted.Providers, codextea.StatuslineProviderCandidate{
						Server:      provider.ID.Server,
						Tool:        provider.ID.Tool,
						Title:       provider.Title,
						PluginLabel: provider.PluginLabel,
						URI:         provider.URI,
					})
				}
				select {
				case updates <- converted:
				case <-ctx.Done():
					return
				}
			}
		}()
		return updates
	}
}

// statuslineProviderContents decodes the read response's resource contents.
func statuslineProviderContents(raw map[string]any) []codextea.StatuslineProviderContent {
	encoded, err := json.Marshal(raw["contents"])
	if err != nil {
		return nil
	}
	var contents []mcp.MCPResourceContent
	if err := json.Unmarshal(encoded, &contents); err != nil {
		return nil
	}
	out := make([]codextea.StatuslineProviderContent, 0, len(contents))
	for _, content := range contents {
		out = append(out, codextea.StatuslineProviderContent{
			URI:      content.URI,
			MIMEType: content.MimeType,
			Text:     content.Text,
			IsText:   content.Blob == "",
		})
	}
	return out
}
