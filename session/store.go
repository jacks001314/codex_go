package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	ForkAll   ForkMode = "all"
	ForkNone  ForkMode = "none"
	ForkLastN ForkMode = "last-n"

	SortCreatedAt       SortKey = "created_at"
	SortUpdatedAt       SortKey = "updated_at"
	SortRecencyAt       SortKey = "recency_at"
	SortSectionPosition SortKey = "section_position"

	SortAsc  SortDirection = "asc"
	SortDesc SortDirection = "desc"
)

var (
	ErrInvalidThreadID      = errors.New("invalid thread id")
	ErrThreadNotFound       = errors.New("thread not found")
	ErrThreadArchived       = errors.New("thread archived")
	ErrThreadSectionMissing = errors.New("thread section not found")
	ErrConflict             = errors.New("thread store conflict")
)

const (
	PinnedThreadSectionID   = "01984de2-8f74-7c91-a3b2-5c5e937cf318"
	PinnedThreadSectionName = "Pinned"
	sectionPositionGap      = int64(1_000_000)
)

type ForkMode string

type SortKey string

type SortDirection string

type ThreadID string

type ThreadSection struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	Appearance *ThreadSectionAppearance `json:"appearance,omitempty"`
}

// ThreadSectionAppearance carries optional visual presentation metadata for a
// custom thread section (mirrors the Rust ThreadSectionAppearance protocol
// type). Both fields are limited to 64 bytes.
type ThreadSectionAppearance struct {
	Icon  *string `json:"icon,omitempty"`
	Color *string `json:"color,omitempty"`
}

type Item struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Role             string          `json:"role,omitempty"`
	Text             string          `json:"text,omitempty"`
	Name             string          `json:"name,omitempty"`
	Namespace        string          `json:"namespace,omitempty"`
	CallID           string          `json:"call_id,omitempty"`
	Status           string          `json:"status,omitempty"`
	Content          []ContentPart   `json:"content,omitempty"`
	Data             map[string]any  `json:"data,omitempty"`
	Raw              json.RawMessage `json:"raw,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	ResponseID       string          `json:"response_id,omitempty"`
	Metadata         map[string]any  `json:"metadata,omitempty"`
	CreatedAtOrdinal uint64          `json:"created_at_ordinal,omitempty"`
	UpdatedAtOrdinal uint64          `json:"updated_at_ordinal,omitempty"`
}

func (s *Store) ListItems(threadID ThreadID, options ListItemsOptions) (*ItemPage, error) {
	record, err := s.Read(threadID, true, true)
	if err != nil {
		return nil, err
	}
	if options.AfterUpdatedAtOrdinal != nil && record.ForkedFromID != "" {
		return nil, fmt.Errorf("%w: incremental item replay is not supported for forked threads", ErrInvalidThreadID)
	}
	sortKey := options.SortKey
	if sortKey == "" {
		sortKey = ItemSortCreatedAtOrdinal
	}
	if sortKey == ItemSortUpdatedAtOrdinal && options.AfterUpdatedAtOrdinal == nil {
		return nil, fmt.Errorf("%w: update-ordinal item sorting requires an update watermark", ErrInvalidThreadID)
	}
	if sortKey != ItemSortCreatedAtOrdinal && sortKey != ItemSortUpdatedAtOrdinal {
		return nil, fmt.Errorf("%w: unsupported item sort key %q", ErrInvalidThreadID, sortKey)
	}
	cursorScope, cursorOrdinal, err := parseItemCursor(options.Cursor)
	if err != nil {
		return nil, err
	}
	if cursorScope != "" && cursorScope != sortKey {
		return nil, fmt.Errorf("%w: item cursor sort key does not match request", ErrInvalidThreadID)
	}
	items := cloneItems(record.Items)
	for i := range items {
		if items[i].CreatedAtOrdinal == 0 {
			items[i].CreatedAtOrdinal = uint64(i + 1)
		}
		if items[i].UpdatedAtOrdinal == 0 {
			items[i].UpdatedAtOrdinal = items[i].CreatedAtOrdinal
		}
	}
	filtered := items[:0]
	for i := range items {
		item := items[i]
		if options.TurnID != "" && itemTurnID(&item, i) != options.TurnID {
			continue
		}
		if options.AfterUpdatedAtOrdinal != nil && item.UpdatedAtOrdinal <= *options.AfterUpdatedAtOrdinal {
			continue
		}
		ordinal := item.CreatedAtOrdinal
		if sortKey == ItemSortUpdatedAtOrdinal {
			ordinal = item.UpdatedAtOrdinal
		}
		if cursorOrdinal > 0 {
			if options.SortDirection == SortDesc && ordinal >= cursorOrdinal {
				continue
			}
			if options.SortDirection != SortDesc && ordinal <= cursorOrdinal {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left, right := filtered[i].CreatedAtOrdinal, filtered[j].CreatedAtOrdinal
		if sortKey == ItemSortUpdatedAtOrdinal {
			left, right = filtered[i].UpdatedAtOrdinal, filtered[j].UpdatedAtOrdinal
		}
		if options.SortDirection == SortDesc {
			return left > right
		}
		return left < right
	})
	limit := options.PageSize
	if limit <= 0 || limit > len(filtered) {
		limit = len(filtered)
	}
	pageItems := append([]Item(nil), filtered[:limit]...)
	next := ""
	if limit < len(filtered) && limit > 0 {
		last := pageItems[len(pageItems)-1]
		ordinal := last.CreatedAtOrdinal
		if sortKey == ItemSortUpdatedAtOrdinal {
			ordinal = last.UpdatedAtOrdinal
		}
		next = fmt.Sprintf("%s:%d", sortKey, ordinal)
	}
	return &ItemPage{Items: pageItems, NextCursor: next}, nil
}

func parseItemCursor(cursor string) (ItemSortKey, uint64, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return "", 0, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("%w: invalid item cursor", ErrInvalidThreadID)
	}
	ordinal, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("%w: invalid item cursor", ErrInvalidThreadID)
	}
	return ItemSortKey(parts[0]), ordinal, nil
}

type ItemSortKey string

const (
	ItemSortCreatedAtOrdinal ItemSortKey = "created_at_ordinal"
	ItemSortUpdatedAtOrdinal ItemSortKey = "updated_at_ordinal"
)

type ListItemsOptions struct {
	TurnID                string
	Cursor                string
	PageSize              int
	SortDirection         SortDirection
	SortKey               ItemSortKey
	AfterUpdatedAtOrdinal *uint64
}

type ItemPage struct {
	Items      []Item
	NextCursor string
}

type ContentPart struct {
	Type     string  `json:"type"`
	Text     string  `json:"text,omitempty"`
	ImageURL string  `json:"image_url,omitempty"`
	AudioURL string  `json:"audio_url,omitempty"`
	Detail   *string `json:"detail,omitempty"`
}

type Metadata struct {
	CWD                        string                      `json:"cwd,omitempty"`
	Model                      string                      `json:"model,omitempty"`
	ModelProvider              string                      `json:"model_provider,omitempty"`
	Source                     string                      `json:"source,omitempty"`
	ThreadSource               string                      `json:"thread_source,omitempty"`
	Originator                 string                      `json:"originator,omitempty"`
	HistoryMode                string                      `json:"history_mode,omitempty"`
	MemoryMode                 string                      `json:"memory_mode,omitempty"`
	Git                        map[string]string           `json:"git,omitempty"`
	BaseInstructions           string                      `json:"base_instructions,omitempty"`
	BaseInstructionsProvenance *BaseInstructionsProvenance `json:"base_instructions_provenance,omitempty"`
	Instructions               string                      `json:"instructions,omitempty"`
	ApprovalPolicy             string                      `json:"approval_policy,omitempty"`
	SandboxPolicy              string                      `json:"sandbox_policy,omitempty"`
	ServiceTier                string                      `json:"service_tier,omitempty"`
	PromptCacheKey             string                      `json:"prompt_cache_key,omitempty"`
	PreviousResponseID         string                      `json:"previous_response_id,omitempty"`
	LastResponseID             string                      `json:"last_response_id,omitempty"`
	SessionPrefix              string                      `json:"session_prefix,omitempty"`
	CLIVersion                 string                      `json:"cli_version,omitempty"`
	AgentNickname              string                      `json:"agent_nickname,omitempty"`
	AgentRole                  string                      `json:"agent_role,omitempty"`
	AgentPath                  string                      `json:"agent_path,omitempty"`
	AgentDepth                 int                         `json:"agent_depth,omitempty"`
	DynamicTools               []json.RawMessage           `json:"dynamic_tools,omitempty"`
	SelectedCapabilityRoots    []json.RawMessage           `json:"selected_capability_roots,omitempty"`
	MultiAgentVersion          string                      `json:"multi_agent_version,omitempty"`
	ContextWindow              json.RawMessage             `json:"context_window,omitempty"`
	TurnContext                json.RawMessage             `json:"turn_context,omitempty"`
	WorldState                 json.RawMessage             `json:"world_state,omitempty"`
	SelectedModelProvider      string                      `json:"selected_model_provider,omitempty"`
	ElicitationCount           int                         `json:"elicitation_count,omitempty"`
	Extra                      map[string]any              `json:"extra,omitempty"`
	RolloutTurns               []TurnSnapshot              `json:"rollout_turns,omitempty"`
	QueuedSubmissions          []QueuedSubmission          `json:"queued_submissions,omitempty"`
}

// BaseInstructionsProvenance records whether persisted instructions were explicit or
// generated from a model template. Model-generated instructions are recomputed when
// a resumed turn selects a different model or personality.
type BaseInstructionsProvenance struct {
	Type  string `json:"type"`
	Model string `json:"model,omitempty"`
}

const (
	BaseInstructionsProvenanceCustom = "custom"
	BaseInstructionsProvenanceModel  = "model"
)

type TurnSnapshot struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	StartedAt      *int64 `json:"started_at,omitempty"`
	CompletedAt    *int64 `json:"completed_at,omitempty"`
	DurationMS     *int64 `json:"duration_ms,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
	CodexErrorInfo any    `json:"codex_error_info,omitempty"`
}

