package appserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
)

func TestExternalAgentSessionImportPersistsSourceChronology(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewDefaultRuntimeRouter(store, home)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	sourcePath := filepath.Join(t.TempDir(), "source.jsonl")
	body := `{"type":"user","cwd":"/repo","timestamp":"2024-01-02T03:04:05Z","message":{"content":"first request"}}
{"type":"assistant","cwd":"/repo","timestamp":"2024-03-01T04:05:06Z","message":{"content":"first answer"}}`
	if err := os.WriteFile(sourcePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	title := "Fix auth flow"
	details := config.NewMigrationDetails()
	details.Sessions = []config.SessionMigration{{Path: sourcePath, CWD: "/repo", Title: &title}}
	response := router.Handle(requestWithParams(t, IntID(1), MethodExternalAgentConfigImport, config.ExternalAgentConfigImportParams{
		MigrationItems: []config.ExternalAgentConfigMigrationItem{{
			ItemType: config.MigrationSessions, Details: details,
		}},
	}))
	if response.Error != nil {
		t.Fatalf("import response = %+v", response)
	}
	completed := waitForExternalAgentImportNotification(t, sink, response.Result.(*config.ExternalAgentConfigImportResponse).ImportID)
	if len(completed.ItemTypeResults) != 1 || len(completed.ItemTypeResults[0].Successes) != 1 || completed.ItemTypeResults[0].Successes[0].Title == nil || *completed.ItemTypeResults[0].Successes[0].Title != title {
		t.Fatalf("completed session title = %+v", completed.ItemTypeResults)
	}
	histories := router.Handle(requestWithParams(t, IntID(2), MethodExternalAgentConfigImportHistoriesRead, map[string]any{}))
	if histories.Error != nil {
		t.Fatalf("read histories error = %+v", histories.Error)
	}
	historyData := histories.Result.(*config.ExternalAgentConfigImportHistoriesReadResponse).Data
	if len(historyData) != 1 || len(historyData[0].Successes) != 1 || historyData[0].Successes[0].Title == nil || *historyData[0].Successes[0].Title != title {
		t.Fatalf("import history session title = %+v", historyData)
	}
	page, err := store.List(session.ListOptions{PageSize: 10, IncludeHistory: true})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("imported records = %+v err=%v", page, err)
	}
	record := page.Records[0]
	if record.Title != title ||
		record.CreatedAt.Format(time.RFC3339) != "2024-01-02T03:04:05Z" ||
		record.UpdatedAt.Format(time.RFC3339) != "2024-03-01T04:05:06Z" ||
		!record.RecencyAt.Equal(record.UpdatedAt) {
		t.Fatalf("record chronology = %+v", record)
	}
}

func TestExternalAgentSessionImportParsesCursorTranscriptByMigrationSource(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewDefaultRuntimeRouter(store, home)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	sourcePath := filepath.Join(t.TempDir(), "cursor-session.jsonl")
	body := `{"role":"user","cwd":"C:\\repo","timestamp_ms":1704164645000,"message":{"content":[{"type":"text","text":"<user_query>first request</user_query>"}]}}
{"role":"assistant","timestamp_ms":1709265906000,"message":{"content":[{"type":"text","text":"first answer"}]}}`
	if err := os.WriteFile(sourcePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	title := "Cursor chat"
	migrationSource := "cursor"
	details := config.NewMigrationDetails()
	details.Sessions = []config.SessionMigration{{Path: sourcePath, CWD: `C:\repo`, Title: &title}}
	response := router.Handle(requestWithParams(t, IntID(1), MethodExternalAgentConfigImport, config.ExternalAgentConfigImportParams{
		MigrationSource: &migrationSource,
		MigrationItems: []config.ExternalAgentConfigMigrationItem{{
			ItemType: config.MigrationSessions, Details: details,
		}},
	}))
	if response.Error != nil {
		t.Fatalf("import response = %+v", response)
	}
	waitForExternalAgentImportNotification(t, sink, response.Result.(*config.ExternalAgentConfigImportResponse).ImportID)
	page, err := store.List(session.ListOptions{PageSize: 10, IncludeHistory: true})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("imported records = %+v err=%v", page, err)
	}
	record := page.Records[0]
	if record.Title != title || record.Metadata.CWD != `C:\repo` || len(record.Items) != 2 {
		t.Fatalf("record = %+v", record)
	}
	if record.Items[0].Role != "user" || record.Items[0].Text != "first request" || record.Items[1].Role != "assistant" || record.Items[1].Text != "first answer" {
		t.Fatalf("record items = %+v", record.Items)
	}
	if record.CreatedAt.Format(time.RFC3339) != "2024-01-02T03:04:05Z" || record.UpdatedAt.Format(time.RFC3339) != "2024-03-01T04:05:06Z" {
		t.Fatalf("record chronology = %+v", record)
	}
}

