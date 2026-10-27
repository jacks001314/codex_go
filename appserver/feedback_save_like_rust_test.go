package appserver

// Like-Rust coverage for `feedback/save` (Rust #55940): local diagnostic ZIP
// exports are published atomically without replacing caller-owned files,
// bound their contents, keep duplicate attachment names safe, fail without a
// partial archive when a selected file cannot be read, honor log consent, and
// never materialize an untouched thread's rollout.

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

func TestFeedbackSaveParamsJSONMatchesRustSchema(t *testing.T) {
	params := FeedbackSaveParams{
		FeedbackUploadParams: FeedbackUploadParams{Classification: "bug"},
		SavePath:             filepath.Join(t.TempDir(), "feedback.zip"),
	}
	data, err := json.Marshal(&params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for _, key := range []string{"classification", "reason", "threadId", "extraLogFiles", "tags", "savePath"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("payload %s missing %q", data, key)
		}
	}
	if _, ok := payload["includeLogs"]; ok {
		t.Fatalf("payload %s must omit includeLogs when false", data)
	}
	if payload["savePath"] != params.SavePath {
		t.Fatalf("savePath = %#v, want %q", payload["savePath"], params.SavePath)
	}
	if err := (&FeedbackSaveParams{FeedbackUploadParams: FeedbackUploadParams{Classification: "bug"}, SavePath: "relative.zip"}).Validate(); !errors.Is(err, ErrInvalidFeedbackRequest) || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative savePath error = %v, want absolute host path", err)
	}
	if err := (&FeedbackSaveParams{FeedbackUploadParams: FeedbackUploadParams{Classification: "bug"}}).Validate(); !errors.Is(err, ErrInvalidFeedbackRequest) {
		t.Fatalf("empty savePath error = %v, want ErrInvalidFeedbackRequest", err)
	}
}

func readFeedbackArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader(%s) error = %v", path, err)
	}
	defer archive.Close()
	entries := map[string][]byte{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("Open(%s) error = %v", file.Name, err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatalf("ReadAll(%s) error = %v", file.Name, err)
		}
		entries[file.Name] = data
	}
	return entries
}

func TestFeedbackSaveArchiveKeepsDuplicateNamesAndManifestLikeRust(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feedback.zip")
	first := FeedbackAttachment{Filename: "../../same.log", Buffer: []byte("first")}
	second := FeedbackAttachment{Filename: "../../same.log", Buffer: []byte("second")}
	if err := SaveFeedbackArchive(destination, []byte("{}"), []feedbackArchiveEntry{
		{attachment: &first},
		{attachment: &second},
	}, DefaultFeedbackArchiveLimits()); err != nil {
		t.Fatalf("SaveFeedbackArchive() error = %v", err)
	}
	entries := readFeedbackArchive(t, destination)
	if string(entries["report.json"]) != "{}" {
		t.Fatalf("report.json = %q", entries["report.json"])
	}
	if string(entries["attachments/0000/same.log"]) != "first" || string(entries["attachments/0001/same.log"]) != "second" {
		t.Fatalf("duplicate attachment entries = %#v", entries)
	}
	var manifest []map[string]any
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatalf("manifest.json error = %v", err)
	}
	if len(manifest) != 2 || manifest[0]["entry"] != "attachments/0000/same.log" || manifest[0]["filename"] != "../../same.log" {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestFeedbackSaveArchiveBoundsAndCleansUpLikeRust(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "feedback.zip")
	attachments := func(count int) []feedbackArchiveEntry {
		entries := make([]feedbackArchiveEntry, 0, count)
		for index := 0; index < count; index++ {
			attachment := FeedbackAttachment{Filename: "test.log", Buffer: []byte("synthetic")}
			entries = append(entries, feedbackArchiveEntry{attachment: &attachment})
		}
		return entries
	}
	limits := FeedbackArchiveLimits{MaxBytes: 1024, MaxAttachments: 2}
	if err := SaveFeedbackArchive(destination, []byte("{}"), attachments(2), limits); err != nil {
		t.Fatalf("SaveFeedbackArchive() error = %v", err)
	}
	entries := readFeedbackArchive(t, destination)
	total := 0
	for _, data := range entries {
		total += len(data)
	}
	if err := os.Remove(destination); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, tc := range []struct {
		name        string
		count       int
		maxBytes    int
		wantMessage string
	}{
		{name: "size", count: 2, maxBytes: total - 1, wantMessage: "uncompressed size limit"},
		{name: "count", count: 3, maxBytes: 1024, wantMessage: "attachment count limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := SaveFeedbackArchive(destination, []byte("{}"), attachments(tc.count), FeedbackArchiveLimits{MaxBytes: tc.maxBytes, MaxAttachments: 2})
			if err == nil || !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("error = %v, want %q", err, tc.wantMessage)
			}
			if remaining, readErr := os.ReadDir(directory); readErr != nil || len(remaining) != 0 {
				t.Fatalf("directory after failure = %#v, %v; want empty", remaining, readErr)
			}
		})
	}
}