// QueuedSubmission is a persistent queued user submission awaiting dispatch
// (Rust #38456). Input mirrors the protocol's user-input item array.
type QueuedSubmission struct {
	ID                  string `json:"id"`
	Input               []any  `json:"input"`
	ClientUserMessageID string `json:"client_user_message_id"`
}

type Record struct {
	ID               ThreadID         `json:"id"`
	SessionID        string           `json:"session_id,omitempty"`
	ForkedFromID     ThreadID         `json:"forked_from_id,omitempty"`
	ParentThreadID   ThreadID         `json:"parent_thread_id,omitempty"`
	Title            string           `json:"title,omitempty"`
	Preview          string           `json:"preview,omitempty"`
	Archived         bool             `json:"archived"`
	Section          *ThreadSection   `json:"section,omitempty"`
	SectionPosition  *int64           `json:"section_position,omitempty"`
	SectionEnteredAt *time.Time       `json:"section_entered_at,omitempty"`
	IsPinned         bool             `json:"is_pinned,omitempty"` // Legacy on-disk migration field.
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	RecencyAt        time.Time        `json:"recency_at"`
	Metadata         Metadata         `json:"metadata"`
	HistoryBase      *HistoryPosition `json:"history_base,omitempty"`
	Items            []Item           `json:"items,omitempty"`
	FromRollout      bool             `json:"-"`
	ItemCount        int              `json:"-"`
	InheritedItems   int              `json:"-"`
	InheritedTurns   int              `json:"-"`
}

// HistoryPosition freezes the materialized prefix inherited by a paginated
// fork. The JSON store has item and turn ordinals rather than Rust's physical
// rollout byte offsets, so the prefix lengths are the durable equivalent.
type HistoryPosition struct {
	ThreadID            ThreadID `json:"thread_id"`
	EndOrdinalExclusive uint64   `json:"end_ordinal_exclusive,omitempty"`
	EndByteOffset       uint64   `json:"end_byte_offset,omitempty"`
	ItemEnd             int      `json:"item_end_exclusive,omitempty"`
	TurnEnd             int      `json:"turn_end_exclusive,omitempty"`
}

type MetadataPatch struct {
	Title                   *string
	Preview                 *string
	Archived                *bool
	IsPinned                *bool
	SectionSet              bool
	SectionID               *string
	CWD                     *string
	Model                   *string
	ModelProvider           *string
	Source                  *string
	ThreadSource            *string
	Originator              *string
	HistoryMode             *string
	MemoryMode              *string
	Git                     map[string]string
	BaseInstructions        *string
	Instructions            *string
	ApprovalPolicy          *string
	SandboxPolicy           *string
	ServiceTier             *string
	PromptCacheKey          *string
	PreviousResponseID      *string
	LastResponseID          *string
	SessionPrefix           *string
	CLIVersion              *string
	AgentNickname           *string
	AgentRole               *string
	AgentPath               *string
	AgentDepth              *int
	DynamicTools            []json.RawMessage
	SelectedCapabilityRoots []json.RawMessage
	MultiAgentVersion       *string
	ContextWindow           json.RawMessage
	TurnContext             json.RawMessage
	WorldState              json.RawMessage
	SelectedModelProvider   *string
	Extra                   map[string]any
}

type ListOptions struct {
	PageSize       int
	Cursor         string
	SortKey        SortKey
	SortDirection  SortDirection
	Archived       bool
	IsPinned       *bool
	SectionSet     bool
	SectionID      *string
	Search         string
	ModelProviders []string
	CWDs           []string
	Sources        []string
	SourceKinds    []string
	Relation       *RelationFilter
	IncludeHistory bool
}

type RelationFilter struct {
	DirectChildrenOf ThreadID
	DescendantsOf    ThreadID
}

type Page struct {
	Records         []Record
	NextCursor      string
	BackwardsCursor string
}

type MessageOccurrence struct {
	TurnID string
	ItemID string
	Text   string
	Start  int
	End    int
}

func (s *Store) SearchMessageOccurrences(threadID ThreadID, searchTerm string) ([]MessageOccurrence, error) {
	record, err := s.Read(threadID, true, true)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(searchTerm))
	if needle == "" {
		return []MessageOccurrence{}, nil
	}
	out := make([]MessageOccurrence, 0)
	for _, item := range record.Items {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		itemType := strings.ToLower(strings.TrimSpace(item.Type))
		if role != "user" && role != "assistant" && itemType != "user_message" && itemType != "assistant_message" && itemType != "agent_message" {
			continue
		}
		if role == "assistant" {
			phase, _ := item.Data["phase"].(string)
			if strings.TrimSpace(phase) != "final_answer" {
				continue
			}
		}
		text := item.Text
		if text == "" {
			parts := make([]string, 0, len(item.Content))
			for _, part := range item.Content {
				if strings.TrimSpace(part.Text) != "" {
					parts = append(parts, part.Text)
				}
			}
			text = strings.Join(parts, "\n")
		}
		lower := strings.ToLower(text)
		for from := 0; from <= len(lower); {
			rel := strings.Index(lower[from:], needle)
			if rel < 0 {
				break
			}
			start := from + rel
			end := start + len(needle)
			turnID, _ := item.Data["turn_id"].(string)
			if turnID == "" {
				turnID, _ = item.Data["turnId"].(string)
			}
			out = append(out, MessageOccurrence{TurnID: turnID, ItemID: item.ID, Text: text, Start: start, End: end})
			from = end
		}
	}
	return out, nil
}

type ForkOptions struct {
	NewID          ThreadID
	Mode           ForkMode
	LastN          int
	LastTurnID     string
	BeforeTurnID   string
	Title          string
	SessionID      string
	ParentThreadID ThreadID
	Ephemeral      bool
	HistoryBase    *HistoryPosition
	HistoryBaseSet bool
	Now            time.Time
}

type ForkBoundaryKind string

const (
	ForkBoundaryLatest      ForkBoundaryKind = "latest"
	ForkBoundaryThroughTurn ForkBoundaryKind = "throughTurn"
	ForkBoundaryBeforeTurn  ForkBoundaryKind = "beforeTurn"
)

type ForkBoundary struct {
	Kind   ForkBoundaryKind
	TurnID string
}

type PrepareForkParams struct {
	Mode     ForkMode
	LastN    int
	Boundary ForkBoundary
}

type PreparedFork struct {
	SourceID     ThreadID
	HistoryBase  *HistoryPosition
	Items        []Item
	RolloutTurns []TurnSnapshot
}

type ResolvedPhysicalHistory struct {
	Items        []Item
	RolloutTurns []TurnSnapshot
}

type PhysicalHistoryResolver func(HistoryPosition) (*ResolvedPhysicalHistory, error)

type Store struct {
	root                  string
	mu                    *sync.RWMutex
	resolverMu            sync.RWMutex
	physicalResolver      PhysicalHistoryResolver
	writerLocksOnce       sync.Once
	writerLockCoordinator *writerLockCoordinator
	customSections        []ThreadSection
}

var storeRootLocks sync.Map

func NewStore(root string) *Store {
	store := &Store{root: root, mu: storeRootLock(root)}
	store.loadCustomSectionsLocked()
	return store
}

