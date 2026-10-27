package tui

// SessionPageLoadMode is the source strategy for one picker page. It mirrors
// Rust resume_picker::PageLoadMode.
type SessionPageLoadMode string

const (
	// SessionPageLoadStoreDefault lets the backend choose its normal history
	// source.
	SessionPageLoadStoreDefault SessionPageLoadMode = "store_default"
	// SessionPageLoadStateDBOnly is a fast first page whose empty or failed
	// fallback is retried through the store's default source (Rust #57215).
	SessionPageLoadStateDBOnly SessionPageLoadMode = "state_db_only"
)

// SessionPage is one page returned by a resume-picker source. Rows carry the
// backend that listed them, and NextCursor belongs to that source alone.
type SessionPage struct {
	Rows            []SessionSummary
	NextCursor      *string
	NumScannedFiles int
	ReachedScanCap  bool
}

// SessionCatalogPageRequest identifies an in-flight page for one backend.
// Token is opaque to callers and is used to reject stale completions after a
// reset or a newer request.
type SessionCatalogPageRequest struct {
	BackendID BackendID
	Cursor    *string
	Mode      SessionPageLoadMode
	token     uint64
}

type threadCatalogSource struct {
	cursor    *string
	mode      SessionPageLoadMode
	pending   uint64
	hasToken  bool
	exhausted bool
}

// ThreadCatalog tracks pagination independently per backend. It mirrors Rust
// resume_picker/catalog.rs: a slow or exhausted source does not stop another,
// errors retain the source cursor for retry, and reset invalidates every
// pending request for that source.
type ThreadCatalog struct {
	sources     map[BackendID]*threadCatalogSource
	nextRequest uint64
}

// NewThreadCatalog returns an empty catalog. The zero value is also usable.
func NewThreadCatalog() *ThreadCatalog {
	return &ThreadCatalog{sources: map[BackendID]*threadCatalogSource{}}
}

// Reset clears every source cursor and invalidates all pending completions.
func (c *ThreadCatalog) Reset() {
	if c == nil {
		return
	}
	clear(c.sources)
	c.sources = map[BackendID]*threadCatalogSource{}
}

// HasMore reports whether any source that is not excluded still has pages.
func (c *ThreadCatalog) HasMore(excluded map[BackendID]bool) bool {
	if c == nil {
		return false
	}
	for backendID, source := range c.sources {
		if source != nil && !source.exhausted && !excluded[backendID] {
			return true
		}
	}
	return false
}

// BeginPage reserves the next page for a backend. It returns false while that
// backend already has a pending page or has exhausted its source. The first
// call for a source adopts initialMode; later pages keep the mode inherited by
// the previous page.
func (c *ThreadCatalog) BeginPage(backendID BackendID, initialMode SessionPageLoadMode) (SessionCatalogPageRequest, bool) {
	if c == nil {
		return SessionCatalogPageRequest{}, false
	}
	if c.sources == nil {
		c.sources = map[BackendID]*threadCatalogSource{}
	}
	source := c.sources[backendID]
	if source == nil {
		source = &threadCatalogSource{}
		c.sources[backendID] = source
	}
	if source.hasToken || source.exhausted {
		return SessionCatalogPageRequest{}, false
	}
	if source.mode == "" {
		source.mode = initialMode
		if source.mode == "" {
			source.mode = SessionPageLoadStoreDefault
		}
	}
	request := SessionCatalogPageRequest{
		BackendID: backendID,
		Cursor:    cloneCatalogCursor(source.cursor),
		Mode:      source.mode,
		token:     c.nextRequest,
	}
	c.nextRequest++
	source.pending = request.token
	source.hasToken = true
	return request, true
}

// FinishPage accepts a page only when it belongs to the current request for
// that backend. A stale completion returns accepted=false and changes nothing.
// Errors leave the cursor untouched so the source can be retried; successful
// rows are stamped with the request's backend.
func (c *ThreadCatalog) FinishPage(request SessionCatalogPageRequest, page SessionPage, pageErr error) (SessionPage, error, bool) {
	if c == nil {
		return SessionPage{}, pageErr, false
	}
	source := c.sources[request.BackendID]
	if source == nil || !source.hasToken || source.pending != request.token {
		return SessionPage{}, pageErr, false
	}
	source.hasToken = false
	source.mode = request.Mode
	if pageErr != nil {
		return SessionPage{}, pageErr, true
	}
	source.cursor = cloneCatalogCursor(page.NextCursor)
	source.exhausted = source.cursor == nil
	for i := range page.Rows {
		page.Rows[i].BackendID = request.BackendID
	}
	return page, nil, true
}

// StampSessionSummariesBackend assigns backendID to rows that do not already
// name their source. It preserves rows listed by another source so a merged
// picker can route each selection back to its owner (Rust #56543).
func StampSessionSummariesBackend(items []SessionSummary, backendID BackendID) []SessionSummary {
	for i := range items {
		if items[i].BackendID.IsZero() {
			items[i].BackendID = backendID
		}
	}
	return items
}

func cloneCatalogCursor(cursor *string) *string {
	if cursor == nil {
		return nil
	}
	value := *cursor
	return &value
}
