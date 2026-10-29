package analytics

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"codex_go/chatgptapi"
)

// Consumer task accounting keeps missing amounts distinct from zero and
// requires complete descendant groups. This is deliberately separate from the
// business lifetime-credit estimates in chats.go (Rust analytics/tasks.rs,
// #45768).

const (
	analyticsTaskChatLimit       = 100
	analyticsTaskThreadLimit     = 1000
	analyticsTaskPageSize        = 100
	analyticsTaskBatchLimit      = 100
	analyticsTaskConcurrency     = 2
	analyticsTaskDiscoveryBudget = 25 * time.Second
	analyticsTaskUsageBudget     = 25 * time.Second
)

const (
	analyticsTaskOverlapMessage     = "Task descendant groups overlap."
	analyticsTaskLimitMessage       = "Task exceeds the reporting limit."
	analyticsTaskCursorMessage      = "Task listing repeated a cursor."
	analyticsTaskCycleMessage       = "Task descendant cycle."
	analyticsTaskIncompleteMessage  = "Task descendant discovery is incomplete."
	analyticsTaskUnsupportedMessage = "Task descendant discovery is unsupported."
	analyticsTaskTimestampMessage   = "Invalid task usage timestamp."
)

// AnalyticsTaskChat is one recent consumer root with its optional accounting.
type AnalyticsTaskChat struct {
	Title string
	Task  *chatgptapi.TaskUsage
}

// DisplayTitle hides titles whose accounting is unavailable, because local
// history can span accounts (Rust tasks::Chat::display_title).
func (c AnalyticsTaskChat) DisplayTitle() string {
	if c.Task != nil && c.Task.DataStatus != chatgptapi.TaskUsageStatusUnavailable {
		return c.Title
	}
	return analyticsChatUnavailableTitle
}

// AnalyticsTaskChats is the loaded consumer ranking plus its coverage.
type AnalyticsTaskChats struct {
	Rows      []AnalyticsTaskChat
	Truncated bool
	// UpdatedAt is the oldest freshness timestamp of the batches that carried
	// one; nil when any batch was missing, failed, or ran out of time.
	UpdatedAt *time.Time
}

// AnalyticsTaskThreadPage is one descendant page for an ancestor root.
type AnalyticsTaskThreadPage struct {
	Data       []AnalyticsChatThread
	NextCursor *string
}

// AnalyticsTaskDescendantLister lists threads under one ancestor, including
// archived pages, which the runtime's thread source provides.
type AnalyticsTaskDescendantLister func(ctx context.Context, ancestorThreadID string, archived bool, cursor *string, limit int) (AnalyticsTaskThreadPage, error)

// LoadAnalyticsTasks loads consumer task accounting for up to 100 recent local
// roots and their descendants. It returns (nil, nil) when the account is not a
// consumer account or a listing source is missing (Rust tasks::read).
func LoadAnalyticsTasks(
	ctx context.Context,
	live *Live,
	rootsLister AnalyticsChatLister,
	descendantLister AnalyticsTaskDescendantLister,
) (*AnalyticsTaskChats, error) {
	if live == nil || rootsLister == nil || descendantLister == nil {
		return nil, nil
	}
	session, err := live.Session(ctx)
	if err != nil {
		return nil, err
	}
	if AccountKindFor(session.Backend.Account().PlanType) != AccountKindConsumer {
		return nil, nil
	}
	if err := session.Backend.EnsureIdentity(); err != nil {
		return nil, err
	}
	rootsCtx, cancelRoots := context.WithTimeout(ctx, analyticsChatRootsTimeout)
	defer cancelRoots()
	roots, err := analyticsChatRoots(rootsCtx, rootsLister)
	if err != nil {
		if ctx.Err() == nil && errors.Is(rootsCtx.Err(), context.DeadlineExceeded) {
			return nil, errors.New(analyticsChatListingTimedOutMessage)
		}
		return nil, err
	}
	if err := session.Backend.EnsureIdentity(); err != nil {
		return nil, err
	}
	sort.SliceStable(roots, func(left, right int) bool {
		if roots[left].UpdatedAt != roots[right].UpdatedAt {
			return roots[left].UpdatedAt > roots[right].UpdatedAt
		}
		return roots[left].ID < roots[right].ID
	})
	truncated := len(roots) > analyticsTaskChatLimit
	if len(roots) > analyticsTaskChatLimit {
		roots = roots[:analyticsTaskChatLimit]
	}
	queries, err := analyticsTaskDiscoveries(ctx, roots, descendantLister)
	if err != nil {
		return nil, err
	}
	batches, err := analyticsTaskBatches(queries)
	if err != nil {
		return nil, err
	}
	usage, freshness, err := analyticsTaskUsage(ctx, session, batches)
	if err != nil {
		return nil, err
	}
	if err := session.Backend.EnsureIdentity(); err != nil {
		return nil, err
	}
	rows := make([]AnalyticsTaskChat, 0, len(roots))
	for _, thread := range roots {
		chat := AnalyticsTaskChat{Title: analyticsChatTitle(thread)}
		if task, ok := usage[thread.ID]; ok {
			loaded := task
			chat.Task = &loaded
		}
		rows = append(rows, chat)
	}
	return &AnalyticsTaskChats{
		Rows:      rows,
		Truncated: truncated,
		UpdatedAt: analyticsTaskFreshness(freshness),
	}, nil
}

