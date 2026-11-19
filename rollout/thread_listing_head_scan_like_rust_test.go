package rollout

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"codex_go/session"
)

// writeHeadScanRollout writes a rollout whose session metadata is the first
// record and whose user message sits at 1-based record position userRecord.
func writeHeadScanRollout(t *testing.T, userRecord int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-2026-06-29T01-02-03-thread-head.jsonl")
	lines := []string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-head","timestamp":"2026-06-29T01:02:03Z","source":"cli","model_provider":"openai","cwd":"/repo"}}`,
	}
	for len(lines) < userRecord-1 {
		lines = append(lines, `{"timestamp":"2026-06-29T01:02:04Z","type":"turn_context","payload":{"cwd":"/repo"}}`)
	}
	lines = append(lines, `{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"user_message","message":"deep preview"}}`)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// Rust's rollout listing reads HEAD_RECORD_LIMIT records, then extends the scan
// to HEAD_RECORD_LIMIT+USER_EVENT_SCAN_LIMIT while the preview is missing
// (codex-rs/rollout/src/list.rs::read_head_summary).
func TestBuildThreadItemScansPastHeadLimitForPreview(t *testing.T) {
	path := writeHeadScanRollout(t, 22)
	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatalf("BuildThreadItem() ok = false")
	}
	if item.FirstUserMessage != "deep preview" || item.Preview != "deep preview" {
		t.Fatalf("BuildThreadItem() preview = %q/%q, want deep preview", item.Preview, item.FirstUserMessage)
	}
}

func TestBuildThreadItemStopsAtUserEventScanLimit(t *testing.T) {
	path := writeHeadScanRollout(t, threadListItemHeadLimit+threadListItemUserScanLimit+8)
	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatalf("BuildThreadItem() ok = false")
	}
	if item.FirstUserMessage != "" || item.Preview != "" {
		t.Fatalf("BuildThreadItem() preview = %q/%q, want empty beyond the scan cap", item.Preview, item.FirstUserMessage)
	}
}

func TestRecordFromThreadItemProjectsListingMetadata(t *testing.T) {
	item := ThreadItem{
		Path:             "/home/sessions/rollout-2026-06-29T01-02-03-thread-1.jsonl",
		ThreadID:         "thread-1",
		Preview:          "hello",
		FirstUserMessage: "hello",
		Source:           "cli",
		HistoryMode:      "legacy",
		CWD:              "/repo",
		Model:            "gpt-5",
		ModelProvider:    "openai",
		CLIVersion:       "0.1.0",
		AgentNickname:    "nick",
		AgentRole:        "reviewer",
		ParentThreadID:   "parent-1",
		ForkedFromID:     "fork-1",
		AttachedThread:   true,
	}
	record := RecordFromThreadItem(item)
	if record == nil {
		t.Fatalf("RecordFromThreadItem() = nil")
	}
	if record.ID != session.ThreadID("thread-1") || record.SessionID != "thread-1" {
		t.Fatalf("RecordFromThreadItem() id = %q/%q", record.ID, record.SessionID)
	}
	if record.ForkedFromID != session.ThreadID("fork-1") || record.AttachedThreadParentID != session.ThreadID("fork-1") {
		t.Fatalf("RecordFromThreadItem() fork = %q/%q", record.ForkedFromID, record.AttachedThreadParentID)
	}
	if record.ParentThreadID != session.ThreadID("parent-1") {
		t.Fatalf("RecordFromThreadItem() parent = %q", record.ParentThreadID)
	}
	if !record.FromRollout || len(record.Items) != 0 {
		t.Fatalf("RecordFromThreadItem() must not materialize history: %#v", record)
	}
	if record.Metadata.CWD != "/repo" || record.Metadata.ModelProvider != "openai" || record.Metadata.Source != "cli" {
		t.Fatalf("RecordFromThreadItem() metadata = %#v", record.Metadata)
	}
	if record.Metadata.Extra["rollout_path"] != item.Path || record.Metadata.Extra["first_user_message"] != "hello" {
		t.Fatalf("RecordFromThreadItem() extra = %#v", record.Metadata.Extra)
	}
}

