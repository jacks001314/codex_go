package analytics

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
)

// Top chats ranks local roots active in the past 30 days by their estimated
// lifetime credits (Rust analytics/chats.rs, #45765). Titles stay hidden until
// the account's backend returns an estimate for that thread, so a title can
// never leak history the selected account cannot see.

const (
	analyticsChatWindow           = 30 * 24 * time.Hour
	analyticsChatListPageSize     = 100
	analyticsChatRootsTimeout     = 60 * time.Second
	analyticsChatEstimateBudget   = 25 * time.Second
	analyticsChatEstimateChunk    = 200
	analyticsChatEstimateBatch    = 100
	analyticsChatEstimateRequests = 15
)

const (
	analyticsChatListingTimedOutMessage = "Chat listing timed out. Press R to retry."
	analyticsChatRepeatedCursorMessage  = "Chat listing repeated a cursor. Press R to retry."
	analyticsChatListingFailedMessage   = "Couldn't load analytics. Press R to retry."
	analyticsChatUnavailableTitle       = "Chat usage unavailable"
	analyticsChatUntitledTitle          = "Untitled chat"
)

// AnalyticsThreadUsageSupported reports the plans whose backend accepts
// per-thread credit estimates. It deliberately matches the existing CLI
// thread-usage capability without widening other workspace reports
// (Rust models.rs thread_usage_supported).
func AnalyticsThreadUsageSupported(plan auth.PlanType) bool {
	switch plan {
	case auth.PlanBusiness, auth.PlanEnterpriseCBPUsageBased, auth.PlanEnterpriseCBPAutomation:
		return true
	default:
		return false
	}
}

// AnalyticsChat is one recent local root with its optional account estimate.
type AnalyticsChat struct {
	Title string
	Usage *chatgptapi.ThreadUsage
}

// DisplayTitle hides titles without a returned account estimate.
func (c AnalyticsChat) DisplayTitle() string {
	if c.Usage != nil {
		return c.Title
	}
	return analyticsChatUnavailableTitle
}

// AnalyticsChats is the loaded chat ranking.
type AnalyticsChats struct {
	Rows []AnalyticsChat
}

// AnalyticsChatThread is one local root discovered by thread listing.
type AnalyticsChatThread struct {
	ID string
	// Name is the user-visible title when one was set.
	Name *string
	// Preview is the fallback title captured from the thread's first message.
	Preview string
	// UpdatedAt is the thread's last activity in Unix seconds.
	UpdatedAt int64
	Ephemeral bool
	// ParentThreadID marks subagent threads, which are excluded from Top chats.
	ParentThreadID *string
}

// AnalyticsChatPage is one thread-list page.
type AnalyticsChatPage struct {
	Data       []AnalyticsChatThread
	NextCursor *string
}

// AnalyticsChatLister reads one page of recent local roots. Rust reads the
// same pages from the app server's thread/list. A nil lister leaves Top chats
// unavailable.
type AnalyticsChatLister func(ctx context.Context, cursor *string, limit int) (AnalyticsChatPage, error)

