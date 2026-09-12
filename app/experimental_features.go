package app

// Rust parity: codex-rs/tui/src/experimental_features.rs. The TUI's
// /experimental popup is populated from the app server's experimentalFeature/list
// catalog; this is the client half (bounded pagination) plus the embedded
// equivalent resolved through the local config service.

import (
	"context"
	"errors"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/features"
	"codex_go/session"
	codextea "codex_go/tui/tea"
)

const (
	// Rust experimental_features::fetch bounds.
	experimentalFeaturesPageLimit    = 100
	experimentalFeaturesMaxPages     = 10
	experimentalFeaturesFetchTimeout = 5 * time.Second
)

// experimentalFeaturesRequestFunc is the typed app-server request Rust performs
// through AppServerRequestHandle::request_typed.
type experimentalFeaturesRequestFunc func(ctx context.Context, method appserver.Method, params any, target any) error

// fetchExperimentalFeatures ports Rust experimental_features::fetch: paginated
// experimentalFeature/list (<=100 per page, <=10 pages) with duplicate-name and
// repeated-cursor guards. The error strings match Rust's because the TUI shows
// them as the popup's discovery status.
func fetchExperimentalFeatures(ctx context.Context, request experimentalFeaturesRequestFunc, threadID string) ([]codextea.ExperimentalFeatureEntry, error) {
	if request == nil {
		return nil, errors.New("Experimental feature request failed")
	}
	threadID = strings.TrimSpace(threadID)
	entries := []codextea.ExperimentalFeatureEntry{}
	names := map[string]bool{}
	cursors := map[string]bool{}
	var cursor *string
	for page := 0; page < experimentalFeaturesMaxPages; page++ {
		limit := experimentalFeaturesPageLimit
		params := features.FeatureListParams{Cursor: cursor, Limit: &limit}
		if threadID != "" {
			value := threadID
			params.ThreadID = &value
		}
		var response features.FeatureListResponse
		if err := request(ctx, appserver.MethodExperimentalFeatureList, params, &response); err != nil {
			return nil, errors.New("Experimental feature request failed")
		}
		if len(response.Data) > experimentalFeaturesPageLimit {
			return nil, errors.New("Experimental feature page exceeds requested limit")
		}
		for _, entry := range response.Data {
			name := strings.TrimSpace(entry.Key)
			if name == "" || names[name] {
				continue
			}
			names[name] = true
			entries = append(entries, experimentalFeatureEntryFromCatalog(entry))
		}
		if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
			return entries, nil
		}
		next := strings.TrimSpace(*response.NextCursor)
		if cursors[next] {
			return nil, errors.New("Experimental feature pagination repeated a cursor")
		}
		cursors[next] = true
		cursor = &next
	}
	return nil, errors.New("Experimental feature discovery exceeded 10 pages")
}

// experimentalFeatureEntryFromCatalog maps one features.FeatureEntry (the
// app-server wire shape, Rust ExperimentalFeature) onto the TUI type.
func experimentalFeatureEntryFromCatalog(entry features.FeatureEntry) codextea.ExperimentalFeatureEntry {
	result := codextea.ExperimentalFeatureEntry{
		Name:           strings.TrimSpace(entry.Key),
		Enabled:        entry.Enabled,
		DefaultEnabled: entry.DefaultEnabled,
		Stage:          string(entry.Stage),
	}
	if entry.DisplayName != nil {
		result.DisplayName = strings.TrimSpace(*entry.DisplayName)
	}
	if entry.Description != nil {
		result.Description = strings.TrimSpace(*entry.Description)
	}
	return result
}

// interactiveRemoteExperimentalFeatures reads the remote app server's catalog.
func interactiveRemoteExperimentalFeatures(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.ExperimentalFeaturesReaderFunc {
	return func(threadID string) ([]codextea.ExperimentalFeatureEntry, error) {
		connectCtx, cancelConnect := remoteTUIAccountRequestContext(ctx)
		defer cancelConnect()
		client, err := openRemoteSessionClient(connectCtx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		fetchCtx, cancelFetch := context.WithTimeout(backgroundContextIfNil(ctx), experimentalFeaturesFetchTimeout)
		defer cancelFetch()
		return fetchExperimentalFeatures(fetchCtx, func(requestCtx context.Context, method appserver.Method, params any, target any) error {
			return remoteSessionRequest(requestCtx, client, method, params, target)
		}, threadID)
	}
}

// interactiveLocalExperimentalFeatures resolves the embedded app server's
// catalog from the same config service its experimentalFeature/list handler uses:
// the thread's refreshed config (including project-local config for its cwd)
// over the default feature catalog.
func interactiveLocalExperimentalFeatures(root *cli.RootOptions) codextea.ExperimentalFeaturesReaderFunc {
	return func(threadID string) ([]codextea.ExperimentalFeatureEntry, error) {
		service := interactiveConfigService(root)
		if service == nil {
			return nil, errors.New("Experimental feature request failed")
		}
		readParams := &config.ConfigReadParams{}
		if threadID = strings.TrimSpace(threadID); threadID != "" {
			if record, err := newSessionStore().Read(session.ThreadID(threadID), true, false); err == nil && record != nil {
				if cwd := strings.TrimSpace(record.Metadata.CWD); cwd != "" {
					readParams.CWD = &cwd
				}
			}
		}
		read, err := service.Read(readParams)
		if err != nil {
			return nil, err
		}
		settings := (&config.Config{Values: read.Config}).FeatureSettings()
		limit := experimentalFeaturesPageLimit
		response, err := features.NewFeatureService(nil).ListWithSettings(&features.FeatureListParams{Limit: &limit}, settings)
		if err != nil {
			return nil, err
		}
		entries := make([]codextea.ExperimentalFeatureEntry, 0, len(response.Data))
		for _, entry := range response.Data {
			entries = append(entries, experimentalFeatureEntryFromCatalog(entry))
		}
		return entries, nil
	}
}

func backgroundContextIfNil(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