func storeRootLock(root string) *sync.RWMutex {
	key := filepath.Clean(root)
	if absolute, err := filepath.Abs(key); err == nil {
		key = absolute
	}
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	lock, _ := storeRootLocks.LoadOrStore(key, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}

func (s *Store) Root() string {
	return s.root
}

// SetPhysicalHistoryResolver installs the rollout-backed reader used for
// Rust-compatible paginated history references. The resolver is read-only and
// is never used for legacy item/turn-count references.
func (s *Store) SetPhysicalHistoryResolver(resolver PhysicalHistoryResolver) {
	if s == nil {
		return
	}
	s.resolverMu.Lock()
	s.physicalResolver = resolver
	s.resolverMu.Unlock()
}

func (s *Store) resolvePhysicalHistory(position HistoryPosition) (*ResolvedPhysicalHistory, error) {
	s.resolverMu.RLock()
	resolver := s.physicalResolver
	s.resolverMu.RUnlock()
	if resolver == nil {
		return nil, errors.New("physical history resolver is not configured")
	}
	return resolver(position)
}

func (s *Store) Path(threadID ThreadID) (string, error) {
	if err := validateThreadID(threadID); err != nil {
		return "", err
	}
	return filepath.Join(s.root, string(threadID)+".json"), nil
}

func (s *Store) Save(record *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(record)
}

func (s *Store) saveLocked(record *Record) error {
	if record == nil {
		return fmt.Errorf("%w: record is nil", ErrInvalidThreadID)
	}
	if err := validateThreadID(record.ID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}
	if record.RecencyAt.IsZero() {
		record.RecencyAt = record.UpdatedAt
	}
	for i := range record.Items {
		if record.Items[i].CreatedAt.IsZero() {
			record.Items[i].CreatedAt = record.CreatedAt
		}
	}
	physical := cloneRecord(record)
	if physical.HistoryBase != nil {
		if physical.InheritedItems < 0 || physical.InheritedItems > len(physical.Items) {
			return fmt.Errorf("%w: invalid inherited item prefix for thread %s", ErrInvalidThreadID, physical.ID)
		}
		if physical.InheritedTurns < 0 || physical.InheritedTurns > len(physical.Metadata.RolloutTurns) {
			return fmt.Errorf("%w: invalid inherited turn prefix for thread %s", ErrInvalidThreadID, physical.ID)
		}
		physical.Items = cloneItems(physical.Items[physical.InheritedItems:])
		physical.Metadata.RolloutTurns = cloneTurnSnapshots(physical.Metadata.RolloutTurns[physical.InheritedTurns:])
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	path, err := s.Path(record.ID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(physical, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeStoreFileAtomically(path, data)
}

func (s *Store) Create(record *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(record)
}

func (s *Store) createLocked(record *Record) error {
	if record == nil {
		return fmt.Errorf("%w: record is nil", ErrInvalidThreadID)
	}
	path, err := s.Path(record.ID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%w: thread %s already exists", ErrConflict, record.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.saveLocked(record)
}

func (s *Store) Load(threadID ThreadID) (*Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked(threadID)
}

// Revert replaces a loaded paginated thread's durable history with the prefix
// before beforeTurnID while preserving the thread ID (Rust #38292/#38440). The
// materialized history is truncated to the retained prefix and persisted as the
// thread's own local history, dropping any inherited-history reference.
func (s *Store) Revert(threadID ThreadID, beforeTurnID string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.readMaterializedLocked(threadID, true, map[ThreadID]struct{}{})
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(record.Metadata.HistoryMode), "paginated") {
		return nil, fmt.Errorf("%w: thread %s does not use paginated history", ErrInvalidThreadID, threadID)
	}
	if strings.TrimSpace(beforeTurnID) == "" {
		return nil, fmt.Errorf("%w: beforeTurnId is required", ErrInvalidThreadID)
	}
	items, err := itemsBeforeTurn(record.Items, beforeTurnID)
	if err != nil {
		return nil, err
	}
	record.Items = items
	record.Metadata.RolloutTurns = forkTurnSnapshots(record.Metadata.RolloutTurns, items)
	record.InheritedItems = 0
	record.InheritedTurns = 0
	record.HistoryBase = nil
	record.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(record); err != nil {
		return nil, err
	}
	return record, nil
}

// MaxQueueItems is the maximum number of queued submissions retained per
// thread (Rust #38456).
const MaxQueueItems = 100

// EnqueueSubmission appends a queued user submission to a thread's durable
// queue (Rust #38456).
func (s *Store) EnqueueSubmission(threadID ThreadID, submission QueuedSubmission) (*QueuedSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.loadLocked(threadID)
	if err != nil {
		return nil, err
	}
	if len(record.Metadata.QueuedSubmissions) >= MaxQueueItems {
		return nil, fmt.Errorf("%w: queue cannot contain more than %d submissions", ErrConflict, MaxQueueItems)
	}
	submission = cloneQueuedSubmission(submission)
	if strings.TrimSpace(submission.ID) == "" {
		submission.ID = uuid.NewString()
	}
	record.Metadata.QueuedSubmissions = append(record.Metadata.QueuedSubmissions, submission)
	if err := s.saveLocked(record); err != nil {
		return nil, err
	}
	return cloneQueuedSubmissionPtr(&submission), nil
}

// ListQueueSubmissions returns a page of queued submissions. An empty cursor
// starts at the beginning; the returned cursor points to the next page.
func (s *Store) ListQueueSubmissions(threadID ThreadID, cursor string, limit int) ([]QueuedSubmission, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, err := s.loadLocked(threadID)
	if err != nil {
		return nil, "", err
	}
	if limit <= 0 {
		limit = MaxQueueItems
	}
	offset := 0
	if strings.TrimSpace(cursor) != "" {
		if parsed, parseErr := strconv.Atoi(cursor); parseErr == nil && parsed >= 0 {
			offset = parsed
		} else {
			return nil, "", fmt.Errorf("%w: invalid queue cursor", ErrInvalidThreadID)
		}
	}
	queue := record.Metadata.QueuedSubmissions
	if offset > len(queue) {
		offset = len(queue)
	}
	end := offset + limit
	if end > len(queue) {
		end = len(queue)
	}
	page := cloneQueuedSubmissions(queue[offset:end])
	nextCursor := ""
	if end < len(queue) {
		nextCursor = strconv.Itoa(end)
	}
	return page, nextCursor, nil
}

// UpdateQueueSubmission replaces the input of an existing queued submission.
func (s *Store) UpdateQueueSubmission(threadID ThreadID, submissionID string, input []any) (*QueuedSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.loadLocked(threadID)
	if err != nil {
		return nil, err
	}
	for i := range record.Metadata.QueuedSubmissions {
		if record.Metadata.QueuedSubmissions[i].ID != submissionID {
			continue
		}
		record.Metadata.QueuedSubmissions[i].Input = cloneAnySlice(input)
		if err := s.saveLocked(record); err != nil {
			return nil, err
		}
		return cloneQueuedSubmissionPtr(&record.Metadata.QueuedSubmissions[i]), nil
	}
	return nil, fmt.Errorf("%w: queued submission not found: %s", ErrThreadNotFound, submissionID)
}

// DeleteQueueSubmission removes one queued submission.
func (s *Store) DeleteQueueSubmission(threadID ThreadID, submissionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.loadLocked(threadID)
	if err != nil {
		return false, err
	}
	queue := record.Metadata.QueuedSubmissions
	for i := range queue {
		if queue[i].ID != submissionID {
			continue
		}
		record.Metadata.QueuedSubmissions = append(cloneQueuedSubmissions(queue[:i]), cloneQueuedSubmissions(queue[i+1:])...)
		if err := s.saveLocked(record); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// ReorderQueueSubmissions replaces the queue order with the provided IDs.
func (s *Store) ReorderQueueSubmissions(threadID ThreadID, submissionIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.loadLocked(threadID)
	if err != nil {
		return err
	}
	byID := make(map[string]QueuedSubmission, len(record.Metadata.QueuedSubmissions))
	for _, submission := range record.Metadata.QueuedSubmissions {
		byID[submission.ID] = submission
	}
	reordered := make([]QueuedSubmission, 0, len(submissionIDs))
	for _, id := range submissionIDs {
		submission, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: queued submission not found: %s", ErrThreadNotFound, id)
		}
		reordered = append(reordered, cloneQueuedSubmission(submission))
	}
	if len(reordered) != len(record.Metadata.QueuedSubmissions) {
		return fmt.Errorf("%w: reorder must include every queued submission", ErrInvalidThreadID)
	}
	record.Metadata.QueuedSubmissions = reordered
	return s.saveLocked(record)
}

// DequeueFirstSubmission removes and returns the front of the queue.
func (s *Store) DequeueFirstSubmission(threadID ThreadID) (*QueuedSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.loadLocked(threadID)
	if err != nil {
		return nil, err
	}
	if len(record.Metadata.QueuedSubmissions) == 0 {
		return nil, nil
	}
	first := cloneQueuedSubmission(record.Metadata.QueuedSubmissions[0])
	record.Metadata.QueuedSubmissions = cloneQueuedSubmissions(record.Metadata.QueuedSubmissions[1:])
	if err := s.saveLocked(record); err != nil {
		return nil, err
	}
	return &first, nil
}

// DequeueSubmission removes and returns a specific submission, or the front of
// the queue when submissionID is empty.
func (s *Store) DequeueSubmission(threadID ThreadID, submissionID string) (*QueuedSubmission, error) {
	if strings.TrimSpace(submissionID) == "" {
		return s.DequeueFirstSubmission(threadID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.loadLocked(threadID)
	if err != nil {
		return nil, err
	}
	for i := range record.Metadata.QueuedSubmissions {
		if record.Metadata.QueuedSubmissions[i].ID != submissionID {
			continue
		}
		submission := cloneQueuedSubmission(record.Metadata.QueuedSubmissions[i])
		record.Metadata.QueuedSubmissions = append(cloneQueuedSubmissions(record.Metadata.QueuedSubmissions[:i]), cloneQueuedSubmissions(record.Metadata.QueuedSubmissions[i+1:])...)
		if err := s.saveLocked(record); err != nil {
			return nil, err
		}
		return &submission, nil
	}
	return nil, fmt.Errorf("%w: queued submission not found: %s", ErrThreadNotFound, submissionID)
}

func (s *Store) loadLocked(threadID ThreadID) (*Record, error) {
	path, err := s.Path(threadID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrThreadNotFound, threadID)
	}
	if err != nil {
		return nil, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if record.ID == "" {
		record.ID = threadID
	}
	if err := validateThreadID(record.ID); err != nil {
		return nil, err
	}
	normalizeRecordSection(&record)
	return &record, nil
}

func (s *Store) Read(threadID ThreadID, includeArchived bool, includeHistory bool) (*Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readLocked(threadID, includeArchived, includeHistory)
}

func (s *Store) readLocked(threadID ThreadID, includeArchived bool, includeHistory bool) (*Record, error) {
	record, err := s.readMaterializedLocked(threadID, includeArchived, map[ThreadID]struct{}{})
	if err != nil {
		return nil, err
	}
	if !includeHistory {
		record.Items = nil
	}
	return record, nil
}

func (s *Store) readMaterializedLocked(threadID ThreadID, includeArchived bool, visiting map[ThreadID]struct{}) (*Record, error) {
	if _, ok := visiting[threadID]; ok {
		return nil, fmt.Errorf("%w: cyclic history reference at thread %s", ErrInvalidThreadID, threadID)
	}
	visiting[threadID] = struct{}{}
	defer delete(visiting, threadID)
	record, err := s.loadLocked(threadID)
	if err != nil {
		return nil, err
	}
	if record.Archived && !includeArchived {
		return nil, fmt.Errorf("%w: %s", ErrThreadArchived, threadID)
	}
	if record.HistoryBase == nil {
		return record, nil
	}
	base := *record.HistoryBase
	if base.ThreadID == "" || base.ThreadID == record.ID || base.ItemEnd < 0 || base.TurnEnd < 0 {
		return nil, fmt.Errorf("%w: invalid history reference for thread %s", ErrInvalidThreadID, threadID)
	}
	if base.EndOrdinalExclusive != 0 || base.EndByteOffset != 0 {
		resolved, resolveErr := s.resolvePhysicalHistory(base)
		if resolveErr != nil {
			return nil, fmt.Errorf("%w: resolve physical history base for thread %s: %v", ErrInvalidThreadID, threadID, resolveErr)
		}
		if resolved == nil {
			return nil, fmt.Errorf("%w: physical history resolver returned no history for thread %s", ErrInvalidThreadID, threadID)
		}
		localItems := cloneItems(record.Items)
		localTurns := cloneTurnSnapshots(record.Metadata.RolloutTurns)
		record.Items = append(cloneItems(resolved.Items), localItems...)
		record.Metadata.RolloutTurns = append(cloneTurnSnapshots(resolved.RolloutTurns), localTurns...)
		record.InheritedItems = len(resolved.Items)
		record.InheritedTurns = len(resolved.RolloutTurns)
		return record, nil
	}
	source, err := s.readMaterializedLocked(base.ThreadID, true, visiting)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve history base for thread %s: %v", ErrInvalidThreadID, threadID, err)
	}
	if base.ItemEnd > len(source.Items) || base.TurnEnd > len(source.Metadata.RolloutTurns) {
		return nil, fmt.Errorf("%w: history reference for thread %s exceeds source %s", ErrInvalidThreadID, threadID, base.ThreadID)
	}
	localItems := cloneItems(record.Items)
	localTurns := cloneTurnSnapshots(record.Metadata.RolloutTurns)
	record.Items = append(cloneItems(source.Items[:base.ItemEnd]), localItems...)
	record.Metadata.RolloutTurns = append(cloneTurnSnapshots(source.Metadata.RolloutTurns[:base.TurnEnd]), localTurns...)
	record.InheritedItems = base.ItemEnd
	record.InheritedTurns = base.TurnEnd
	return record, nil
}

func (s *Store) AppendItem(threadID ThreadID, item Item) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.readLocked(threadID, true, true)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	record.Items = append(record.Items, item)
	record.UpdatedAt = item.CreatedAt
	record.RecencyAt = item.CreatedAt
	if record.Preview == "" {
		record.Preview = itemPreviewText(&item)
	}
	if err := s.saveLocked(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) AppendItems(threadID ThreadID, items []Item) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.readLocked(threadID, true, true)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for i := range items {
		if items[i].CreatedAt.IsZero() {
			items[i].CreatedAt = now
		}
		record.Items = append(record.Items, items[i])
		record.UpdatedAt = items[i].CreatedAt
		record.RecencyAt = items[i].CreatedAt
		if record.Preview == "" {
			record.Preview = itemPreviewText(&items[i])
		}
	}
	if err := s.saveLocked(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) UpdateMetadata(threadID ThreadID, patch *MetadataPatch, includeArchived bool) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateMetadataLocked(threadID, patch, includeArchived)
}

// MoveThreadToSection moves a thread into, within, or out of a persisted
// section. Positions are sparse so most reorders only rewrite the moved thread.
func (s *Store) MoveThreadToSection(threadID ThreadID, sectionID *string, beforeThreadID *ThreadID) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateThreadID(threadID); err != nil {
		return nil, err
	}
	if beforeThreadID != nil {
		if err := validateThreadID(*beforeThreadID); err != nil {
			return nil, err
		}
	}
	section, err := s.threadSectionForIDLocked(sectionID)
	if err != nil {
		return nil, err
	}
	if section == nil && beforeThreadID != nil {
		return nil, fmt.Errorf("before thread cannot be specified without a section")
	}
	if beforeThreadID != nil && *beforeThreadID == threadID {
		return nil, fmt.Errorf("thread %s cannot be moved before itself", threadID)
	}
	records, err := s.loadAllLocked()
	if err != nil {
		return nil, err
	}
	targetIndex := -1
	for i := range records {
		if records[i].ID == threadID {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		return nil, fmt.Errorf("%w: %s", ErrThreadNotFound, threadID)
	}
	target := &records[targetIndex]
	if section == nil {
		target.Section = nil
		target.SectionPosition = nil
		target.SectionEnteredAt = nil
		target.IsPinned = false
		if err := s.saveLocked(target); err != nil {
			return nil, err
		}
		return cloneRecord(target), nil
	}

	members := make([]*Record, 0)
	for i := range records {
		record := &records[i]
		if record.ID != threadID && record.Section != nil && record.Section.ID == section.ID {
			members = append(members, record)
		}
	}
	if beforeThreadID != nil {
		found := false
		for _, member := range members {
			if member.ID == *beforeThreadID {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("before thread %s is not in section %s", *beforeThreadID, section.ID)
		}
	}

	changed := make([]*Record, 0)
	if sectionMembersNeedRanks(members) {
		sort.SliceStable(members, func(i, j int) bool {
			left, right := members[i], members[j]
			if left.SectionPosition != nil && right.SectionPosition != nil && *left.SectionPosition != *right.SectionPosition {
				return *left.SectionPosition < *right.SectionPosition
			}
			if left.SectionPosition != nil || right.SectionPosition != nil {
				return left.SectionPosition != nil
			}
			if !left.RecencyAt.Equal(right.RecencyAt) {
				return left.RecencyAt.After(right.RecencyAt)
			}
			return left.ID < right.ID
		})
		for i, member := range members {
			position := int64(i+1) * sectionPositionGap
			if member.SectionPosition == nil || *member.SectionPosition != position {
				member.SectionPosition = int64Ptr(position)
				if member.SectionEnteredAt == nil {
					entered := member.RecencyAt.UTC()
					if entered.IsZero() {
						entered = member.UpdatedAt.UTC()
					}
					member.SectionEnteredAt = &entered
				}
				changed = append(changed, member)
			}
		}
	}

	position, renumbered := sectionMovePosition(members, beforeThreadID)
	if renumbered {
		changed = changed[:0]
		sort.SliceStable(members, func(i, j int) bool {
			left, right := *members[i].SectionPosition, *members[j].SectionPosition
			if left != right {
				return left < right
			}
			return members[i].ID < members[j].ID
		})
		for i, member := range members {
			value := int64(i+1) * sectionPositionGap
			member.SectionPosition = int64Ptr(value)
			changed = append(changed, member)
		}
		position, _ = sectionMovePosition(members, beforeThreadID)
	}
	oldSectionID := ""
	if target.Section != nil {
		oldSectionID = target.Section.ID
	}
	target.Section = cloneThreadSection(section)
	target.SectionPosition = int64Ptr(position)
	target.IsPinned = section.ID == PinnedThreadSectionID
	if oldSectionID != section.ID || target.SectionEnteredAt == nil {
		now := time.Now().UTC()
		target.SectionEnteredAt = &now
	}
	changed = append(changed, target)
	for _, record := range changed {
		if err := s.saveLocked(record); err != nil {
			return nil, err
		}
	}
	return cloneRecord(target), nil
}

func sectionMembersNeedRanks(members []*Record) bool {
	for _, member := range members {
		if member.SectionPosition == nil {
			return true
		}
	}
	return false
}

func sectionMovePosition(members []*Record, beforeThreadID *ThreadID) (int64, bool) {
	if beforeThreadID == nil {
		maxPosition := int64(0)
		for _, member := range members {
			if member.SectionPosition != nil && *member.SectionPosition > maxPosition {
				maxPosition = *member.SectionPosition
			}
		}
		return maxPosition + sectionPositionGap, false
	}
	var upper int64
	for _, member := range members {
		if member.ID == *beforeThreadID && member.SectionPosition != nil {
			upper = *member.SectionPosition
			break
		}
	}
	var lower int64
	for _, member := range members {
		if member.SectionPosition != nil && *member.SectionPosition < upper && *member.SectionPosition > lower {
			lower = *member.SectionPosition
		}
	}
	if lower == 0 && upper > 1 {
		return upper / 2, false
	}
	if upper-lower > 1 {
		return lower + (upper-lower)/2, false
	}
	return 0, true
}

func (s *Store) updateMetadataLocked(threadID ThreadID, patch *MetadataPatch, includeArchived bool) (*Record, error) {
	record, err := s.readLocked(threadID, includeArchived, true)
	if err != nil {
		return nil, err
	}
	if patch == nil {
		return record, nil
	}
	if patch.Title != nil {
		record.Title = *patch.Title
	}
	if patch.Preview != nil {
		record.Preview = *patch.Preview
	}
	if patch.Archived != nil {
		record.Archived = *patch.Archived
	}
	if patch.SectionSet {
		section, err := s.threadSectionForIDLocked(patch.SectionID)
		if err != nil {
			return nil, err
		}
		record.Section = section
		record.IsPinned = section != nil && section.ID == PinnedThreadSectionID
	} else if patch.IsPinned != nil {
		// Preserve callers compiled against the former pin API while storing the
		// replacement section contract.
		if *patch.IsPinned {
			record.Section = pinnedThreadSection()
		} else {
			record.Section = nil
		}
		record.IsPinned = *patch.IsPinned
	}
	if patch.CWD != nil {
		record.Metadata.CWD = *patch.CWD
	}
	if patch.Model != nil {
		record.Metadata.Model = *patch.Model
	}
	if patch.ModelProvider != nil {
		record.Metadata.ModelProvider = *patch.ModelProvider
	}
	if patch.Source != nil {
		record.Metadata.Source = *patch.Source
	}
	if patch.ThreadSource != nil {
		record.Metadata.ThreadSource = *patch.ThreadSource
	}
	if patch.Originator != nil {
		record.Metadata.Originator = *patch.Originator
	}
	if patch.HistoryMode != nil {
		record.Metadata.HistoryMode = *patch.HistoryMode
	}
	if patch.MemoryMode != nil {
		record.Metadata.MemoryMode = *patch.MemoryMode
	}
	if patch.Git != nil {
		record.Metadata.Git = cloneStringMap(patch.Git)
	}
	if patch.BaseInstructions != nil {
		record.Metadata.BaseInstructions = *patch.BaseInstructions
	}
	if patch.Instructions != nil {
		record.Metadata.Instructions = *patch.Instructions
	}
	if patch.ApprovalPolicy != nil {
		record.Metadata.ApprovalPolicy = *patch.ApprovalPolicy
	}
	if patch.SandboxPolicy != nil {
		record.Metadata.SandboxPolicy = *patch.SandboxPolicy
	}
	if patch.ServiceTier != nil {
		record.Metadata.ServiceTier = *patch.ServiceTier
	}
	if patch.PromptCacheKey != nil {
		record.Metadata.PromptCacheKey = *patch.PromptCacheKey
	}
	if patch.PreviousResponseID != nil {
		record.Metadata.PreviousResponseID = *patch.PreviousResponseID
	}
	if patch.LastResponseID != nil {
		record.Metadata.LastResponseID = *patch.LastResponseID
	}
	if patch.SessionPrefix != nil {
		record.Metadata.SessionPrefix = *patch.SessionPrefix
	}
	if patch.CLIVersion != nil {
		record.Metadata.CLIVersion = *patch.CLIVersion
	}
	if patch.AgentNickname != nil {
		record.Metadata.AgentNickname = *patch.AgentNickname
	}
	if patch.AgentRole != nil {
		record.Metadata.AgentRole = *patch.AgentRole
	}
	if patch.AgentPath != nil {
		record.Metadata.AgentPath = *patch.AgentPath
	}
	if patch.AgentDepth != nil {
		record.Metadata.AgentDepth = *patch.AgentDepth
	}
	if patch.DynamicTools != nil {
		record.Metadata.DynamicTools = cloneRawMessages(patch.DynamicTools)
	}
	if patch.SelectedCapabilityRoots != nil {
		record.Metadata.SelectedCapabilityRoots = cloneRawMessages(patch.SelectedCapabilityRoots)
	}
	if patch.MultiAgentVersion != nil {
		record.Metadata.MultiAgentVersion = *patch.MultiAgentVersion
	}
	if patch.ContextWindow != nil {
		record.Metadata.ContextWindow = append(json.RawMessage(nil), patch.ContextWindow...)
	}
	if patch.TurnContext != nil {
		record.Metadata.TurnContext = append(json.RawMessage(nil), patch.TurnContext...)
	}
	if patch.WorldState != nil {
		record.Metadata.WorldState = append(json.RawMessage(nil), patch.WorldState...)
	}
	if patch.SelectedModelProvider != nil {
		record.Metadata.SelectedModelProvider = *patch.SelectedModelProvider
	}
	if patch.Extra != nil {
		record.Metadata.Extra = cloneAnyMap(patch.Extra)
	}
	record.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) Archive(threadID ThreadID) error {
	archived := true
	_, err := s.UpdateMetadata(threadID, &MetadataPatch{Archived: &archived}, true)
	return err
}

func (s *Store) Unarchive(threadID ThreadID) (*Record, error) {
	archived := false
	return s.UpdateMetadata(threadID, &MetadataPatch{Archived: &archived}, true)
}

func (s *Store) Delete(threadID ThreadID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadAllLocked()
	if err != nil {
		return err
	}
	for i := range records {
		base := records[i].HistoryBase
		if records[i].ID != threadID && base != nil && base.ThreadID == threadID {
			return fmt.Errorf("%w: cannot delete thread %s: forked history still references it", ErrInvalidThreadID, threadID)
		}
	}
	path, err := s.Path(threadID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrThreadNotFound, threadID)
	} else if err != nil {
		return err
	}
	return nil
}

func (s *Store) SubtreeThreadIDs(root ThreadID) ([]ThreadID, error) {
	if err := validateThreadID(root); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	records, err := s.loadAllLocked()
	if err != nil {
		return nil, err
	}
	foundRoot := false
	relation := &RelationFilter{DescendantsOf: root}
	out := []ThreadID{root}
	for _, record := range records {
		if record.ID == root {
			foundRoot = true
			continue
		}
		if recordMatchesRelation(&record, relation, records) {
			out = append(out, record.ID)
		}
	}
	if !foundRoot {
		return nil, fmt.Errorf("%w: %s", ErrThreadNotFound, root)
	}
	sort.SliceStable(out[1:], func(i, j int) bool {
		return string(out[1+i]) < string(out[1+j])
	})
	return out, nil
}

func DeleteOrderForSubtree(ids []ThreadID) []ThreadID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]ThreadID, 0, len(ids))
	for i := len(ids) - 1; i >= 1; i-- {
		out = append(out, ids[i])
	}
	out = append(out, ids[0])
	return out
}

func ArchiveNotificationOrder(ids []ThreadID) []ThreadID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]ThreadID, 0, len(ids))
	out = append(out, ids[0])
	for i := len(ids) - 1; i >= 1; i-- {
		out = append(out, ids[i])
	}
	return out
}