// analyticsTaskDiscoveries collects complete descendant groups before the
// discovery deadline and never queries a partial group (Rust tasks::read).
func analyticsTaskDiscoveries(
	ctx context.Context,
	roots []AnalyticsChatThread,
	lister AnalyticsTaskDescendantLister,
) ([]chatgptapi.TaskUsageThread, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, analyticsTaskDiscoveryBudget)
	defer cancel()
	results := make(chan chatgptapi.TaskUsageThread, len(roots))
	work := make(chan AnalyticsChatThread)
	var workers sync.WaitGroup
	for worker := 0; worker < analyticsTaskConcurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for root := range work {
				query, err := analyticsTaskDescendants(deadlineCtx, lister, root)
				if err != nil {
					continue
				}
				results <- query
			}
		}()
	}
	go func() {
		defer close(work)
		for _, root := range roots {
			select {
			case work <- root:
			case <-deadlineCtx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	queries := make([]chatgptapi.TaskUsageThread, 0, len(roots))
	for query := range results {
		queries = append(queries, query)
	}
	return queries, nil
}

// analyticsTaskBatches keeps every batch disjoint and within the backend's
// task and thread limits.
func analyticsTaskBatches(queries []chatgptapi.TaskUsageThread) ([][]chatgptapi.TaskUsageThread, error) {
	seen := make(map[string]struct{})
	var batches [][]chatgptapi.TaskUsageThread
	var batch []chatgptapi.TaskUsageThread
	count := 0
	for _, query := range queries {
		ids := make([]string, 0, 1+len(query.DescendantThreadIDs))
		ids = append(ids, query.ThreadID)
		ids = append(ids, query.DescendantThreadIDs...)
		overlap := false
		for _, id := range ids {
			if _, duplicate := seen[id]; duplicate {
				overlap = true
				break
			}
		}
		if overlap {
			return nil, errors.New(analyticsTaskOverlapMessage)
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
		size := len(ids)
		if count+size > analyticsTaskThreadLimit || len(batch) == analyticsTaskBatchLimit {
			batches = append(batches, batch)
			batch = nil
			count = 0
		}
		count += size
		batch = append(batch, query)
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches, nil
}

// analyticsTaskUsage queries each batch concurrently. A batch that fails with
// 404 or 503, or that misses the deadline, only clears the freshness
// timestamp; its rows stay unavailable instead of dropping the ranking
// (Rust tasks::read).
func analyticsTaskUsage(
	ctx context.Context,
	session *AnalyticsLiveSession,
	batches [][]chatgptapi.TaskUsageThread,
) (map[string]chatgptapi.TaskUsage, []*time.Time, error) {
	usage := make(map[string]chatgptapi.TaskUsage)
	if len(batches) == 0 {
		return usage, nil, nil
	}
	type batchResult struct {
		response *chatgptapi.TaskUsageResponse
		err      error
	}
	results := make(chan batchResult, len(batches))
	work := make(chan []chatgptapi.TaskUsageThread)
	usageCtx, cancelUsage := context.WithCancel(ctx)
	defer cancelUsage()
	var workers sync.WaitGroup
	for worker := 0; worker < analyticsTaskConcurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for batch := range work {
				response, err := RequestAnalytics(usageCtx, session.Backend, func(ctx context.Context, client *chatgptapi.CloudClient) (*chatgptapi.TaskUsageResponse, error) {
					return client.GetTaskUsage(ctx, batch)
				})
				results <- batchResult{response: response, err: err}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, batch := range batches {
			work <- batch
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	deadline := time.Now().Add(analyticsTaskUsageBudget)
	freshness := make([]*time.Time, 0, len(batches))
	completed := 0
	timedOut := false
collect:
	for completed < len(batches) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			timedOut = true
			break
		}
		select {
		case <-usageCtx.Done():
			return nil, nil, usageCtx.Err()
		case <-time.After(remaining):
			timedOut = true
			break collect
		case result := <-results:
			completed++
			if result.err != nil {
				var statusErr *chatgptapi.HTTPStatusError
				if errors.As(result.err, &statusErr) && (statusErr.IsStatus(404) || statusErr.IsStatus(503)) {
					freshness = append(freshness, nil)
					continue
				}
				return nil, nil, analyticsChatRequestError(result.err)
			}
			parsed, err := analyticsTaskDataAsOf(result.response.DataAsOf)
			if err != nil {
				return nil, nil, err
			}
			freshness = append(freshness, parsed)
			for _, task := range result.response.Threads {
				usage[task.ThreadID] = task
			}
		}
	}
	if timedOut || completed < len(batches) {
		freshness = append(freshness, nil)
	}
	return usage, freshness, nil
}

// analyticsTaskDataAsOf parses one freshness timestamp.
func analyticsTaskDataAsOf(value *string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return nil, errors.New(analyticsTaskTimestampMessage)
	}
	utc := parsed.UTC()
	return &utc, nil
}

// analyticsTaskFreshness reports the oldest known timestamp, or nil when any
// batch was missing one.
func analyticsTaskFreshness(freshness []*time.Time) *time.Time {
	if len(freshness) == 0 {
		return nil
	}
	var oldest *time.Time
	for _, entry := range freshness {
		if entry == nil {
			return nil
		}
		if oldest == nil || entry.Before(*oldest) {
			oldest = entry
		}
	}
	return oldest
}

// analyticsTaskDescendants discovers one root's complete descendant group,
// proving that every returned chain reaches the root (Rust tasks::descendants).
func analyticsTaskDescendants(
	ctx context.Context,
	lister AnalyticsTaskDescendantLister,
	root AnalyticsChatThread,
) (chatgptapi.TaskUsageThread, error) {
	ids := make(map[string]struct{})
	parents := make(map[string]string)
	for _, archived := range []bool{false, true} {
		var cursor *string
		seen := make(map[string]struct{})
		for {
			page, err := lister(ctx, root.ID, archived, cursor, analyticsTaskPageSize)
			if err != nil {
				return chatgptapi.TaskUsageThread{}, err
			}
			for _, thread := range page.Data {
				if thread.ID == root.ID || thread.ParentThreadID == nil {
					return chatgptapi.TaskUsageThread{}, errors.New(analyticsTaskUnsupportedMessage)
				}
				parents[thread.ID] = *thread.ParentThreadID
				ids[thread.ID] = struct{}{}
			}
			if len(ids) >= analyticsTaskThreadLimit {
				return chatgptapi.TaskUsageThread{}, errors.New(analyticsTaskLimitMessage)
			}
			if page.NextCursor == nil {
				break
			}
			if _, repeated := seen[*page.NextCursor]; repeated {
				return chatgptapi.TaskUsageThread{}, errors.New(analyticsTaskCursorMessage)
			}
			seen[*page.NextCursor] = struct{}{}
			next := *page.NextCursor
			cursor = &next
		}
	}
	// Older servers can ignore the ancestor filter. Prove every returned chain
	// reaches this root.
	for id := range ids {
		current := id
		chain := make(map[string]struct{})
		for current != root.ID {
			if _, cycle := chain[current]; cycle {
				return chatgptapi.TaskUsageThread{}, errors.New(analyticsTaskCycleMessage)
			}
			chain[current] = struct{}{}
			parent, ok := parents[current]
			if !ok {
				return chatgptapi.TaskUsageThread{}, errors.New(analyticsTaskIncompleteMessage)
			}
			current = parent
		}
	}
	descendants := make([]string, 0, len(ids))
	for id := range ids {
		descendants = append(descendants, id)
	}
	sort.Strings(descendants)
	query := chatgptapi.TaskUsageThread{
		ThreadID:            root.ID,
		DescendantThreadIDs: descendants,
	}
	if root.CreatedAt > 0 {
		created := time.Unix(root.CreatedAt, 0).UTC().Format(time.RFC3339)
		query.CreatedAt = &created
	}
	return query, nil
}
