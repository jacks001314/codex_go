package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectMCPPaginatedPreservesValidPages(t *testing.T) {
	var cursors []string
	items, err := collectMCPPaginated(context.Background(), "tools/list", time.Second, func(_ context.Context, cursor *string) ([]string, *string, error) {
		if cursor == nil {
			cursors = append(cursors, "<nil>")
			next := "second"
			return []string{"first"}, &next, nil
		}
		cursors = append(cursors, *cursor)
		return []string{"second"}, nil, nil
	})
	if err != nil || strings.Join(items, ",") != "first,second" || strings.Join(cursors, ",") != "<nil>,second" {
		t.Fatalf("items=%#v cursors=%#v error=%v", items, cursors, err)
	}
}

func TestCollectMCPPaginatedRejectsCatalogBounds(t *testing.T) {
	t.Run("pages", func(t *testing.T) {
		var requests atomic.Int32
		_, err := collectMCPPaginated(context.Background(), "tools/list", time.Second, func(_ context.Context, _ *string) ([]struct{}, *string, error) {
			page := requests.Add(1)
			next := fmt.Sprintf("page-%d", page)
			return nil, &next, nil
		})
		if err == nil || err.Error() != "tools/list exceeded the pagination limit of 100 pages" || requests.Load() != maxMCPCatalogPages {
			t.Fatalf("error=%v requests=%d", err, requests.Load())
		}
	})

	t.Run("items single page", func(t *testing.T) {
		_, err := collectMCPPaginated(context.Background(), "tools/list", time.Second, func(_ context.Context, _ *string) ([]struct{}, *string, error) {
			return make([]struct{}, maxMCPCatalogItems+1), nil, nil
		})
		if err == nil || err.Error() != "tools/list exceeded the catalog limit of 1024 items" {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("items across pages", func(t *testing.T) {
		_, err := collectMCPPaginated(context.Background(), "resources/list", time.Second, func(_ context.Context, cursor *string) ([]struct{}, *string, error) {
			if cursor == nil {
				next := "last"
				return make([]struct{}, maxMCPCatalogItems), &next, nil
			}
			return []struct{}{{}}, nil, nil
		})
		if err == nil || err.Error() != "resources/list exceeded the catalog limit of 1024 items" {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("cursor bytes", func(t *testing.T) {
		var requests atomic.Int32
		_, err := collectMCPPaginated(context.Background(), "resources/list", time.Second, func(_ context.Context, _ *string) ([]struct{}, *string, error) {
			requests.Add(1)
			next := strings.Repeat("x", maxMCPPaginationCursorBytes+1)
			return nil, &next, nil
		})
		if err == nil || err.Error() != "resources/list returned a pagination cursor exceeding 65536 bytes" || requests.Load() != 1 {
			t.Fatalf("error=%v requests=%d", err, requests.Load())
		}
	})

	t.Run("repeated cursor", func(t *testing.T) {
		var page int
		_, err := collectMCPPaginated(context.Background(), "resources/templates/list", time.Second, func(_ context.Context, _ *string) ([]struct{}, *string, error) {
			cursors := []string{"first", "second", "first"}
			next := cursors[page]
			page++
			return nil, &next, nil
		})
		if err == nil || err.Error() != "resources/templates/list returned a repeated pagination cursor" || page != 3 {
			t.Fatalf("error=%v pages=%d", err, page)
		}
	})
}

func TestCollectMCPPaginatedAppliesOneOverallTimeout(t *testing.T) {
	timeout := 20 * time.Millisecond
	_, err := collectMCPPaginated(context.Background(), "resources/list", timeout, func(ctx context.Context, _ *string) ([]struct{}, *string, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})
	if err == nil || err.Error() != "resources/list pagination timed out after 20ms" {
		t.Fatalf("error=%v", err)
	}
}