func (s *Store) List(options ListOptions) (*Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	records, err := s.loadAllLocked()
	if err != nil {
		return nil, err
	}
	return ListRecords(records, options)
}

func (s *Store) AllRecords() ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadAllLocked()
}

func (s *Store) ListSections(cursor string, limit int) ([]ThreadSection, string, error) {
	s.mu.RLock()
	customSections := cloneThreadSections(s.customSections)
	s.mu.RUnlock()
	sections := make([]ThreadSection, 0, 1+len(customSections))
	sections = append(sections, ThreadSection{ID: PinnedThreadSectionID, Name: PinnedThreadSectionName})
	sections = append(sections, customSections...)
	start := 0
	for start < len(sections) && sections[start].ID <= strings.TrimSpace(cursor) {
		start++
	}
	if limit < 1 {
		limit = 1
	}
	end := start + limit
	if end > len(sections) {
		end = len(sections)
	}
	page := append([]ThreadSection(nil), sections[start:end]...)
	next := ""
	if end < len(sections) && len(page) > 0 {
		next = page[len(page)-1].ID
	}
	return page, next, nil
}

// CreateSection adds a custom thread section and persists it (mirrors the Rust
// ThreadSectionCreateParams flow).
func (s *Store) CreateSection(name string, appearance *ThreadSectionAppearance) (*ThreadSection, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("section name is required")
	}
	if err := validateSectionAppearance(appearance); err != nil {
		return nil, err
	}
	section := ThreadSection{
		ID:         uuid.NewString(),
		Name:       name,
		Appearance: cloneThreadSectionAppearance(appearance),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.customSections = append(s.customSections, section)
	if err := s.saveCustomSectionsLocked(); err != nil {
		return nil, err
	}
	return cloneThreadSection(&section), nil
}