func TestFeedbackSaveArchivePublishesAtomicallyLikeRust(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "feedback.zip")
	attachment := FeedbackAttachment{Filename: "one.log", Buffer: []byte("first")}
	if err := SaveFeedbackArchive(destination, []byte("{}"), []feedbackArchiveEntry{{attachment: &attachment}}, DefaultFeedbackArchiveLimits()); err != nil {
		t.Fatalf("SaveFeedbackArchive() error = %v", err)
	}
	original, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	second := FeedbackAttachment{Filename: "two.log", Buffer: []byte("second")}
	err = SaveFeedbackArchive(destination, []byte("{}"), []feedbackArchiveEntry{{attachment: &second}}, DefaultFeedbackArchiveLimits())
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error = %v, want already exists", err)
	}
	after, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("existing export was replaced")
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 1 {
		t.Fatalf("directory after overwrite failure = %#v, %v", entries, err)
	}
	if err := SaveFeedbackArchive(filepath.Join(directory, "missing", "feedback.zip"), []byte("{}"), nil, DefaultFeedbackArchiveLimits()); err == nil {
		t.Fatalf("missing parent directory export unexpectedly succeeded")
	}
}

func TestFeedbackSaveAttachesStrictSelectedPathsLikeRust(t *testing.T) {
	inputs := t.TempDir()
	output := t.TempDir()
	name := "rollout-2026-10-19T12-00-00-thread.jsonl"
	first := filepath.Join(inputs, name)
	nested := filepath.Join(inputs, "nested", name)
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(first, []byte("first\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(nested, []byte("second\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	destination := filepath.Join(output, "feedback.zip")
	if err := SaveFeedbackArchive(destination, []byte("{}"), []feedbackArchiveEntry{
		{path: &FeedbackAttachmentPath{Path: first}},
		{path: &FeedbackAttachmentPath{Path: nested}},
	}, DefaultFeedbackArchiveLimits()); err != nil {
		t.Fatalf("SaveFeedbackArchive() error = %v", err)
	}
	entries := readFeedbackArchive(t, destination)
	if string(entries["attachments/0000/"+name]) != "first\n" || string(entries["attachments/0001/"+name]) != "second\n" {
		t.Fatalf("selected attachment entries = %#v", entries)
	}
	if err := os.Remove(destination); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.Remove(nested); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	err := SaveFeedbackArchive(destination, []byte("{}"), []feedbackArchiveEntry{
		{path: &FeedbackAttachmentPath{Path: first}},
		{path: &FeedbackAttachmentPath{Path: nested}},
	}, DefaultFeedbackArchiveLimits())
	if err == nil || !strings.Contains(err.Error(), "selected attachment") {
		t.Fatalf("missing selected file error = %v", err)
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial archive published after failure: %v", statErr)
	}
}

func TestFeedbackSaveIncludesReportAndConsentedLogsLikeRust(t *testing.T) {
	for _, includeLogs := range []bool{false, true} {
		destination := filepath.Join(t.TempDir(), "feedback.zip")
		snapshot := &FeedbackSnapshot{ThreadID: "thread-save", Logs: []byte("ring logs")}
		reason := "synthetic feedback"
		err := snapshot.SaveFeedback(&FeedbackUploadOptions{
			Classification: "bug",
			Reason:         &reason,
			IncludeLogs:    includeLogs,
			LogsOverride:   []byte("consented log bytes"),
		}, destination)
		if err != nil {
			t.Fatalf("SaveFeedback(includeLogs=%t) error = %v", includeLogs, err)
		}
		entries := readFeedbackArchive(t, destination)
		var report map[string]any
		if err := json.Unmarshal(entries["report.json"], &report); err != nil {
			t.Fatalf("report.json error = %v", err)
		}
		if report["message"] != "[Bug]: Codex session thread-save" {
			t.Fatalf("message = %#v", report["message"])
		}
		tags, _ := report["tags"].(map[string]any)
		if tags["classification"] != "bug" || tags["thread_id"] != "thread-save" {
			t.Fatalf("tags = %#v", report["tags"])
		}
		exception, _ := report["exception"].(map[string]any)
		values, _ := exception["values"].([]any)
		if len(values) != 1 || values[0].(map[string]any)["value"] != reason {
			t.Fatalf("exception = %#v", report["exception"])
		}
		logs, hasLogs := entries["attachments/0000/codex-logs.log"]
		if hasLogs != includeLogs {
			t.Fatalf("logs present = %t, want %t", hasLogs, includeLogs)
		}
		if includeLogs && string(logs) != "consented log bytes" {
			t.Fatalf("logs = %q", logs)
		}
	}
}

func TestFeedbackSaveRejectsOversizedReportLikeRust(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "oversized.zip")
	reason := strings.Repeat("x", FeedbackMaxEventBytes)
	err := (&FeedbackSnapshot{ThreadID: "thread-save"}).SaveFeedback(&FeedbackUploadOptions{Classification: "bug", Reason: &reason}, destination)
	if err == nil || !strings.Contains(err.Error(), "feedback event exceeds the size limit") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("oversized report published: %v", statErr)
	}
}

