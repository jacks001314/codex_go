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