// UpdateSection updates a custom section name and appearance. An omitted
// appearance is preserved, null clears it, and a value replaces it (double
// option semantics; the caller tracks whether the field was present).
func (s *Store) UpdateSection(sectionID string, name string, appearance *ThreadSectionAppearance, appearanceSet bool) (*ThreadSection, error) {
	sectionID = strings.TrimSpace(sectionID)
	if sectionID == "" {
		return nil, fmt.Errorf("section id is required")
	}
	if sectionID == PinnedThreadSectionID {
		return nil, fmt.Errorf("the pinned section cannot be updated")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("section name is required")
	}
	if appearanceSet {
		if err := validateSectionAppearance(appearance); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i := range s.customSections {
		if s.customSections[i].ID == sectionID {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, fmt.Errorf("%w: %s", ErrThreadSectionMissing, sectionID)
	}
	updated := s.customSections[index]
	updated.Name = name
	if appearanceSet {
		updated.Appearance = cloneThreadSectionAppearance(appearance)
	}
	s.customSections[index] = updated
	if err := s.saveCustomSectionsLocked(); err != nil {
		return nil, err
	}
	return cloneThreadSection(&updated), nil
}

// DeleteSection removes a custom thread section.
func (s *Store) DeleteSection(sectionID string) error {
	sectionID = strings.TrimSpace(sectionID)
	if sectionID == "" {
		return fmt.Errorf("section id is required")
	}
	if sectionID == PinnedThreadSectionID {
		return fmt.Errorf("the pinned section cannot be deleted")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i := range s.customSections {
		if s.customSections[i].ID == sectionID {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("%w: %s", ErrThreadSectionMissing, sectionID)
	}
	s.customSections = append(s.customSections[:index], s.customSections[index+1:]...)
	return s.saveCustomSectionsLocked()
}

func ListRecords(records []Record, options ListOptions) (*Page, error) {
	filtered := make([]Record, 0, len(records))
	for i := range records {
		record := records[i]
		if !matchesListOptions(&record, &options, records) {
			continue
		}
		if !options.IncludeHistory {
			record.ItemCount = len(record.Items)
			record.Items = nil
		}
		filtered = append(filtered, record)
	}
	sortRecords(filtered, options.SortKey, options.SortDirection)
	start, legacyCursor, err := listStartIndex(filtered, options)
	if err != nil {
		return nil, err
	}
	if start > len(filtered) {
		start = len(filtered)
	}
	pageSize := options.PageSize
	if pageSize <= 0 || pageSize > len(filtered)-start {
		pageSize = len(filtered) - start
	}
	end := start + pageSize
	nextCursor := ""
	if end < len(filtered) {
		if legacyCursor {
			nextCursor = fmt.Sprintf("%d", end)
		} else if end > start {
			nextCursor = listCursor(&filtered[end-1], options.SortKey)
		}
	}
	backwardsCursor := ""
	if end > start {
		backwardsCursor = listBackwardsRecordCursor(&filtered[start], options.SortKey, options.SortDirection)
	}
	return &Page{
		Records:         append([]Record(nil), filtered[start:end]...),
		NextCursor:      nextCursor,
		BackwardsCursor: backwardsCursor,
	}, nil
}

func (s *Store) Fork(sourceID ThreadID, options ForkOptions) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.readLocked(sourceID, true, true)
	if err != nil {
		return nil, err
	}
	return s.forkRecordLocked(source, options)
}

func (s *Store) ForkRecord(source *Record, options ForkOptions) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forkRecordLocked(source, options)
}

func (s *Store) forkRecordLocked(source *Record, options ForkOptions) (*Record, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: source record is nil", ErrInvalidThreadID)
	}
	newID := options.NewID
	if newID == "" {
		generated, err := newThreadID()
		if err != nil {
			return nil, err
		}
		newID = generated
	}
	if err := validateThreadID(newID); err != nil {
		return nil, err
	}
	if newID == source.ID {
		return nil, fmt.Errorf("%w: fork id matches source id", ErrConflict)
	}
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(options.LastTurnID) != "" && strings.TrimSpace(options.BeforeTurnID) != "" {
		return nil, fmt.Errorf("%w: `beforeTurnId` cannot be combined with `lastTurnId`", ErrInvalidThreadID)
	}
	prepared, err := s.PrepareFork(source, prepareForkParamsFromOptions(options))
	if err != nil {
		return nil, err
	}
	if options.HistoryBaseSet {
		prepared.HistoryBase = cloneHistoryPosition(options.HistoryBase)
	}
	items := prepared.Items
	title := options.Title
	if title == "" && source.Title != "" {
		title = source.Title
	}
	sessionID := options.SessionID
	if sessionID == "" {
		sessionID = string(newID)
	}
	metadata := forkMetadata(source, options, now, len(items))
	metadata.RolloutTurns = cloneTurnSnapshots(prepared.RolloutTurns)
	if prepared.HistoryBase != nil {
		metadata.Extra["history_base"] = historyPositionMetadata(prepared.HistoryBase)
	}
	record := &Record{
		ID:             newID,
		SessionID:      sessionID,
		ForkedFromID:   source.ID,
		ParentThreadID: options.ParentThreadID,
		Title:          title,
		Preview:        source.Preview,
		Archived:       false,
		CreatedAt:      now,
		UpdatedAt:      now,
		RecencyAt:      now,
		Metadata:       metadata,
		HistoryBase:    cloneHistoryPosition(prepared.HistoryBase),
		Items:          items,
	}
	if record.HistoryBase != nil {
		record.InheritedItems = len(items)
		if record.HistoryBase.EndOrdinalExclusive != 0 || record.HistoryBase.EndByteOffset != 0 {
			record.InheritedTurns = len(metadata.RolloutTurns)
		} else {
			record.InheritedTurns = record.HistoryBase.TurnEnd
		}
	}
	if options.Ephemeral {
		if record.Metadata.Extra == nil {
			record.Metadata.Extra = map[string]any{}
		}
		record.Metadata.Extra["ephemeral"] = true
		return record, nil
	}
	if err := s.createLocked(record); err != nil {
		return nil, err
	}
	return record, nil
}

func historyPositionMetadata(position *HistoryPosition) map[string]any {
	if position == nil {
		return nil
	}
	if position.EndOrdinalExclusive != 0 || position.EndByteOffset != 0 {
		return map[string]any{
			"thread_id": string(position.ThreadID), "end_ordinal_exclusive": position.EndOrdinalExclusive, "end_byte_offset": position.EndByteOffset,
		}
	}
	return map[string]any{
		"thread_id": string(position.ThreadID), "item_end_exclusive": position.ItemEnd, "turn_end_exclusive": position.TurnEnd,
	}
}

func (s *Store) PrepareFork(source *Record, params PrepareForkParams) (*PreparedFork, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: source record is nil", ErrInvalidThreadID)
	}
	mode := params.Mode
	if mode == "" {
		mode = ForkAll
	}
	boundary := params.Boundary
	if boundary.Kind == "" {
		boundary.Kind = ForkBoundaryLatest
	}
	turnID := strings.TrimSpace(boundary.TurnID)
	lastTurnID := ""
	beforeTurnID := ""
	switch boundary.Kind {
	case ForkBoundaryLatest:
		if turnID != "" {
			return nil, fmt.Errorf("%w: latest fork boundary must not include a turn id", ErrInvalidThreadID)
		}
	case ForkBoundaryThroughTurn:
		if turnID == "" {
			return nil, fmt.Errorf("%w: throughTurn fork boundary requires a turn id", ErrInvalidThreadID)
		}
		lastTurnID = turnID
	case ForkBoundaryBeforeTurn:
		if turnID == "" {
			return nil, fmt.Errorf("%w: beforeTurn fork boundary requires a turn id", ErrInvalidThreadID)
		}
		beforeTurnID = turnID
	default:
		return nil, fmt.Errorf("%w: unknown fork boundary %q", ErrInvalidThreadID, boundary.Kind)
	}
	if err := validateForkLastTurnSnapshot(source.Metadata.RolloutTurns, lastTurnID); err != nil {
		return nil, err
	}
	if err := validateForkBeforeTurnSnapshot(source.Metadata.RolloutTurns, source.Items, beforeTurnID); err != nil {
		return nil, err
	}
	items, err := forkItems(source.Items, mode, params.LastN, lastTurnID, beforeTurnID)
	if err != nil {
		return nil, err
	}
	inheritedSnapshots := forkTurnSnapshots(source.Metadata.RolloutTurns, items)
	snapshots := inheritedSnapshots
	if len(snapshots) == 0 {
		snapshots = syntheticForkTurnSnapshots(items, lastTurnID != "")
	}
	var historyBase *HistoryPosition
	if strings.EqualFold(strings.TrimSpace(source.Metadata.HistoryMode), "paginated") {
		historyBase = &HistoryPosition{
			ThreadID: source.ID,
			ItemEnd:  len(items),
			TurnEnd:  len(inheritedSnapshots),
		}
	}
	return &PreparedFork{
		SourceID:     source.ID,
		HistoryBase:  historyBase,
		Items:        items,
		RolloutTurns: snapshots,
	}, nil
}

