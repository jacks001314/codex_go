package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	maxMCPCatalogPages          = 100
	maxMCPCatalogItems          = 2048
	maxCodexAppsCatalogItems    = 8192
	maxMCPPaginationCursorBytes = 64 * 1024
	defaultMCPPaginationTimeout = 30 * time.Second
	defaultMCPCatalogItemLimit  = maxMCPCatalogItems
)

type mcpPaginationFetch[T any] func(context.Context, *string) ([]T, *string, error)

// mcpPaginationMetaFetch is the tools/list shape of mcpPaginationFetch: the
// response-level `_meta` of a page is reported next to that page's items so the
// caller can retain the catalog metadata the server attached to the listing
// (Rust #54686).
type mcpPaginationMetaFetch[T any] func(context.Context, *string) ([]T, json.RawMessage, *string, error)

func collectMCPPaginated[T any](ctx context.Context, method string, timeout time.Duration, fetch mcpPaginationFetch[T]) ([]T, error) {
	return collectMCPPaginatedWithLimit(ctx, method, timeout, defaultMCPCatalogItemLimit, fetch)
}

// collectMCPPaginatedWithLimit mirrors Rust's collect_paginated_with_limit:
// host-owned codex_apps registrations may expose larger catalogs
// (maxCodexAppsCatalogItems) while every other MCP server keeps the standard
// maxMCPCatalogItems limit.
func collectMCPPaginatedWithLimit[T any](ctx context.Context, method string, timeout time.Duration, itemLimit int, fetch mcpPaginationFetch[T]) ([]T, error) {
	items, _, err := collectMCPPaginatedWithLimitAndMeta(ctx, method, timeout, itemLimit, func(ctx context.Context, cursor *string) ([]T, json.RawMessage, *string, error) {
		pageItems, nextCursor, err := fetch(ctx, cursor)
		return pageItems, nil, nextCursor, err
	})
	return items, err
}

// collectMCPPaginatedWithLimitAndMeta mirrors Rust's collect_paginated: the
// response-level `_meta` of the first page rides along with the collected items
// so a caller can preserve the metadata the server attached to the start of the
// catalog. Later pages never replace it, and a failed listing reports no
// metadata at all (Rust #54686).
func collectMCPPaginatedWithLimitAndMeta[T any](ctx context.Context, method string, timeout time.Duration, itemLimit int, fetch mcpPaginationMetaFetch[T]) ([]T, json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = defaultMCPPaginationTimeout
	}
	if itemLimit <= 0 {
		itemLimit = defaultMCPCatalogItemLimit
	}
	overallCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	items := make([]T, 0)
	var meta json.RawMessage
	seenCursors := map[string]struct{}{}
	var cursor *string
	for page := 0; ; page++ {
		if page == maxMCPCatalogPages {
			return nil, nil, fmt.Errorf("%s exceeded the pagination limit of %d pages", method, maxMCPCatalogPages)
		}
		pageItems, pageMeta, nextCursor, err := fetch(overallCtx, cloneMCPPaginationCursor(cursor))
		if err != nil {
			if errors.Is(overallCtx.Err(), context.DeadlineExceeded) {
				return nil, nil, fmt.Errorf("%s pagination timed out after %s", method, timeout)
			}
			return nil, nil, err
		}
		if page == 0 {
			meta = cloneMCPRawMessage(pageMeta)
		}
		if len(pageItems) > itemLimit-len(items) {
			return nil, nil, fmt.Errorf("%s exceeded the catalog limit of %d items", method, itemLimit)
		}
		items = append(items, pageItems...)
		if nextCursor == nil {
			return items, meta, nil
		}
		if len(*nextCursor) > maxMCPPaginationCursorBytes {
			return nil, nil, fmt.Errorf("%s returned a pagination cursor exceeding %d bytes", method, maxMCPPaginationCursorBytes)
		}
		if _, repeated := seenCursors[*nextCursor]; repeated {
			return nil, nil, fmt.Errorf("%s returned a repeated pagination cursor", method)
		}
		seenCursors[*nextCursor] = struct{}{}
		cursor = cloneMCPPaginationCursor(nextCursor)
	}
}

func mcpListParamsForCursor(cursor *string) map[string]any {
	if cursor == nil {
		return map[string]any{}
	}
	return map[string]any{"cursor": *cursor}
}

func cloneMCPPaginationCursor(cursor *string) *string {
	if cursor == nil {
		return nil
	}
	cloned := *cursor
	return &cloned
}

func mcpPaginationTimeout(config *ServerConfig) time.Duration {
	if config != nil && config.ToolTimeout > 0 {
		return config.ToolTimeout
	}
	return defaultMCPPaginationTimeout
}

// mcpCatalogItemLimit returns the per-server catalog item limit. Host-owned
// codex_apps servers opt into the larger limit when their config is built by
// CodexAppsServerConfig; all other registrations use the standard limit.
func mcpCatalogItemLimit(config *ServerConfig) int {
	if config != nil && config.CatalogItemLimit > 0 {
		return config.CatalogItemLimit
	}
	return defaultMCPCatalogItemLimit
}