func waitForExternalAgentImportNotification(t *testing.T, sink *NotificationBuffer, importID string) *config.ExternalAgentConfigImportCompletedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationExternalAgentConfigImportCompleted {
				continue
			}
			completed, _ := notification.Params.(*config.ExternalAgentConfigImportCompletedNotification)
			if completed != nil && completed.ImportID == importID {
				return completed
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for external agent import completion %s", importID)
	return nil
}

func TestExternalAgentImportReturnsBeforeBackgroundSessionCompletionLikeRust(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(t.TempDir()),
		ExternalAgentSessionImporter: func(*config.ExternalAgentConfigImportParams) []config.ExternalAgentConfigImportTypeResult {
			close(started)
			<-release
			return []config.ExternalAgentConfigImportTypeResult{{
				ItemType: config.MigrationSessions,
				Successes: []config.ExternalAgentConfigImportItemTypeSuccess{{
					ItemType: config.MigrationSessions,
				}},
			}}
		},
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	details := config.NewMigrationDetails()
	details.Sessions = []config.SessionMigration{{Path: "blocked-session.jsonl"}}
	request := requestWithParams(t, IntID(1), MethodExternalAgentConfigImport, config.ExternalAgentConfigImportParams{
		MigrationItems: []config.ExternalAgentConfigMigrationItem{{ItemType: config.MigrationSessions, Details: details}},
	})
	responses := make(chan *Response, 1)
	go func() {
		responses <- router.Handle(request)
	}()

	var response *Response
	select {
	case response = <-responses:
	case <-time.After(time.Second):
		t.Fatal("externalAgentConfig/import waited for the background session importer")
	}
	if response.Error != nil {
		t.Fatalf("import response = %+v", response)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background session importer did not start")
	}
	if notifications := sink.List(); len(notifications) != 0 {
		t.Fatalf("completion arrived before the background import was released: %#v", notifications)
	}
	importID := response.Result.(*config.ExternalAgentConfigImportResponse).ImportID
	close(release)
	completed := waitForExternalAgentImportNotification(t, sink, importID)
	if len(completed.ItemTypeResults) == 0 || completed.ItemTypeResults[len(completed.ItemTypeResults)-1].ItemType != config.MigrationSessions {
		t.Fatalf("completed notification = %#v", completed)
	}
}

func TestExternalAgentSessionImportReportsLedgerFailureAfterSuccess(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	blockedHome := filepath.Join(t.TempDir(), "not-a-directory")
	router := NewDefaultRuntimeRouter(store, blockedHome)
	sourcePath := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte(`{"type":"user","cwd":"/repo","timestamp":"2024-01-02T03:04:05Z","message":{"content":"request"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(blockedHome, "external_agent_session_imports.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	details := config.NewMigrationDetails()
	details.Sessions = []config.SessionMigration{{Path: sourcePath, CWD: "/repo"}}
	results := router.importExternalAgentSessions(&config.ExternalAgentConfigImportParams{
		MigrationItems: []config.ExternalAgentConfigMigrationItem{{ItemType: config.MigrationSessions, Details: details}},
	})
	if len(results) != 1 || len(results[0].Successes) != 1 || len(results[0].Failures) != 1 {
		t.Fatalf("session import results = %#v", results)
	}
	failure := results[0].Failures[0]
	if failure.FailureStage != "session_ledger_update" || externalAgentStringValue(failure.SubErrorType) != "failed_to_update_session_ledger" || failure.Source != nil {
		t.Fatalf("ledger failure = %#v", failure)
	}
}