func prepareForkParamsFromOptions(options ForkOptions) PrepareForkParams {
	lastTurnID := strings.TrimSpace(options.LastTurnID)
	beforeTurnID := strings.TrimSpace(options.BeforeTurnID)
	boundary := ForkBoundary{Kind: ForkBoundaryLatest}
	if lastTurnID != "" {
		boundary = ForkBoundary{Kind: ForkBoundaryThroughTurn, TurnID: lastTurnID}
	} else if beforeTurnID != "" {
		boundary = ForkBoundary{Kind: ForkBoundaryBeforeTurn, TurnID: beforeTurnID}
	}
	return PrepareForkParams{Mode: options.Mode, LastN: options.LastN, Boundary: boundary}
}

func cloneTurnSnapshots(values []TurnSnapshot) []TurnSnapshot {
	if len(values) == 0 {
		return nil
	}
	out := make([]TurnSnapshot, len(values))
	for i := range values {
		out[i] = cloneTurnSnapshot(values[i])
	}
	return out
}

func (s *Store) loadAllLocked() ([]Record, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		if record.ID == "" {
			record.ID = ThreadID(strings.TrimSuffix(entry.Name(), ".json"))
		}
		if err := validateThreadID(record.ID); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		normalizeRecordSection(&record)
		records = append(records, record)
	}
	return records, nil
}

func writeStoreFileAtomically(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".thread-store-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	return os.Rename(temporaryPath, path)
}

func matchesListOptions(record *Record, options *ListOptions, all []Record) bool {
	if record.Archived != options.Archived {
		return false
	}
	if options.SectionSet {
		if options.SectionID == nil {
			if record.Section != nil {
				return false
			}
		} else if record.Section == nil || record.Section.ID != strings.TrimSpace(*options.SectionID) {
			return false
		}
	} else if options.IsPinned != nil {
		pinned := record.Section != nil && record.Section.ID == PinnedThreadSectionID
		if pinned != *options.IsPinned {
			return false
		}
	}
	if len(options.ModelProviders) > 0 && !containsString(options.ModelProviders, record.Metadata.ModelProvider) {
		return false
	}
	if len(options.CWDs) > 0 && !recordMatchesCWDs(record.Metadata.CWD, options.CWDs) {
		return false
	}
	if len(options.Sources) > 0 && !recordMatchesAllowedSources(record.Metadata.Source, options.Sources) {
		return false
	}
	if len(options.SourceKinds) > 0 && !recordMatchesSourceKinds(record, options.SourceKinds) {
		return false
	}
	if options.Search != "" && !recordMatchesSearch(record, options.Search) {
		return false
	}
	if options.Relation != nil && !recordMatchesRelation(record, options.Relation, all) {
		return false
	}
	return true
}

func recordMatchesCWDs(cwd string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if normalizedCWDEqual(cwd, filter) {
			return true
		}
	}
	return false
}

