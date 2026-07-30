package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	maxMCPCatalogPages          = 100
	maxMCPCatalogItems          = 1024
	maxMCPPaginationCursorBytes = 64 * 1024
	defaultMCPPaginationTimeout = 30 * time.Second
)

type mcpPaginationFetch[T any] func(context.Context, *string) ([]T, *string, error)

func collectMCPPaginated[T any](ctx context.Context, method string, timeout time.Duration, fetch mcpPaginationFetch[T]) ([]T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = defaultMCPPaginationTimeout
	}
	overallCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	items := make([]T, 0)
	seenCursors := map[string]struct{}{}
	var cursor *string
	for page := 0; ; page++ {
		if page == maxMCPCatalogPages {
			return nil, fmt.Errorf("%s exceeded the pagination limit of %d pages", method, maxMCPCatalogPages)
		}
		pageItems, nextCursor, err := fetch(overallCtx, cloneMCPPaginationCursor(cursor))
		if err != nil {
			if errors.Is(overallCtx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("%s pagination timed out after %s", method, timeout)
			}
			return nil, err
		}
		if len(pageItems) > maxMCPCatalogItems-len(items) {
			return nil, fmt.Errorf("%s exceeded the catalog limit of %d items", method, maxMCPCatalogItems)
		}
		items = append(items, pageItems...)
		if nextCursor == nil {
			return items, nil
		}
		if len(*nextCursor) > maxMCPPaginationCursorBytes {
			return nil, fmt.Errorf("%s returned a pagination cursor exceeding %d bytes", method, maxMCPPaginationCursorBytes)
		}
		if _, repeated := seenCursors[*nextCursor]; repeated {
			return nil, fmt.Errorf("%s returned a repeated pagination cursor", method)
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