// writeListingRollout writes one rollout under <home>/sessions with the given
// session metadata payload fields and optional trailing records.
func writeListingRollout(t *testing.T, home, name, metaFields string, extraLines ...string) string {
	t.Helper()
	path := filepath.Join(home, SessionsSubdir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	lines := []string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"timestamp":"2026-06-29T01:02:03Z","source":"cli","model_provider":"openai","cwd":"/repo"` + metaFields + `}}`,
	}
	lines = append(lines, extraLines...)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func listingThreadIDs(page *Page) []string {
	ids := make([]string, 0, len(page.Items))
	for i := range page.Items {
		ids = append(ids, page.Items[i].ThreadID)
	}
	return ids
}

// Rust's rollout listing uses PreviewFilter::RequirePreview for the ordinary
// thread list, so a rollout that produced no preview is not resumable history.
func TestListThreadsRequiresPreviewLikeRust(t *testing.T) {
	home := t.TempDir()
	writeListingRollout(t, home, "rollout-2026-06-29T01-02-03-thread-empty.jsonl", `,"id":"thread-empty"`)
	writeListingRollout(t, home, "rollout-2026-06-29T01-02-04-thread-msg.jsonl", `,"id":"thread-msg"`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}`)

	all, err := ListThreads(home, ListOptions{PageSize: 0})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if got := len(all.Items); got != 2 {
		t.Fatalf("ListThreads() items = %d, want 2", got)
	}
	page, err := ListThreads(home, ListOptions{PageSize: 0, RequirePreview: true})
	if err != nil {
		t.Fatalf("ListThreads(RequirePreview) error = %v", err)
	}
	if got := listingThreadIDs(page); !reflect.DeepEqual(got, []string{"thread-msg"}) {
		t.Fatalf("ListThreads(RequirePreview) = %v, want thread-msg", got)
	}
}

// A reference-backed fork with prior history stays visible without a local
// preview, but a non-root agent source does not (HeadTailSummary::has_fork_history).
func TestListThreadsKeepsForkHistoryWithoutPreviewLikeRust(t *testing.T) {
	home := t.TempDir()
	writeListingRollout(t, home, "rollout-2026-06-29T01-02-03-thread-fork.jsonl",
		`,"id":"thread-fork","forked_from_id":"parent-1","history_base":{"thread_id":"parent-1","end_ordinal_exclusive":5,"end_byte_offset":100}`)
	writeListingRollout(t, home, "rollout-2026-06-29T01-02-04-thread-subagent.jsonl",
		`,"id":"thread-subagent","source":"subagent_review","forked_from_id":"parent-1","history_base":{"thread_id":"parent-1","end_ordinal_exclusive":5,"end_byte_offset":100}`)

	page, err := ListThreads(home, ListOptions{PageSize: 0, RequirePreview: true, IncludeAttachedThreads: true})
	if err != nil {
		t.Fatalf("ListThreads(RequirePreview) error = %v", err)
	}
	if got := listingThreadIDs(page); !reflect.DeepEqual(got, []string{"thread-fork"}) {
		t.Fatalf("ListThreads(RequirePreview) = %v, want thread-fork", got)
	}
}

// Attached forks are kept even without a preview when the caller asked for
// attached threads (Rust build_thread_item's attached_to_thread_id exemption).
func TestListThreadsKeepsAttachedThreadWithoutPreviewLikeRust(t *testing.T) {
	home := t.TempDir()
	writeListingRollout(t, home, "rollout-2026-06-29T01-02-03-thread-attached.jsonl",
		`,"id":"thread-attached","forked_from_id":"parent-1","attached_thread":true`)

	page, err := ListThreads(home, ListOptions{PageSize: 0, RequirePreview: true, IncludeAttachedThreads: true})
	if err != nil {
		t.Fatalf("ListThreads(RequirePreview) error = %v", err)
	}
	if got := listingThreadIDs(page); !reflect.DeepEqual(got, []string{"thread-attached"}) {
		t.Fatalf("ListThreads(RequirePreview) = %v, want thread-attached", got)
	}
	hidden, err := ListThreads(home, ListOptions{PageSize: 0, RequirePreview: true})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(hidden.Items) != 0 {
		t.Fatalf("ListThreads() items = %d, want none", len(hidden.Items))
	}
}