func normalizedCWDEqual(left string, right string) bool {
	left = normalizedCWD(left)
	right = normalizedCWD(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func normalizedCWD(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.Clean(value)
	if absolute, err := filepath.Abs(value); err == nil {
		value = absolute
	}
	return filepath.Clean(value)
}

func recordMatchesAllowedSources(source string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	normalized := normalizedSourceKind(source)
	for _, candidate := range allowed {
		if normalized == normalizedSourceKind(candidate) {
			return true
		}
	}
	return false
}

func recordMatchesSourceKinds(record *Record, kinds []string) bool {
	if record == nil || len(kinds) == 0 {
		return true
	}
	for _, kind := range kinds {
		if recordMatchesSourceKind(record, kind) {
			return true
		}
	}
	return false
}

func recordMatchesSourceKind(record *Record, kind string) bool {
	source := normalizedSourceKind(record.Metadata.Source)
	switch strings.TrimSpace(kind) {
	case "cli":
		return source == "cli"
	case "vscode":
		return source == "vscode"
	case "exec":
		return source == "exec"
	case "appServer":
		return source == "appserver"
	case "unknown":
		return source == "unknown"
	case "subAgent":
		return strings.HasPrefix(source, "subagent")
	case "subAgentReview":
		return source == "subagentreview"
	case "subAgentCompact":
		return source == "subagentcompact"
	case "subAgentThreadSpawn":
		return source == "subagentthreadspawn" || (strings.HasPrefix(source, "subagent") && strings.TrimSpace(record.Metadata.AgentPath) != "")
	case "subAgentOther":
		return source == "subagentother"
	default:
		return false
	}
}

func normalizedSourceKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("_", "", "-", "", "/", "", ":", "", " ", "").Replace(value)
	switch value {
	case "":
		return "unknown"
	case "app", "appserver", "mcp":
		return "appserver"
	case "subagentthreadspawn", "subagentspawn", "threadspawn":
		return "subagentthreadspawn"
	case "subagentreview", "review":
		return "subagentreview"
	case "subagentcompact", "compact":
		return "subagentcompact"
	case "subagentother", "other":
		return "subagentother"
	default:
		return value
	}
}

func recordMatchesSearch(record *Record, search string) bool {
	needle := strings.ToLower(search)
	if strings.Contains(strings.ToLower(record.Title), needle) {
		return true
	}
	if strings.Contains(strings.ToLower(record.Preview), needle) {
		return true
	}
	for i := range record.Items {
		if strings.Contains(strings.ToLower(itemPreviewText(&record.Items[i])), needle) {
			return true
		}
	}
	return false
}

func recordMatchesRelation(record *Record, relation *RelationFilter, all []Record) bool {
	if relation.DirectChildrenOf != "" {
		return record.ParentThreadID == relation.DirectChildrenOf
	}
	if relation.DescendantsOf == "" {
		return true
	}
	parentByID := make(map[ThreadID]ThreadID, len(all))
	for i := range all {
		parentByID[all[i].ID] = all[i].ParentThreadID
	}
	for parent := record.ParentThreadID; parent != ""; parent = parentByID[parent] {
		if parent == relation.DescendantsOf {
			return true
		}
	}
	return false
}

func sortRecords(records []Record, key SortKey, direction SortDirection) {
	if key == "" {
		key = SortCreatedAt
	}
	if direction == "" {
		if key == SortSectionPosition {
			direction = SortAsc
		} else {
			direction = SortDesc
		}
	}
	sort.SliceStable(records, func(i int, j int) bool {
		if key == SortSectionPosition {
			left, right := records[i].SectionPosition, records[j].SectionPosition
			if left == nil || right == nil {
				if left == nil && right == nil {
					if direction == SortAsc {
						return records[i].ID < records[j].ID
					}
					return records[i].ID > records[j].ID
				}
				return right == nil
			}
			if *left != *right {
				if direction == SortAsc {
					return *left < *right
				}
				return *left > *right
			}
			if direction == SortAsc {
				return records[i].ID < records[j].ID
			}
			return records[i].ID > records[j].ID
		}
		left := sortTime(&records[i], key)
		right := sortTime(&records[j], key)
		if left.Equal(right) {
			if direction == SortAsc {
				return records[i].ID < records[j].ID
			}
			return records[i].ID > records[j].ID
		}
		if direction == SortAsc {
			return left.Before(right)
		}
		return left.After(right)
	})
}

func sortTime(record *Record, key SortKey) time.Time {
	switch key {
	case SortUpdatedAt:
		return record.UpdatedAt
	case SortRecencyAt:
		return record.RecencyAt
	default:
		return record.CreatedAt
	}
}

func forkItems(items []Item, mode ForkMode, lastN int, lastTurnID string, beforeTurnID string) ([]Item, error) {
	items, err := itemsThroughTurn(items, lastTurnID)
	if err != nil {
		return nil, err
	}
	items, err = itemsBeforeTurn(items, beforeTurnID)
	if err != nil {
		return nil, err
	}
	switch mode {
	case "", ForkAll:
		return cloneItems(items), nil
	case ForkNone:
		return nil, nil
	case ForkLastN:
		if lastN < 0 {
			return nil, fmt.Errorf("%w: last_n must be non-negative", ErrInvalidThreadID)
		}
		if lastN > len(items) {
			lastN = len(items)
		}
		return cloneItems(items[len(items)-lastN:]), nil
	default:
		return nil, fmt.Errorf("%w: unknown fork mode %q", ErrInvalidThreadID, mode)
	}
}

func itemsBeforeTurn(items []Item, beforeTurnID string) ([]Item, error) {
	beforeTurnID = strings.TrimSpace(beforeTurnID)
	if beforeTurnID == "" {
		return items, nil
	}
	firstIndex := -1
	canonical := false
	for i := range items {
		turnID, explicit := itemTurnIDWithExplicit(&items[i], i)
		if turnID == beforeTurnID {
			if firstIndex < 0 {
				firstIndex = i
			}
			canonical = canonical || explicit
		}
	}
	if firstIndex < 0 {
		return nil, fmt.Errorf("%w: beforeTurnId '%s' was not found in the source thread", ErrInvalidThreadID, beforeTurnID)
	}
	if !canonical {
		return nil, fmt.Errorf("%w: beforeTurnId '%s' is not a persisted canonical turn in the source thread", ErrInvalidThreadID, beforeTurnID)
	}
	return items[:firstIndex], nil
}

func itemsThroughTurn(items []Item, lastTurnID string) ([]Item, error) {
	lastTurnID = strings.TrimSpace(lastTurnID)
	if lastTurnID == "" {
		return items, nil
	}
	lastIndex := -1
	canonical := false
	for i := range items {
		turnID, explicit := itemTurnIDWithExplicit(&items[i], i)
		if turnID == lastTurnID {
			lastIndex = i
			canonical = canonical || explicit
		}
	}
	if lastIndex < 0 {
		return nil, fmt.Errorf("%w: lastTurnId '%s' was not found in the source thread", ErrInvalidThreadID, lastTurnID)
	}
	if !canonical {
		return nil, fmt.Errorf("%w: lastTurnId '%s' is not a persisted canonical turn in the source thread", ErrInvalidThreadID, lastTurnID)
	}
	return items[:lastIndex+1], nil
}

func validateForkLastTurnSnapshot(snapshots []TurnSnapshot, lastTurnID string) error {
	lastTurnID = strings.TrimSpace(lastTurnID)
	if lastTurnID == "" {
		return nil
	}
	for i := len(snapshots) - 1; i >= 0; i-- {
		snapshot := snapshots[i]
		if strings.TrimSpace(snapshot.ID) != lastTurnID {
			continue
		}
		status := strings.TrimSpace(snapshot.Status)
		if strings.EqualFold(status, "inProgress") || strings.EqualFold(status, "in_progress") || strings.EqualFold(status, "in-progress") {
			return fmt.Errorf("%w: lastTurnId '%s' identifies an in-progress turn", ErrInvalidThreadID, lastTurnID)
		}
		return nil
	}
	return nil
}

func validateForkBeforeTurnSnapshot(snapshots []TurnSnapshot, items []Item, beforeTurnID string) error {
	beforeTurnID = strings.TrimSpace(beforeTurnID)
	if beforeTurnID == "" {
		return nil
	}
	for i := range items {
		if turnID, explicit := itemTurnIDWithExplicit(&items[i], i); explicit && turnID == beforeTurnID {
			return nil
		}
	}
	for i := len(snapshots) - 1; i >= 0; i-- {
		if strings.TrimSpace(snapshots[i].ID) == beforeTurnID {
			return fmt.Errorf("%w: turn %s does not have a persisted start boundary", ErrInvalidThreadID, beforeTurnID)
		}
	}
	return nil
}

func itemTurnID(item *Item, index int) string {
	turnID, explicit := itemTurnIDWithExplicit(item, index)
	if explicit {
		return turnID
	}
	return turnID
}

func itemTurnIDWithExplicit(item *Item, index int) (string, bool) {
	if item != nil {
		if value, ok := item.Metadata["turnId"].(string); ok && strings.TrimSpace(value) != "" {
			return value, true
		}
		if value, ok := item.Metadata["turn_id"].(string); ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return fmt.Sprintf("turn-%d", index+1), false
}

func listStartIndex(records []Record, options ListOptions) (int, bool, error) {
	cursor := strings.TrimSpace(options.Cursor)
	if cursor == "" {
		return 0, false, nil
	}
	if options.SortKey == SortSectionPosition {
		position, threadID, err := parseSectionPositionCursor(cursor)
		if err != nil {
			return 0, false, err
		}
		direction := options.SortDirection
		if direction == "" {
			direction = SortAsc
		}
		for i := range records {
			if records[i].SectionPosition == nil {
				continue
			}
			value := *records[i].SectionPosition
			if direction == SortAsc {
				if value > position || (value == position && records[i].ID > threadID) {
					return i, false, nil
				}
			} else if value < position || (value == position && records[i].ID < threadID) {
				return i, false, nil
			}
		}
		return len(records), false, nil
	}
	if start, ok := parseLegacyOffsetCursor(cursor); ok {
		return start, true, nil
	}
	anchor, err := time.Parse(time.RFC3339Nano, cursor)
	if err != nil {
		return 0, false, fmt.Errorf("%w: invalid cursor %q", ErrInvalidThreadID, cursor)
	}
	direction := options.SortDirection
	if direction == "" {
		if options.SortKey == SortSectionPosition {
			direction = SortAsc
		} else {
			direction = SortDesc
		}
	}
	for i := range records {
		value := sortTime(&records[i], options.SortKey)
		if direction == SortAsc {
			if value.After(anchor) {
				return i, false, nil
			}
			continue
		}
		if value.Before(anchor) {
			return i, false, nil
		}
	}
	return len(records), false, nil
}

func parseLegacyOffsetCursor(cursor string) (int, bool) {
	if cursor == "" {
		return 0, false
	}
	start := 0
	for _, ch := range cursor {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		start = start*10 + int(ch-'0')
	}
	return start, true
}

func listTimeCursor(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func listCursor(record *Record, key SortKey) string {
	if key == SortSectionPosition {
		if record == nil || record.SectionPosition == nil {
			return ""
		}
		return fmt.Sprintf("%d|%s", *record.SectionPosition, record.ID)
	}
	return listTimeCursor(sortTime(record, key))
}

func parseSectionPositionCursor(cursor string) (int64, ThreadID, error) {
	positionText, threadText, ok := strings.Cut(cursor, "|")
	if !ok || strings.TrimSpace(threadText) == "" {
		return 0, "", fmt.Errorf("%w: invalid cursor %q", ErrInvalidThreadID, cursor)
	}
	position, err := strconv.ParseInt(positionText, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("%w: invalid cursor %q", ErrInvalidThreadID, cursor)
	}
	threadID := ThreadID(threadText)
	if err := validateThreadID(threadID); err != nil {
		return 0, "", fmt.Errorf("%w: invalid cursor %q", ErrInvalidThreadID, cursor)
	}
	return position, threadID, nil
}

func listBackwardsRecordCursor(record *Record, key SortKey, direction SortDirection) string {
	if key == SortSectionPosition {
		if record == nil || record.SectionPosition == nil {
			return ""
		}
		position := *record.SectionPosition
		if direction == SortDesc {
			position--
		} else {
			position++
		}
		return fmt.Sprintf("%d|%s", position, record.ID)
	}
	return listBackwardsCursor(sortTime(record, key), direction)
}

func listBackwardsCursor(value time.Time, direction SortDirection) string {
	if value.IsZero() {
		return ""
	}
	if direction == SortAsc {
		return listTimeCursor(value.Add(time.Millisecond))
	}
	return listTimeCursor(value.Add(-time.Millisecond))
}

func validateThreadID(threadID ThreadID) error {
	value := string(threadID)
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: empty", ErrInvalidThreadID)
	}
	if value == "." || value == ".." || strings.Contains(value, "..") {
		return fmt.Errorf("%w: %s", ErrInvalidThreadID, value)
	}
	if strings.ContainsAny(value, `/\:`) {
		return fmt.Errorf("%w: %s", ErrInvalidThreadID, value)
	}
	return nil
}

func newThreadID() (ThreadID, error) {
	// Match Rust ThreadId::new(): fresh threads receive a UUIDv7. The previous
	// random 32-hex encoding made fork-created threads distinguishable from
	// CLI/app-server threads and is not a valid UUIDv7.
	if id, err := uuid.NewV7(); err == nil {
		return ThreadID(id.String()), nil
	}
	return ThreadID(uuid.NewString()), nil
}

func cloneItems(items []Item) []Item {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]Item, len(items))
	for i := range items {
		cloned[i] = items[i]
		cloned[i].Content = cloneContentParts(items[i].Content)
		cloned[i].Data = cloneAnyMap(items[i].Data)
		if items[i].Raw != nil {
			cloned[i].Raw = append(json.RawMessage(nil), items[i].Raw...)
		}
		if items[i].Metadata != nil {
			cloned[i].Metadata = cloneAnyMap(items[i].Metadata)
		}
	}
	return cloned
}