// LoadAnalyticsChats discovers recent roots and loads their credit estimates.
// It returns (nil, nil) when the account cannot use chat estimates or no
// listing source is available, which the dashboard renders as unavailable
// (Rust chats::read).
func LoadAnalyticsChats(ctx context.Context, live *Live, lister AnalyticsChatLister) (*AnalyticsChats, error) {
	if live == nil || lister == nil {
		return nil, nil
	}
	session, err := live.Session(ctx)
	if err != nil {
		return nil, err
	}
	if !AnalyticsThreadUsageSupported(session.Backend.Account().PlanType) {
		return nil, nil
	}
	if err := session.Backend.EnsureIdentity(); err != nil {
		return nil, err
	}
	rootsCtx, cancel := context.WithTimeout(ctx, analyticsChatRootsTimeout)
	defer cancel()
	threads, err := analyticsChatRoots(rootsCtx, lister)
	if err != nil {
		if ctx.Err() == nil && errors.Is(rootsCtx.Err(), context.DeadlineExceeded) {
			return nil, errors.New(analyticsChatListingTimedOutMessage)
		}
		return nil, err
	}
	if err := session.Backend.EnsureIdentity(); err != nil {
		return nil, err
	}
	if len(threads) == 0 {
		return &AnalyticsChats{Rows: []AnalyticsChat{}}, nil
	}
	ids := make([]string, 0, len(threads))
	for _, thread := range threads {
		ids = append(ids, thread.ID)
	}
	// Repairing local history has its own budget; it must not consume estimate
	// time.
	deadline := time.Now().Add(analyticsChatEstimateBudget)
	var data []chatgptapi.ThreadUsage
	for start := 0; start < len(ids); start += analyticsChatEstimateChunk {
		end := start + analyticsChatEstimateChunk
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		midpoint := len(chunk)
		if midpoint > analyticsChatEstimateBatch {
			midpoint = analyticsChatEstimateBatch
		}
		rows, deadlineHit, err := analyticsChatEstimateHalves(ctx, session, chunk[:midpoint], chunk[midpoint:], deadline)
		if err != nil {
			return nil, err
		}
		data = append(data, rows...)
		// A half that ran out of estimate time leaves only its own rows
		// unavailable; the other half's healthy estimates are retained.
		if deadlineHit || !time.Now().Before(deadline) {
			break
		}
	}
	if err := session.Backend.EnsureIdentity(); err != nil {
		return nil, err
	}
	usage := make(map[string]chatgptapi.ThreadUsage, len(data))
	for _, row := range data {
		usage[row.ThreadID] = row
	}
	rows := make([]AnalyticsChat, 0, len(threads))
	for _, thread := range threads {
		chat := AnalyticsChat{Title: analyticsChatTitle(thread)}
		if row, ok := usage[thread.ID]; ok {
			estimated := row
			chat.Usage = &estimated
		}
		rows = append(rows, chat)
	}
	sort.SliceStable(rows, func(left, right int) bool {
		leftUsage, rightUsage := rows[left].Usage, rows[right].Usage
		switch {
		case leftUsage == nil && rightUsage == nil:
			return false
		case leftUsage == nil:
			return false
		case rightUsage == nil:
			return true
		default:
			return leftUsage.EstimatedUsageCreditsMicros > rightUsage.EstimatedUsageCreditsMicros
		}
	})
	return &AnalyticsChats{Rows: rows}, nil
}

// analyticsChatEstimateHalves runs both halves of one chunk concurrently, each
// bounded by the shared estimate deadline (Rust tokio::join! with timeout_at).
// Rows from a half that ran out of time are dropped while the other half's
// available estimates are kept.
func analyticsChatEstimateHalves(
	ctx context.Context,
	session *AnalyticsLiveSession,
	first []string,
	second []string,
	deadline time.Time,
) ([]chatgptapi.ThreadUsage, bool, error) {
	deadlineCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	parts := [2][]string{first, second}
	results := make([][]chatgptapi.ThreadUsage, len(parts))
	errs := make([]error, len(parts))
	var wg sync.WaitGroup
	for index, part := range parts {
		if len(part) == 0 {
			continue
		}
		wg.Add(1)
		go func(index int, part []string) {
			defer wg.Done()
			results[index], errs[index] = analyticsChatEstimates(deadlineCtx, session, part)
		}(index, part)
	}
	wg.Wait()
	// The estimate deadline cancels in-flight halves; Rust drops those futures
	// instead of surfacing their timeout.
	deadlineHit := deadlineCtx.Err() != nil && ctx.Err() == nil
	var rows []chatgptapi.ThreadUsage
	for index := range parts {
		if errs[index] != nil {
			if ctx.Err() != nil {
				return nil, false, ctx.Err()
			}
			if deadlineHit {
				continue
			}
			return nil, false, analyticsChatRequestError(errs[index])
		}
		rows = append(rows, results[index]...)
	}
	return rows, deadlineHit, nil
}