func newFeedbackSaveRuntime(t *testing.T, codexHome string, record *session.Record) *RuntimeRouter {
	t.Helper()
	store := session.NewStore(codexHome)
	if record != nil {
		if err := store.Save(record); err != nil {
			t.Fatalf("store.Save: %v", err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Feedback:     &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)},
	})
	snapshot := router.requireFeedback()
	snapshot.Logs = []byte("logs")
	return router
}

func feedbackSaveRequest(t *testing.T, router *RuntimeRouter, id int64, params FeedbackSaveParams) *Response {
	t.Helper()
	return router.Handle(requestWithParams(t, IntID(id), MethodFeedbackSave, params))
}

func TestFeedbackSaveSkipsUnmaterializedThreadRolloutLikeRust(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	record := &session.Record{
		ID:        "thread-fresh",
		SessionID: "thread-fresh",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: session.Metadata{
			CWD:   home,
			Extra: map[string]any{"rollout_path": rollout.PathForThread(home, "thread-fresh", now)},
		},
	}
	router := newFeedbackSaveRuntime(t, home, record)
	router.markResponseThreadLoaded(&ThreadStartResponse{Thread: &Thread{ID: "thread-fresh"}}, "default")

	destination := filepath.Join(t.TempDir(), "fresh.zip")
	response := feedbackSaveRequest(t, router, 1, FeedbackSaveParams{
		FeedbackUploadParams: FeedbackUploadParams{Classification: "bug", ThreadID: stringPointerAppserver("thread-fresh"), IncludeLogs: true},
		SavePath:             destination,
	})
	if response.Error != nil {
		t.Fatalf("feedback/save error = %+v", response.Error)
	}
	saved, ok := response.Result.(*FeedbackSaveResponse)
	if !ok || saved.SavedPath != destination || saved.ThreadID != "thread-fresh" {
		t.Fatalf("response = %#v", response.Result)
	}
	if _, err := os.Stat(record.Metadata.Extra["rollout_path"].(string)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("saving materialized the untouched rollout: %v", err)
	}
	entries := readFeedbackArchive(t, destination)
	for name := range entries {
		if strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, "rollouts.tar.gz") {
			t.Fatalf("untouched rollout was attached: %s", name)
		}
	}
	indexName := ""
	for name := range entries {
		if strings.HasSuffix(name, "feedback-thread-index.json") {
			indexName = name
		}
	}
	if indexName == "" {
		t.Fatalf("thread index missing from %#v", entries)
	}
	var index map[string]any
	if err := json.Unmarshal(entries[indexName], &index); err != nil {
		t.Fatalf("thread index error = %v", err)
	}
	if value, ok := index["rollout_archive_filename"]; !ok || value != nil {
		t.Fatalf("rollout_archive_filename = %#v present=%t", value, ok)
	}
	threads, _ := index["threads"].([]any)
	if len(threads) != 1 || threads[0].(map[string]any)["rollout_filename"] != nil {
		t.Fatalf("threads = %#v", index["threads"])
	}
}