func cloneHistoryPosition(position *HistoryPosition) *HistoryPosition {
	if position == nil {
		return nil
	}
	clone := *position
	return &clone
}

func cloneRecord(record *Record) *Record {
	if record == nil {
		return nil
	}
	clone := *record
	clone.Section = cloneThreadSection(record.Section)
	clone.SectionPosition = cloneInt64(record.SectionPosition)
	clone.SectionEnteredAt = cloneTime(record.SectionEnteredAt)
	clone.Metadata = cloneMetadata(record.Metadata)
	clone.HistoryBase = cloneHistoryPosition(record.HistoryBase)
	clone.Items = cloneItems(record.Items)
	return &clone
}

func pinnedThreadSection() *ThreadSection {
	return &ThreadSection{ID: PinnedThreadSectionID, Name: PinnedThreadSectionName}
}

func cloneThreadSection(section *ThreadSection) *ThreadSection {
	if section == nil {
		return nil
	}
	cloned := *section
	cloned.Appearance = cloneThreadSectionAppearance(section.Appearance)
	return &cloned
}

func cloneThreadSections(sections []ThreadSection) []ThreadSection {
	if sections == nil {
		return nil
	}
	out := make([]ThreadSection, len(sections))
	for i := range sections {
		out[i] = *cloneThreadSection(&sections[i])
	}
	return out
}

func cloneThreadSectionAppearance(appearance *ThreadSectionAppearance) *ThreadSectionAppearance {
	if appearance == nil {
		return nil
	}
	cloned := *appearance
	if appearance.Icon != nil {
		value := *appearance.Icon
		cloned.Icon = &value
	}
	if appearance.Color != nil {
		value := *appearance.Color
		cloned.Color = &value
	}
	return &cloned
}

func validateSectionAppearance(appearance *ThreadSectionAppearance) error {
	if appearance == nil {
		return nil
	}
	if appearance.Icon != nil && len(*appearance.Icon) > 64 {
		return fmt.Errorf("section appearance icon must not exceed 64 bytes")
	}
	if appearance.Color != nil && len(*appearance.Color) > 64 {
		return fmt.Errorf("section appearance color must not exceed 64 bytes")
	}
	return nil
}

func sectionsFilePath(root string) string {
	return filepath.Join(root, "sections.json")
}

func (s *Store) loadCustomSectionsLocked() {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return
	}
	data, err := os.ReadFile(sectionsFilePath(s.root))
	if err != nil {
		return
	}
	var sections []ThreadSection
	if err := json.Unmarshal(data, &sections); err != nil {
		return
	}
	s.customSections = cloneThreadSections(sections)
}

func (s *Store) saveCustomSectionsLocked() error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil
	}
	data, err := json.MarshalIndent(cloneThreadSections(s.customSections), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	return os.WriteFile(sectionsFilePath(s.root), append(data, '\n'), 0o600)
}

func (s *Store) threadSectionForID(id *string) (*ThreadSection, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.threadSectionForIDLocked(id)
}

// threadSectionForIDLocked assumes the caller already holds s.mu. It must not
// re-acquire the lock: MoveThreadToSection and updateMetadataLocked call it
// while holding the write lock, and sync.RWMutex is not reentrant.
func (s *Store) threadSectionForIDLocked(id *string) (*ThreadSection, error) {
	if id == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*id)
	if value == PinnedThreadSectionID {
		return pinnedThreadSection(), nil
	}
	if s != nil {
		for i := range s.customSections {
			if s.customSections[i].ID == value {
				cloned := s.customSections[i]
				return &cloned, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrThreadSectionMissing, value)
}

func normalizeRecordSection(record *Record) {
	if record == nil {
		return
	}
	if record.Section == nil && record.IsPinned {
		record.Section = pinnedThreadSection()
	}
	if record.Section == nil {
		record.SectionPosition = nil
		record.SectionEnteredAt = nil
	}
	record.IsPinned = record.Section != nil && record.Section.ID == PinnedThreadSectionID
}

func int64Ptr(value int64) *int64 { return &value }

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// LocalRecord returns the physical, non-inherited portion of a record. It is
// used when creating a paginated child rollout so inherited events remain a
// reference instead of being replayed into the child file.
func LocalRecord(record *Record) *Record {
	clone := cloneRecord(record)
	if clone == nil || clone.HistoryBase == nil {
		return clone
	}
	if clone.InheritedItems >= 0 && clone.InheritedItems <= len(clone.Items) {
		clone.Items = cloneItems(clone.Items[clone.InheritedItems:])
	}
	if clone.InheritedTurns >= 0 && clone.InheritedTurns <= len(clone.Metadata.RolloutTurns) {
		clone.Metadata.RolloutTurns = cloneTurnSnapshots(clone.Metadata.RolloutTurns[clone.InheritedTurns:])
	}
	return clone
}

func cloneMetadata(metadata Metadata) Metadata {
	metadata.Git = cloneStringMap(metadata.Git)
	metadata.BaseInstructionsProvenance = cloneBaseInstructionsProvenance(metadata.BaseInstructionsProvenance)
	metadata.Extra = cloneAnyMap(metadata.Extra)
	metadata.DynamicTools = cloneRawMessages(metadata.DynamicTools)
	metadata.SelectedCapabilityRoots = cloneRawMessages(metadata.SelectedCapabilityRoots)
	metadata.ContextWindow = append(json.RawMessage(nil), metadata.ContextWindow...)
	metadata.TurnContext = append(json.RawMessage(nil), metadata.TurnContext...)
	metadata.WorldState = append(json.RawMessage(nil), metadata.WorldState...)
	return metadata
}

func cloneBaseInstructionsProvenance(value *BaseInstructionsProvenance) *BaseInstructionsProvenance {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func forkMetadata(source *Record, options ForkOptions, now time.Time, itemCount int) Metadata {
	if source == nil {
		return Metadata{}
	}
	metadata := cloneMetadata(source.Metadata)
	if metadata.Extra == nil {
		metadata.Extra = map[string]any{}
	}
	mode := string(options.Mode)
	if mode == "" {
		mode = string(ForkAll)
	}
	metadata.Extra["forked_from_id"] = string(source.ID)
	metadata.Extra["fork_mode"] = mode
	metadata.Extra["fork_item_count"] = itemCount
	metadata.Extra["fork_snapshot_at"] = now.UTC().Format(time.RFC3339Nano)
	if options.LastN > 0 {
		metadata.Extra["fork_last_n"] = options.LastN
	}
	return metadata
}

func forkTurnSnapshots(snapshots []TurnSnapshot, items []Item) []TurnSnapshot {
	if len(snapshots) == 0 || len(items) == 0 {
		return nil
	}
	keptTurnIDs := map[string]bool{}
	for i := range items {
		keptTurnIDs[itemTurnID(&items[i], i)] = true
	}
	out := make([]TurnSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		turnID := strings.TrimSpace(snapshot.ID)
		if turnID == "" || !keptTurnIDs[turnID] {
			continue
		}
		out = append(out, cloneTurnSnapshot(snapshot))
	}
	return out
}

func syntheticForkTurnSnapshots(items []Item, terminalPrefix bool) []TurnSnapshot {
	if len(items) == 0 {
		return nil
	}
	turnIDs := []string{}
	seen := map[string]bool{}
	for i := range items {
		turnID := itemTurnID(&items[i], i)
		if seen[turnID] {
			continue
		}
		seen[turnID] = true
		turnIDs = append(turnIDs, turnID)
	}
	if len(turnIDs) == 0 {
		return nil
	}
	snapshots := make([]TurnSnapshot, 0, len(turnIDs))
	for i, turnID := range turnIDs {
		status := "completed"
		if !terminalPrefix && i == len(turnIDs)-1 {
			status = "interrupted"
		}
		snapshots = append(snapshots, TurnSnapshot{ID: turnID, Status: status})
	}
	return snapshots
}

func cloneTurnSnapshot(snapshot TurnSnapshot) TurnSnapshot {
	return TurnSnapshot{
		ID:           snapshot.ID,
		Status:       snapshot.Status,
		StartedAt:    cloneInt64Ptr(snapshot.StartedAt),
		CompletedAt:  cloneInt64Ptr(snapshot.CompletedAt),
		DurationMS:   cloneInt64Ptr(snapshot.DurationMS),
		ErrorMessage: snapshot.ErrorMessage,
	}
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneContentParts(parts []ContentPart) []ContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]ContentPart, len(parts))
	for i := range parts {
		cloned[i] = parts[i]
		if parts[i].Detail != nil {
			value := *parts[i].Detail
			cloned[i].Detail = &value
		}
	}
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneAnySlice(values []any) []any {
	if values == nil {
		return nil
	}
	return append([]any(nil), values...)
}

func cloneQueuedSubmission(submission QueuedSubmission) QueuedSubmission {
	submission.Input = cloneAnySlice(submission.Input)
	return submission
}

func cloneQueuedSubmissionPtr(submission *QueuedSubmission) *QueuedSubmission {
	if submission == nil {
		return nil
	}
	clone := cloneQueuedSubmission(*submission)
	return &clone
}

func cloneQueuedSubmissions(submissions []QueuedSubmission) []QueuedSubmission {
	if submissions == nil {
		return nil
	}
	cloned := make([]QueuedSubmission, len(submissions))
	for i := range submissions {
		cloned[i] = cloneQueuedSubmission(submissions[i])
	}
	return cloned
}

func itemPreviewText(item *Item) string {
	if item == nil {
		return ""
	}
	if strings.TrimSpace(item.Text) != "" {
		return item.Text
	}
	for i := range item.Content {
		if strings.TrimSpace(item.Content[i].Text) != "" {
			return item.Content[i].Text
		}
	}
	for _, key := range []string{"text", "output", "input", "arguments"} {
		if text, ok := item.Data[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		cloned = append(cloned, append(json.RawMessage(nil), value...))
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
