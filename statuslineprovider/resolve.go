package statuslineprovider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/plugin"
)

var statuslineLookupTimeout = 5 * time.Second

const mcpResourceReadMethod = "mcpServer/resource/read"

// LayerReader returns the complete configuration layer stack for cwd, ordered
// from highest to lowest precedence, matching the Rust resolver input.
type LayerReader func(ctx context.Context, cwd string) ([]config.Layer, error)

// Resolved is a provider that has passed project-scope and live-connection
// checks for a displayed thread.
type Resolved struct {
	app      AppServer
	ThreadID string
	Provider Provider
}

// Lookup resolves a saved or discovered provider within five seconds.
func Lookup(ctx context.Context, app AppServer, layers LayerReader, threadID string, cwd string, selection Selection) (*Resolved, error) {
	if app == nil || layers == nil {
		return nil, errors.New("statusline provider lookup requires an app server and config reader")
	}
	lookupCtx, cancel := context.WithTimeout(ctx, statuslineLookupTimeout)
	defer cancel()
	result, err := resolve(lookupCtx, app, layers, threadID, cwd, selection)
	if err != nil {
		if errors.Is(lookupCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, &LookupError{Kind: LookupTimedOut}
		}
		return nil, &LookupError{Kind: LookupRequest, Err: err}
	}
	return result, nil
}

func resolve(ctx context.Context, app AppServer, layers LayerReader, threadID string, cwd string, selection Selection) (*Resolved, error) {
	if (selection.Saved == nil) == (selection.Discovered == nil) {
		return nil, errInvalidSelection
	}
	stack, err := layers(ctx, cwd)
	if err != nil {
		return nil, err
	}
	if stack == nil {
		return nil, errors.New("config/read omitted the requested layers")
	}
	id := ProviderID{}
	var advertisement *Provider
	if selection.Saved != nil {
		id = *selection.Saved
	} else {
		advertisement = selection.Discovered
		id = advertisement.ID
	}
	if projectSets(stack, "mcp_servers", id.Server) {
		return nil, nil
	}

	var owner *plugin.PluginId
	if advertisement != nil {
		owner = advertisement.PluginID.Clone()
	} else {
		global, err := inventoryServer(ctx, app, id.Server, nil)
		if err != nil {
			return nil, err
		}
		if global == nil {
			return nil, nil
		}
		provider, err := global.Provider(id.Tool)
		if err != nil {
			return nil, err
		}
		if provider == nil {
			return nil, nil
		}
		owner = global.PluginID.Clone()
	}
	if owner != nil {
		if projectSets(stack, "plugins", owner.Key()) || projectSets(stack, "marketplaces", owner.MarketplaceName) {
			return nil, nil
		}
	}

	live, err := inventoryServer(ctx, app, id.Server, &threadID)
	if err != nil {
		return nil, err
	}
	if live == nil {
		return nil, nil
	}
	if !samePluginIDs(live.PluginID, owner) {
		return nil, nil
	}
	provider, err := live.Provider(id.Tool)
	if err != nil {
		return nil, err
	}
	if live.RuntimeStatus == nil || *live.RuntimeStatus != mcp.MCPConnectionConnected {
		return nil, fmt.Errorf("MCP server %s is not connected", id.Server)
	}
	if provider == nil {
		return nil, nil
	}
	return &Resolved{app: app, ThreadID: threadID, Provider: *provider}, nil
}

func samePluginIDs(a *plugin.PluginId, b *plugin.PluginId) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Key() == b.Key()
}

// Read asks the displayed thread to render the provider's resource with the
// opaque statusline snapshot. The environment is explicitly null.
func (r *Resolved) Read(ctx context.Context, snapshot map[string]any) (map[string]any, error) {
	if r == nil {
		return nil, errors.New("statusline provider is not resolved")
	}
	meta := map[string]any{}
	if snapshot != nil {
		meta["com.openai/codex-statusline-state"] = snapshot
	}
	params := map[string]any{
		"threadId":      r.ThreadID,
		"server":        r.Provider.ID.Server,
		"environmentId": nil,
		"uri":           r.Provider.URI,
		"connectorId":   nil,
		"originCallId":  nil,
		"target":        nil,
		"_meta":         meta,
	}
	return r.app.Request(ctx, mcpResourceReadMethod, params)
}

// projectSets implements Rust has_project_field over config layers ordered
// highest first. Arrays and scalars only count when their winning definition is
// project-owned; object fields are checked recursively.
func projectSets(layers []config.Layer, section string, key string) bool {
	definitions := make([]layerDefinition, 0, len(layers))
	for _, layer := range layers {
		if layer.DisabledReason != nil {
			continue
		}
		values, ok := layer.Config.(map[string]any)
		if !ok {
			continue
		}
		table, ok := values[section].(map[string]any)
		if !ok {
			continue
		}
		value, present := table[key]
		if !present {
			continue
		}
		definitions = append(definitions, layerDefinition{
			Project: layer.Name.Type == config.LayerSourceProject,
			Value:   value,
		})
	}
	return hasProjectField(definitions)
}

type layerDefinition struct {
	Project bool
	Value   any
}

func hasProjectField(definitions []layerDefinition) bool {
	if len(definitions) == 0 {
		return false
	}
	first := definitions[0]
	_, object := first.Value.(map[string]any)
	allSameOrigin := true
	for _, definition := range definitions[1:] {
		if definition.Project != first.Project {
			allSameOrigin = false
			break
		}
	}
	if !object || allSameOrigin {
		return first.Project
	}
	fields := map[string][]layerDefinition{}
	for _, definition := range definitions {
		values, ok := definition.Value.(map[string]any)
		if !ok {
			break
		}
		for name, value := range values {
			fields[name] = append(fields[name], layerDefinition{Project: definition.Project, Value: value})
		}
	}
	if len(fields) == 0 {
		return first.Project
	}
	for _, values := range fields {
		if hasProjectField(values) {
			return true
		}
	}
	return false
}
