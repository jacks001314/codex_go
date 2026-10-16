package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Rust #54351 ("Use the turn index for turn-scoped item pagination") forces
// `idx_thread_items_by_turn_page` in thread-store's `page_item_rows` whenever a
// turn-scoped item page carries neither an item-id nor an updated-at anchor:
// the thread-wide `idx_thread_items_page` files a thread's rows by
// (thread_id, rollout_ordinal) only, so its turn filter is a post-filter and a
// turn whose final page is underfilled makes the scan walk earlier turns.
//
// Go's `ListThreadHistoryItems` builds exactly that statement, so the same hint
// belongs on it. The test pins the statement the runtime builds (the index hint
// is the whole upstream change, and upstream added no test for it), and then
// proves through SQLite that the hinted statement is valid, resolves onto the
// turn index, and still pages one turn at a time - including with ANALYZE
// statistics, which is where a planner would otherwise become free to prefer the
// thread-wide index.
func TestThreadHistoryTurnScopedItemPageUsesTurnIndexLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	path := writePaginatedHistoryFixture(t, home, "thread-index", nil, []historyFixtureTurn{
		{ID: "turn-1", Items: []historyFixtureItem{
			{ID: "user-1", Type: "userMessage", Text: "question"},
			{ID: "agent-1", Type: "agentMessage", Text: "answer", Phase: "final_answer"},
		}},
		{ID: "turn-2", Items: []historyFixtureItem{
			{ID: "user-2", Type: "userMessage", Text: "next"},
			{ID: "agent-2", Type: "agentMessage", Text: "second", Phase: "final_answer"},
		}},
	})
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	// The statement the runtime executes: only a turn-scoped page carries the hint.
	turnScopedStatement := threadHistoryItemsPageQuery(true)
	if !strings.Contains(turnScopedStatement, " INDEXED BY idx_thread_items_by_turn_page") {
		t.Fatalf("turn-scoped item page statement carries no turn index hint:\n%s", turnScopedStatement)
	}
	if wideStatement := threadHistoryItemsPageQuery(false); strings.Contains(wideStatement, "INDEXED BY") {
		t.Fatalf("thread-wide item page statement must keep the planner free:\n%s", wideStatement)
	}

	// Behavioural half: the hinted statement runs through the runtime and still
	// pages one turn at a time.
	turnID := "turn-1"
	page, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-index", TurnID: &turnID, PageSize: 10, SortDirection: ThreadHistorySortAsc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := historyItemIDs(page.Items); !equalStrings(got, []string{"user-1", "agent-1"}) {
		t.Fatalf("turn-scoped items = %v, want [user-1 agent-1]", got)
	}
	if page.NextCursor != nil {
		t.Fatalf("turn-scoped page cursor = %v, want none on a complete page", *page.NextCursor)
	}

	// Mechanism half: SQLite resolves both forms onto the turn index, so the guard
	// cannot change which rows a page returns.
	db, err := OpenThreadHistoryDB(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := seedThreadHistoryIndexStatistics(ctx, db); err != nil {
		t.Fatal(err)
	}

	type planKey struct {
		turnScoped bool
		direction  ThreadHistorySortDirection
	}
	plans := map[planKey]string{}
	for _, turnScoped := range []bool{false, true} {
		for _, direction := range []ThreadHistorySortDirection{ThreadHistorySortAsc, ThreadHistorySortDesc} {
			statement := threadHistoryItemsPageQuery(turnScoped)
			args := []any{"thread-index", 0}
			if turnScoped {
				statement += " AND turn_id = ?"
				args = append(args, turnID)
			}
			statement += " ORDER BY rollout_ordinal " + historySQLDirection(direction) + " LIMIT ?"
			args = append(args, 10)
			plan, planErr := explainThreadHistoryQueryPlan(ctx, db, statement, args...)
			if planErr != nil {
				t.Fatalf("explain turnScoped=%v %s error = %v", turnScoped, direction, planErr)
			}
			t.Logf("turnScoped=%v %s plan:\n%s", turnScoped, direction, plan)
			plans[planKey{turnScoped: turnScoped, direction: direction}] = plan
		}
	}
	for _, direction := range []ThreadHistorySortDirection{ThreadHistorySortAsc, ThreadHistorySortDesc} {
		turnScoped := plans[planKey{turnScoped: true, direction: direction}]
		if !strings.Contains(turnScoped, "idx_thread_items_by_turn_page") {
			t.Fatalf("turn-scoped %s item page plan does not use the turn index:\n%s", direction, turnScoped)
		}
		if wide := plans[planKey{turnScoped: false, direction: direction}]; !strings.Contains(wide, "idx_thread_items_page") {
			t.Fatalf("thread-wide %s item page plan does not use the thread page index:\n%s", direction, wide)
		}
	}
}

// seedThreadHistoryIndexStatistics gives SQLite the ANALYZE statistics a real
// thread accumulates, so the plan assertions below are made with the planner
// fully informed rather than on an empty database.
func seedThreadHistoryIndexStatistics(ctx context.Context, db *sql.DB) error {
	for turn := 0; turn < 400; turn++ {
		turnID := fmt.Sprintf("turn-%03d", turn)
		for item := 0; item < 4; item++ {
			if _, err := db.ExecContext(ctx, `INSERT INTO thread_items (thread_id, turn_id, item_id, rollout_ordinal, updated_at_ordinal, created_at_ms, item_type, item_json) VALUES (?,?,?,?,?,?,?,?)`,
				"thread-index", turnID, fmt.Sprintf("%s-item-%d", turnID, item), turn*4+item+1000, turn*4+item+1000, turn*4+item+1000, "message", "{}"); err != nil {
				return err
			}
		}
	}
	_, err := db.ExecContext(ctx, "ANALYZE")
	return err
}

// explainThreadHistoryQueryPlan renders `EXPLAIN QUERY PLAN` rows for one
// statement, so a test can assert which index SQLite resolves a query onto.
func explainThreadHistoryQueryPlan(ctx context.Context, db *sql.DB, statement string, args ...any) (string, error) {
	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+statement, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			return "", err
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return strings.Join(details, "\n"), nil
}
