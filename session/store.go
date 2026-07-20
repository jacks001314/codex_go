package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	ForkAll   ForkMode = "all"
	ForkNone  ForkMode = "none"
	ForkLastN ForkMode = "last-n"

	SortCreatedAt SortKey = "created_at"
	SortUpdatedAt SortKey = "updated_at"
	SortRecencyAt SortKey = "recency_at"

	SortAsc  SortDirection = "asc"
	SortDesc SortDirection = "desc"
)

var (
	ErrInvalidThreadID = errors.New("invalid thread id")
	ErrThreadNotFound  = errors.New("thread not found")
	ErrThreadArchived  = errors.New("thread archived")
	ErrConflict        = errors.New("thread store conflict")
)

type ForkMode string

type SortKey string

type SortDirection string

type ThreadID string

type Item struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Role       string          `json:"role,omitempty"`
	Text       string          `json:"text,omitempty"`
	Name       string          `json:"name,omitempty"`
	Namespace  string          `json:"namespace,omitempty"`
	CallID     string          `json:"call_id,omitempty"`
	Status     string          `json:"status,omitempty"`
	Content    []ContentPart   `json:"content,omitempty"`
	Data       map[string]any  `json:"data,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	ResponseID string          `json:"response_id,omitempty"`
	Metadata   map[string]any  `json:"metadata,omitempty"`
}

type ContentPart struct {
	Type     string  `json:"type"`
	Text     string  `json:"text,omitempty"`
	ImageURL string  `json:"image_url,omitempty"`
	AudioURL string  `json:"audio_url,omitempty"`
	Detail   *string `json:"detail,omitempty"`
}

type Metadata struct {
	CWD                     string            `json:"cwd,omitempty"`
	Model                   string            `json:"model,omitempty"`
	ModelProvider           string            `json:"model_provider,omitempty"`
	Source                  string            `json:"source,omitempty"`
	ThreadSource            string            `json:"thread_source,omitempty"`
	Originator              string            `json:"originator,omitempty"`
	HistoryMode             string            `json:"history_mode,omitempty"`
	MemoryMode              string            `json:"memory_mode,omitempty"`
	Git                     map[string]string `json:"git,omitempty"`
	BaseInstructions        string            `json:"base_instructions,omitempty"`
	Instructions            string            `json:"instructions,omitempty"`
	ApprovalPolicy          string            `json:"approval_policy,omitempty"`
	SandboxPolicy           string            `json:"sandbox_policy,omitempty"`
	ServiceTier             string            `json:"service_tier,omitempty"`
	PromptCacheKey          string            `json:"prompt_cache_key,omitempty"`
	PreviousResponseID      string            `json:"previous_response_id,omitempty"`
	LastResponseID          string            `json:"last_response_id,omitempty"`
	SessionPrefix           string            `json:"session_prefix,omitempty"`
	CLIVersion              string            `json:"cli_version,omitempty"`
	AgentNickname           string            `json:"agent_nickname,omitempty"`
	AgentRole               string            `json:"agent_role,omitempty"`
	AgentPath               string            `json:"agent_path,omitempty"`
	DynamicTools            []json.RawMessage `json:"dynamic_tools,omitempty"`
	SelectedCapabilityRoots []json.RawMessage `json:"selected_capability_roots,omitempty"`
	MultiAgentVersion       string            `json:"multi_agent_version,omitempty"`
	ContextWindow           json.RawMessage   `json:"context_window,omitempty"`
	TurnContext             json.RawMessage   `json:"turn_context,omitempty"`
	WorldState              json.RawMessage   `json:"world_state,omitempty"`
	SelectedModelProvider   string            `json:"selected_model_provider,omitempty"`
	ElicitationCount        int               `json:"elicitation_count,omitempty"`
	Extra                   map[string]any    `json:"extra,omitempty"`
	RolloutTurns            []TurnSnapshot    `json:"rollout_turns,omitempty"`
}

type TurnSnapshot struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	StartedAt    *int64 `json:"started_at,omitempty"`
	CompletedAt  *int64 `json:"completed_at,omitempty"`
	DurationMS   *int64 `json:"duration_ms,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type Record struct {
	ID             ThreadID  `json:"id"`
	SessionID      string    `json:"session_id,omitempty"`
	ForkedFromID   ThreadID  `json:"forked_from_id,omitempty"`
	ParentThreadID ThreadID  `json:"parent_thread_id,omitempty"`
	Title          string    `json:"title,omitempty"`
	Preview        string    `json:"preview,omitempty"`
	Archived       bool      `json:"archived"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	RecencyAt      time.Time `json:"recency_at"`
	Metadata       Metadata  `json:"metadata"`
	Items          []Item    `json:"items,omitempty"`
	FromRollout    bool      `json:"-"`
}

type MetadataPatch struct {
	Title                   *string
	Preview                 *string
	Archived                *bool
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
	Now            time.Time
}

type Store struct {
	root string
}

func NewStore(root string) *Store {
	return &Store{root: root}
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) Path(threadID ThreadID) (string, error) {
	if err := validateThreadID(threadID); err != nil {
		return "", err
	}
	return filepath.Join(s.root, string(threadID)+".json"), nil
}

func (s *Store) Save(record *Record) error {
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
	record.Metadata.Git = cloneStringMap(record.Metadata.Git)
	record.Metadata.Extra = cloneAnyMap(record.Metadata.Extra)
	record.Items = cloneItems(record.Items)
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	path, err := s.Path(record.ID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func (s *Store) Create(record *Record) error {
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
	return s.Save(record)
}

func (s *Store) Load(threadID ThreadID) (*Record, error) {
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
	return &record, nil
}

func (s *Store) Read(threadID ThreadID, includeArchived bool, includeHistory bool) (*Record, error) {
	record, err := s.Load(threadID)
	if err != nil {
		return nil, err
	}
	if record.Archived && !includeArchived {
		return nil, fmt.Errorf("%w: %s", ErrThreadArchived, threadID)
	}
	if !includeHistory {
		record.Items = nil
	}
	return record, nil
}

func (s *Store) AppendItem(threadID ThreadID, item Item) (*Record, error) {
	record, err := s.Read(threadID, true, true)
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
	if err := s.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) AppendItems(threadID ThreadID, items []Item) (*Record, error) {
	record, err := s.Read(threadID, true, true)
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
	if err := s.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) UpdateMetadata(threadID ThreadID, patch *MetadataPatch, includeArchived bool) (*Record, error) {
	record, err := s.Read(threadID, includeArchived, true)
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
	if err := s.Save(record); err != nil {
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
	records, err := s.loadAll()
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
	records, err := s.loadAll()
	if err != nil {
		return nil, err
	}
	return ListRecords(records, options)
}

func (s *Store) AllRecords() ([]Record, error) {
	return s.loadAll()
}

func ListRecords(records []Record, options ListOptions) (*Page, error) {
	filtered := make([]Record, 0, len(records))
	for i := range records {
		record := records[i]
		if !matchesListOptions(&record, &options, records) {
			continue
		}
		if !options.IncludeHistory {
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
			nextCursor = listTimeCursor(sortTime(&filtered[end-1], options.SortKey))
		}
	}
	backwardsCursor := ""
	if end > start {
		backwardsCursor = listBackwardsCursor(sortTime(&filtered[start], options.SortKey), options.SortDirection)
	}
	return &Page{
		Records:         append([]Record(nil), filtered[start:end]...),
		NextCursor:      nextCursor,
		BackwardsCursor: backwardsCursor,
	}, nil
}

func (s *Store) Fork(sourceID ThreadID, options ForkOptions) (*Record, error) {
	source, err := s.Read(sourceID, true, true)
	if err != nil {
		return nil, err
	}
	return s.ForkRecord(source, options)
}

func (s *Store) ForkRecord(source *Record, options ForkOptions) (*Record, error) {
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
	if err := validateForkLastTurnSnapshot(source.Metadata.RolloutTurns, options.LastTurnID); err != nil {
		return nil, err
	}
	items, err := forkItems(source.Items, options.Mode, options.LastN, options.LastTurnID, options.BeforeTurnID)
	if err != nil {
		return nil, err
	}
	title := options.Title
	if title == "" && source.Title != "" {
		title = source.Title
	}
	sessionID := options.SessionID
	if sessionID == "" {
		sessionID = string(newID)
	}
	parentID := options.ParentThreadID
	if parentID == "" {
		parentID = source.ID
	}
	metadata := forkMetadata(source, options, now, len(items))
	metadata.RolloutTurns = forkTurnSnapshots(source.Metadata.RolloutTurns, items)
	if len(metadata.RolloutTurns) == 0 {
		metadata.RolloutTurns = syntheticForkTurnSnapshots(items, strings.TrimSpace(options.LastTurnID) != "")
	}
	record := &Record{
		ID:             newID,
		SessionID:      sessionID,
		ForkedFromID:   source.ID,
		ParentThreadID: parentID,
		Title:          title,
		Preview:        source.Preview,
		Archived:       false,
		CreatedAt:      now,
		UpdatedAt:      now,
		RecencyAt:      now,
		Metadata:       metadata,
		Items:          items,
	}
	if options.Ephemeral {
		if record.Metadata.Extra == nil {
			record.Metadata.Extra = map[string]any{}
		}
		record.Metadata.Extra["ephemeral"] = true
		return record, nil
	}
	if err := s.Create(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) loadAll() ([]Record, error) {
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
		records = append(records, record)
	}
	return records, nil
}

func matchesListOptions(record *Record, options *ListOptions, all []Record) bool {
	if record.Archived != options.Archived {
		return false
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
		direction = SortDesc
	}
	sort.SliceStable(records, func(i int, j int) bool {
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
	for _, snapshot := range snapshots {
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
	if start, ok := parseLegacyOffsetCursor(cursor); ok {
		return start, true, nil
	}
	anchor, err := time.Parse(time.RFC3339Nano, cursor)
	if err != nil {
		return 0, false, fmt.Errorf("%w: invalid cursor %q", ErrInvalidThreadID, cursor)
	}
	direction := options.SortDirection
	if direction == "" {
		direction = SortDesc
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
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return ThreadID(hex.EncodeToString(bytes[:])), nil
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

func cloneMetadata(metadata Metadata) Metadata {
	metadata.Git = cloneStringMap(metadata.Git)
	metadata.Extra = cloneAnyMap(metadata.Extra)
	metadata.DynamicTools = cloneRawMessages(metadata.DynamicTools)
	metadata.SelectedCapabilityRoots = cloneRawMessages(metadata.SelectedCapabilityRoots)
	metadata.ContextWindow = append(json.RawMessage(nil), metadata.ContextWindow...)
	metadata.TurnContext = append(json.RawMessage(nil), metadata.TurnContext...)
	metadata.WorldState = append(json.RawMessage(nil), metadata.WorldState...)
	return metadata
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
