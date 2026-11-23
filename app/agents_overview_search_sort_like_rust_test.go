package app

import (
	"testing"
	"time"

	"codex_go/session"
	agentsoverview "codex_go/tui/agents_overview"
)

// Mirrors Rust #63208: the embedded history search returns matches in the
// command center's selected ordering so creation-time sorting finds recently
// created tasks that the last-update order pushed down.
func TestEmbeddedAgentsOverviewSearchSortsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	base := time.Now().UTC().Add(-time.Hour)
	records := []*session.Record{
		{
			ID: "task-newest-created", Title: "needle newest created", Preview: "needle",
			CreatedAt: base.Add(50 * time.Minute), UpdatedAt: base.Add(51 * time.Minute), RecencyAt: base.Add(51 * time.Minute),
			Metadata: session.Metadata{CWD: "/work/a", Source: "cli"},
		},
		{
			ID: "task-newest-updated", Title: "needle newest updated", Preview: "needle",
			CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour), RecencyAt: base.Add(2 * time.Hour),
			Metadata: session.Metadata{CWD: "/work/a", Source: "cli"},
		},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	search := embeddedAgentsOverviewSearch(store)
	byCreated, err := search("needle", agentsoverview.SearchSortCreated)
	if err != nil {
		t.Fatalf("created search error = %v", err)
	}
	if len(byCreated) != 2 || byCreated[0].ThreadID != "task-newest-created" {
		t.Fatalf("created search rows = %#v", byCreated)
	}
	if byCreated[0].CreatedAt == 0 {
		t.Fatal("search rows must carry the creation timestamp")
	}

	byUpdated, err := search("needle", agentsoverview.SearchSortUpdated)
	if err != nil {
		t.Fatalf("updated search error = %v", err)
	}
	if len(byUpdated) != 2 || byUpdated[0].ThreadID != "task-newest-updated" {
		t.Fatalf("updated search rows = %#v", byUpdated)
	}
}