// analyticsChatEstimates isolates unavailable estimates within the existing
// concurrency slot and keeps healthy rows. At most 15 requests are sent per
// half, so a 503 outage leaves the remaining estimates unavailable
// (Rust chats::estimates).
func analyticsChatEstimates(ctx context.Context, session *AnalyticsLiveSession, ids []string) ([]chatgptapi.ThreadUsage, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	pending := [][]string{ids}
	var rows []chatgptapi.ThreadUsage
	for request := 0; request < analyticsChatEstimateRequests; request++ {
		if len(pending) == 0 {
			break
		}
		batch := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		usage, err := RequestAnalytics(ctx, session.Backend, func(ctx context.Context, client *chatgptapi.CloudClient) ([]chatgptapi.ThreadUsage, error) {
			return client.GetThreadsUsage(ctx, batch)
		})
		if err == nil {
			rows = append(rows, usage...)
			continue
		}
		var statusErr *chatgptapi.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.IsStatus(http.StatusServiceUnavailable) {
			// A temporary outage can affect only one row; bisect the batch so
			// healthy estimates still load.
			if len(batch) > 1 {
				midpoint := (len(batch) + 1) / 2
				pending = append(pending, batch[midpoint:], batch[:midpoint])
			}
			continue
		}
		return nil, err
	}
	return rows, nil
}

// analyticsChatRequestError maps backend failures onto the dashboard's report
// guidance while keeping account and sign-in messages intact.
func analyticsChatRequestError(err error) error {
	var statusErr *chatgptapi.HTTPStatusError
	if errors.As(err, &statusErr) {
		return errors.New(analyticsRequestErrorMessage(err))
	}
	return err
}

// analyticsChatRoots pages recent roots ordered by last update and stops at the
// first page that reaches the 30-day cutoff (Rust chats::roots).
func analyticsChatRoots(ctx context.Context, lister AnalyticsChatLister) ([]AnalyticsChatThread, error) {
	cutoff := time.Now().Unix() - int64(analyticsChatWindow/time.Second)
	var threads []AnalyticsChatThread
	var cursor *string
	seenCursors := map[string]struct{}{}
	for {
		page, err := lister(ctx, cursor, analyticsChatListPageSize)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, errors.New(analyticsChatListingFailedMessage)
		}
		reachedCutoff := false
		for _, thread := range page.Data {
			if thread.UpdatedAt < cutoff {
				reachedCutoff = true
			}
			if thread.UpdatedAt >= cutoff && !thread.Ephemeral && analyticsIsRootThread(thread) {
				threads = append(threads, thread)
			}
		}
		if page.NextCursor == nil || strings.TrimSpace(*page.NextCursor) == "" {
			break
		}
		if _, seen := seenCursors[*page.NextCursor]; seen {
			return nil, errors.New(analyticsChatRepeatedCursorMessage)
		}
		seenCursors[*page.NextCursor] = struct{}{}
		if reachedCutoff {
			break
		}
		cursor = page.NextCursor
	}
	// Keep repeated rows from overlapping pages out of the backend's distinct-ID
	// batches.
	seenIDs := make(map[string]struct{}, len(threads))
	unique := threads[:0]
	for _, thread := range threads {
		if _, seen := seenIDs[thread.ID]; seen {
			continue
		}
		seenIDs[thread.ID] = struct{}{}
		unique = append(unique, thread)
	}
	return unique, nil
}

func analyticsIsRootThread(thread AnalyticsChatThread) bool {
	return thread.ParentThreadID == nil || strings.TrimSpace(*thread.ParentThreadID) == ""
}

// analyticsChatTitle prefers the stored name, then the preview, and finally a
// placeholder (Rust chats::read).
func analyticsChatTitle(thread AnalyticsChatThread) string {
	if thread.Name != nil && strings.TrimSpace(*thread.Name) != "" {
		return *thread.Name
	}
	if strings.TrimSpace(thread.Preview) == "" {
		return analyticsChatUntitledTitle
	}
	return thread.Preview
}