// Rust matches a rollout's recorded cwd against the picker filter with
// path_utils::paths_match_after_normalization (rollout/src/list.rs).
func TestListThreadsCWDFilterUsesPathNormalizationLikeRust(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll(nested) error = %v", err)
	}
	path := filepath.Join(home, SessionsSubdir, "rollout-2026-06-29T01-02-03-thread-cwd.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	lines := []string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-cwd","timestamp":"2026-06-29T01:02:03Z","source":"cli","model_provider":"openai","cwd":` + strconv.Quote(root) + `}}`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	normalized := root + string(os.PathSeparator) + "nested" + string(os.PathSeparator) + ".."
	page, err := ListThreads(home, ListOptions{PageSize: 0, CWDFilters: []string{normalized}})
	if err != nil {
		t.Fatalf("ListThreads(normalized cwd) error = %v", err)
	}
	if got := listingThreadIDs(page); !reflect.DeepEqual(got, []string{"thread-cwd"}) {
		t.Fatalf("ListThreads(normalized cwd) = %v, want thread-cwd", got)
	}
	missing, err := ListThreads(home, ListOptions{PageSize: 0, CWDFilters: []string{filepath.Join(root, "elsewhere")}})
	if err != nil {
		t.Fatalf("ListThreads(other cwd) error = %v", err)
	}
	if len(missing.Items) != 0 {
		t.Fatalf("ListThreads(other cwd) items = %d, want none", len(missing.Items))
	}
}

// Newer rollouts persist user turns as ItemCompleted(UserMessage) instead of
// the legacy user_message event, so the listing must read both (Rust
// event_msg_preview + HeadTailSummary::first_user_message).
func TestBuildThreadItemReadsItemCompletedUserMessageLikeRust(t *testing.T) {
	home := t.TempDir()
	path := writeListingRollout(t, home, "rollout-2026-06-29T01-02-03-thread-item.jsonl", `,"id":"thread-item"`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"# Context from my IDE setup:\n\n## My request for Codex:\npaginated hello"}]}}}`)

	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatalf("BuildThreadItem() ok = false")
	}
	if item.Preview != "paginated hello" || item.FirstUserMessage != "paginated hello" {
		t.Fatalf("BuildThreadItem() preview = %q/%q, want paginated hello", item.Preview, item.FirstUserMessage)
	}
	page, err := ListThreads(home, ListOptions{PageSize: 0, RequirePreview: true})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if got := listingThreadIDs(page); !reflect.DeepEqual(got, []string{"thread-item"}) {
		t.Fatalf("ListThreads(RequirePreview) = %v, want thread-item", got)
	}
}

// Rust strips the model-context prefix from legacy user_message events too
// (protocol::strip_user_message_prefix).
func TestBuildThreadItemStripsUserMessagePrefixLikeRust(t *testing.T) {
	path := writeListingRollout(t, t.TempDir(), "rollout-2026-06-29T01-02-03-thread-prefix.jsonl", `,"id":"thread-prefix"`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"user_message","message":"# Context from my IDE setup:\n\n## My request for Codex:\nlegacy hello"}}`)

	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatalf("BuildThreadItem() ok = false")
	}
	if item.Preview != "legacy hello" || item.FirstUserMessage != "legacy hello" {
		t.Fatalf("BuildThreadItem() preview = %q/%q, want legacy hello", item.Preview, item.FirstUserMessage)
	}
}

// A thread goal fills the preview but never first_user_message, which keeps a
// goal-only thread visible in the resume picker.
func TestBuildThreadItemReadsGoalPreviewLikeRust(t *testing.T) {
	home := t.TempDir()
	path := writeListingRollout(t, home, "rollout-2026-06-29T01-02-03-thread-goal.jsonl", `,"id":"thread-goal"`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"thread_goal_updated","goal":{"objective":"  Ship the resume fix  "}}}`)

	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatalf("BuildThreadItem() ok = false")
	}
	if item.Preview != "Ship the resume fix" || item.FirstUserMessage != "" {
		t.Fatalf("BuildThreadItem() preview = %q/%q, want goal preview only", item.Preview, item.FirstUserMessage)
	}
	page, err := ListThreads(home, ListOptions{PageSize: 0, RequirePreview: true})
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if got := listingThreadIDs(page); !reflect.DeepEqual(got, []string{"thread-goal"}) {
		t.Fatalf("ListThreads(RequirePreview) = %v, want thread-goal", got)
	}
}

// Attachment-only user messages fall back to the Rust media labels.
func TestBuildThreadItemUsesMediaPreviewLikeRust(t *testing.T) {
	path := writeListingRollout(t, t.TempDir(), "rollout-2026-06-29T01-02-03-thread-image.jsonl", `,"id":"thread-image"`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"image","image_url":"data:image/png;base64,AAAA"}]}}}`)

	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatalf("BuildThreadItem() ok = false")
	}
	if item.Preview != "[Image]" || item.FirstUserMessage != "[Image]" {
		t.Fatalf("BuildThreadItem() preview = %q/%q, want [Image]", item.Preview, item.FirstUserMessage)
	}
}
