package appserver

import (
	"errors"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/rollout"
	"codex_go/session"
)

func (r *RuntimeRouter) importExternalAgentSessions(params *config.ExternalAgentConfigImportParams) []config.ExternalAgentConfigImportTypeResult {
	if params == nil {
		return nil
	}
	selected, _ := config.ValidatePendingSessionImports(params.MigrationItems)
	if len(selected) == 0 {
		return nil
	}
	result := config.ExternalAgentConfigImportTypeResult{ItemType: config.MigrationSessions}
	completedImports := make([]config.ExternalSessionImportCompletion, 0, len(selected))
	for _, migration := range selected {
		success, failure := r.importExternalAgentSession(migration, params.Source, params.MigrationSource)
		if failure != nil {
			result.Failures = append(result.Failures, *failure)
		} else {
			result.Successes = append(result.Successes, *success)
			completedImports = append(completedImports, config.ExternalSessionImportCompletion{
				SourcePath:       migration.Path,
				ImportedThreadID: externalAgentStringValue(success.Target),
			})
		}
	}
	if len(completedImports) > 0 && r.services.Config != nil {
		if err := config.RecordExternalSessionImports(r.services.Config.CodexHome(), completedImports); err != nil {
			result.Failures = append(result.Failures, config.ExternalAgentConfigImportItemTypeFailure{
				ItemType:     config.MigrationSessions,
				SubErrorType: externalAgentStringPointer("failed_to_update_session_ledger"),
				FailureStage: "session_ledger_update",
				Message:      err.Error(),
			})
		}
	}
	return []config.ExternalAgentConfigImportTypeResult{result}
}

func (r *RuntimeRouter) importExternalAgentSession(migration config.SessionMigration, source *string, migrationSource *string) (*config.ExternalAgentConfigImportItemTypeSuccess, *config.ExternalAgentConfigImportItemTypeFailure) {
	path := strings.TrimSpace(migration.Path)
	fail := func(stage, subtype string, err error) (*config.ExternalAgentConfigImportItemTypeSuccess, *config.ExternalAgentConfigImportItemTypeFailure) {
		message := "external agent session import failed"
		if err != nil {
			message = err.Error()
		}
		return nil, &config.ExternalAgentConfigImportItemTypeFailure{
			ItemType: config.MigrationSessions, FailureStage: stage, Message: message,
			SubErrorType: externalAgentStringPointer(subtype), Source: externalAgentStringPointer(path),
		}
	}
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return fail("session_persist", "session_store_unavailable", errors.New("thread store is unavailable"))
	}
	now := time.Now().UTC()
	if r.services.ThreadRouter.now != nil {
		now = r.services.ThreadRouter.now().UTC()
	}
	var record *session.Record
	var err error
	if migrationSource != nil && strings.EqualFold(strings.TrimSpace(*migrationSource), "cursor") {
		record, err = rollout.ExternalCursorSessionRecord(path, now)
	} else {
		record, err = rollout.ExternalAgentSessionRecord(path, now)
	}
	if err != nil {
		return fail("session_parse", "session_parse_failed", err)
	}
	record.ID = newThreadID()
	record.SessionID = string(record.ID)
	record.Title = strings.TrimSpace(externalAgentStringValue(migration.Title))
	if strings.TrimSpace(migration.CWD) != "" {
		record.Metadata.CWD = strings.TrimSpace(migration.CWD)
	}
	if source != nil && strings.TrimSpace(*source) != "" {
		record.Metadata.Source = strings.TrimSpace(*source)
	}
	if err := r.services.ThreadRouter.store.Create(record); err != nil {
		return fail("session_persist", "session_persist_failed", err)
	}
	if err := r.services.ThreadRouter.createThreadRollout(record, record.CreatedAt); err != nil {
		_ = r.services.ThreadRouter.store.Delete(record.ID)
		return fail("session_persist", "session_rollout_failed", err)
	}
	target := string(record.ID)
	return &config.ExternalAgentConfigImportItemTypeSuccess{
		ItemType: config.MigrationSessions, Source: externalAgentStringPointer(path), Target: &target,
	}, nil
}

func externalAgentStringPointer(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func externalAgentStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