func TestFeedbackSaveAttachesMaterializedThreadRolloutLikeRust(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	rolloutPath := rollout.PathForThread(home, "thread-live", now)
	record := &session.Record{
		ID:        "thread-live",
		SessionID: "thread-live",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: session.Metadata{
			CWD:   home,
			Extra: map[string]any{"rollout_path": rolloutPath},
		},
	}
	router := newFeedbackSaveRuntime(t, home, record)
	router.markResponseThreadLoaded(&ThreadStartResponse{Thread: &Thread{ID: "thread-live"}}, "default")
	contents := `{"timestamp":"2026-10-19T12:00:00Z","type":"session_meta","payload":{"id":"thread-live"}}` + "\n"
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(rolloutPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "live.zip")
	response := feedbackSaveRequest(t, router, 1, FeedbackSaveParams{
		FeedbackUploadParams: FeedbackUploadParams{Classification: "bug", ThreadID: stringPointerAppserver("thread-live"), IncludeLogs: true},
		SavePath:             destination,
	})
	if response.Error != nil {
		t.Fatalf("feedback/save error = %+v", response.Error)
	}
	entries := readFeedbackArchive(t, destination)
	suffix := "/" + filepath.Base(rolloutPath)
	found := ""
	for name := range entries {
		if strings.HasSuffix(name, suffix) {
			found = name
			break
		}
	}
	if found == "" || string(entries[found]) != contents {
		t.Fatalf("rollout attachment = %q at %q, want %q in %#v", entries[found], found, contents, entries)
	}
}

func TestFeedbackSaveRejectsExistingAndRelativePathsLikeRust(t *testing.T) {
	router := newFeedbackSaveRuntime(t, t.TempDir(), nil)
	directory := t.TempDir()
	existing := filepath.Join(directory, "local.zip")
	if err := os.WriteFile(existing, []byte("existing export"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, tc := range []struct {
		path    string
		message string
	}{
		{path: "relative.zip", message: "absolute host path"},
		{path: "~/feedback.zip", message: "absolute host path"},
		{path: existing, message: "already exists"},
		{path: filepath.Join(directory, "missing", "feedback.zip"), message: "failed to save feedback"},
	} {
		response := feedbackSaveRequest(t, router, 1, FeedbackSaveParams{
			FeedbackUploadParams: FeedbackUploadParams{Classification: "bug"},
			SavePath:             tc.path,
		})
		if response.Error == nil || !strings.Contains(response.Error.Message, tc.message) {
			t.Fatalf("savePath %q response = %+v, want %q", tc.path, response.Error, tc.message)
		}
	}
	contents, err := os.ReadFile(existing)
	if err != nil || string(contents) != "existing export" {
		t.Fatalf("existing export = %q, %v", contents, err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 1 {
		t.Fatalf("directory after failures = %#v, %v", entries, err)
	}
}
