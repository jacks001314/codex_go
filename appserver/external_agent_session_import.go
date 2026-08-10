package appserver

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"codex_go/config"
	"codex_go/rollout"
	"codex_go/session"
)

const (
	externalSessionImportedMarker     = "<EXTERNAL SESSION IMPORTED>"
	externalSessionImportedMarkerType = "external_session_import_marker"
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
		success, failure, created := r.importExternalAgentSession(migration, params.Source, params.MigrationSource)
		if failure != nil {
			result.Failures = append(result.Failures, *failure)
			continue
		}
		if success != nil {
			result.Successes = append(result.Successes, *success)
		}
		if success != nil && created {
			completedImports = append(completedImports, config.ExternalSessionImportCompletion{
				SourcePath:       migration.Path,
				ImportedThreadID: externalAgentStringValue(success.Target),
				Title:            migration.Title,
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

func (r *RuntimeRouter) importExternalAgentSession(migration config.SessionMigration, source *string, migrationSource *string) (*config.ExternalAgentConfigImportItemTypeSuccess, *config.ExternalAgentConfigImportItemTypeFailure, bool) {
	path := strings.TrimSpace(migration.Path)
	fail := func(stage, subtype string, err error) (*config.ExternalAgentConfigImportItemTypeSuccess, *config.ExternalAgentConfigImportItemTypeFailure, bool) {
		message := "external agent session import failed"
		if err != nil {
			message = err.Error()
		}
		return nil, &config.ExternalAgentConfigImportItemTypeFailure{
			ItemType: config.MigrationSessions, FailureStage: stage, Message: message,
			SubErrorType: externalAgentStringPointer(subtype), Source: externalAgentStringPointer(path),
		}, false
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
		// Rust 50ef7395fa: append a stable std::io::ErrorKind-style category to
		// session import failure subtypes so I/O failures are distinguishable
		// from format errors.
		return fail("session_parse", sessionImportIOCategorySubtype("session_parse_failed", err), err)
	}
	canonicalPath, sourceHash, err := config.ExternalSessionContentSHA256(path)
	if err != nil {
		return fail("session_prepare", "session_source_state_failed", err)
	}
	codexHome := ""
	if r.services.Config != nil {
		codexHome = r.services.Config.CodexHome()
	}
	mapping, err := config.FindExternalSessionImport(codexHome, canonicalPath)
	if err != nil {
		return fail("session_prepare", "session_ledger_read_failed", err)
	}
	prepareExternalAgentImportedItems(record)
	if mapping.Ambiguous || (mapping.Found && mapping.SourceContentSHA256 == sourceHash) {
		return nil, nil, false
	}
	if mapping.Found {
		if r.appendExistingExternalAgentSession(canonicalPath, sourceHash, mapping, record) {
			target := mapping.ImportedThreadID
			return &config.ExternalAgentConfigImportItemTypeSuccess{
				ItemType: config.MigrationSessions, Source: externalAgentStringPointer(path), Target: &target,
				Title: migration.Title,
			}, nil, false
		}
		return nil, nil, false
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
	if strings.TrimSpace(record.Metadata.Source) == "" {
		record.Metadata.Source = "external_agent_import"
	}
	if strings.TrimSpace(record.Metadata.ModelProvider) == "" {
		record.Metadata.ModelProvider = "openai"
	}
	appendExternalAgentImportMarker(record)
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
		Title: externalAgentStringPointer(record.Title),
	}, nil, true
}

func (r *RuntimeRouter) appendExistingExternalAgentSession(sourcePath string, sourceHash string, mapping config.ExternalSessionImportMapping, source *session.Record) bool {
	if r == nil || source == nil || !mapping.Found || mapping.Ambiguous || strings.TrimSpace(mapping.ImportedThreadID) == "" {
		return false
	}
	r.externalSessionSyncMu.Lock()
	defer r.externalSessionSyncMu.Unlock()

	threadID := session.ThreadID(mapping.ImportedThreadID)
	if r.externalAgentTargetLoaded(threadID) {
		return false
	}
	target, err := r.services.ThreadRouter.store.Read(threadID, true, true)
	if err != nil || target == nil || target.ID != threadID || target.Archived ||
		strings.TrimSpace(target.Metadata.CWD) == "" || strings.TrimSpace(target.Metadata.ModelProvider) == "" {
		return false
	}
	if r.externalAgentTargetLoaded(threadID) {
		return false
	}
	if _, err := r.services.ThreadRouter.findThreadRolloutPath(threadID, false); err != nil {
		return false
	}
	suffix, ok := externalAgentAppendPlan(source.Items, target.Items)
	if !ok {
		return false
	}
	original := target
	if _, err := r.services.ThreadRouter.store.AppendItems(threadID, suffix); err != nil {
		return false
	}
	now := time.Now().UTC()
	if r.services.ThreadRouter.now != nil {
		now = r.services.ThreadRouter.now().UTC()
	}
	if err := r.services.ThreadRouter.appendThreadRollout(threadID, suffix, now); err != nil {
		_ = r.services.ThreadRouter.store.Save(original)
		return false
	}
	verified, err := r.services.ThreadRouter.store.Read(threadID, true, true)
	if err != nil || verified == nil || verified.Archived || !externalAgentTranscriptsMatch(source.Items, verified.Items) {
		return false
	}
	checkpointed, err := config.CheckpointExternalSessionImport(
		r.services.Config.CodexHome(), sourcePath, mapping.ImportedThreadID,
		mapping.SourceContentSHA256, sourceHash,
	)
	return err == nil && checkpointed
}

func (r *RuntimeRouter) externalAgentTargetLoaded(threadID session.ThreadID) bool {
	if r == nil || strings.TrimSpace(string(threadID)) == "" {
		return false
	}
	if r.threads != nil && r.threads.LiveThread(threadID) != nil {
		return true
	}
	for _, loaded := range r.requireThreadStatus().LoadedThreadIDs() {
		if loaded == string(threadID) {
			return true
		}
	}
	return false
}

type externalAgentTranscriptItem struct {
	Role string
	Text string
}

func externalAgentAppendPlan(source []session.Item, target []session.Item) ([]session.Item, bool) {
	sourceTranscript, sourceOK := externalAgentModelTranscript(source)
	targetTranscript, targetOK := externalAgentModelTranscript(target)
	if !sourceOK || !targetOK || len(targetTranscript) >= len(sourceTranscript) {
		return nil, false
	}
	for index := range targetTranscript {
		if targetTranscript[index] != sourceTranscript[index] {
			return nil, false
		}
	}
	missing := len(sourceTranscript) - len(targetTranscript)
	suffix := append([]session.Item(nil), source[len(source)-missing:]...)
	return suffix, len(suffix) > 0
}

func externalAgentTranscriptsMatch(source []session.Item, target []session.Item) bool {
	sourceTranscript, sourceOK := externalAgentModelTranscript(source)
	targetTranscript, targetOK := externalAgentModelTranscript(target)
	if !sourceOK || !targetOK || len(sourceTranscript) != len(targetTranscript) {
		return false
	}
	for index := range sourceTranscript {
		if sourceTranscript[index] != targetTranscript[index] {
			return false
		}
	}
	return true
}

func externalAgentModelTranscript(items []session.Item) ([]externalAgentTranscriptItem, bool) {
	transcript := make([]externalAgentTranscriptItem, 0, len(items))
	for _, item := range items {
		if item.Type == externalSessionImportedMarkerType && item.Text == externalSessionImportedMarker {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(item.Role))
		if role == "" {
			switch strings.TrimSpace(item.Type) {
			case "message", "user_message":
				role = "user"
			case "agent_message", "assistant_message":
				role = "assistant"
			}
		}
		if (role != "user" && role != "assistant") || strings.TrimSpace(item.Text) == "" {
			return nil, false
		}
		transcript = append(transcript, externalAgentTranscriptItem{Role: role, Text: item.Text})
	}
	return transcript, true
}

func prepareExternalAgentImportedItems(record *session.Record) {
	if record == nil {
		return
	}
	turn := 0
	for index := range record.Items {
		if strings.EqualFold(strings.TrimSpace(record.Items[index].Role), "user") || turn == 0 {
			turn++
		}
		turnID := "external-import-turn-" + strconv.Itoa(turn)
		if record.Items[index].Metadata == nil {
			record.Items[index].Metadata = map[string]any{}
		}
		record.Items[index].Metadata["turn_id"] = turnID
		record.Items[index].Metadata["turnId"] = turnID
	}
}

func appendExternalAgentImportMarker(record *session.Record) {
	if record == nil || len(record.Items) == 0 {
		return
	}
	last := record.Items[len(record.Items)-1]
	metadata := map[string]any{}
	for key, value := range last.Metadata {
		if key == "turn_id" || key == "turnId" {
			metadata[key] = value
		}
	}
	record.Items = append(record.Items, session.Item{
		ID:        "external-import-marker",
		Type:      externalSessionImportedMarkerType,
		Role:      "assistant",
		Text:      externalSessionImportedMarker,
		CreatedAt: last.CreatedAt,
		Metadata:  metadata,
	})
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

// sessionImportIOCategorySubtype mirrors Rust 50ef7395fa: when the error is an
// I/O error it appends a stable category derived from the underlying error kind
// (not_found, permission_denied, invalid_data, ...), otherwise it keeps the
// base subtype unchanged.
func sessionImportIOCategorySubtype(base string, err error) string {
	kind := sessionImportIOErrorKind(err)
	if kind == "" {
		return base
	}
	return base + "_" + kind
}

func sessionImportIOErrorKind(err error) string {
	if err == nil {
		return ""
	}
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	var syscallErr *os.SyscallError
	var underlying error
	switch {
	case errors.As(err, &pathErr):
		underlying = pathErr.Err
	case errors.As(err, &linkErr):
		underlying = linkErr.Err
	case errors.As(err, &syscallErr):
		underlying = syscallErr.Err
	default:
		return ""
	}
	if errors.Is(underlying, fs.ErrNotExist) {
		return "not_found"
	}
	if errors.Is(underlying, fs.ErrPermission) {
		return "permission_denied"
	}
	if errors.Is(underlying, fs.ErrExist) {
		return "already_exists"
	}
	if errors.Is(underlying, fs.ErrInvalid) {
		return "invalid_input"
	}
	if errors.Is(underlying, syscall.EISDIR) {
		return "is_a_directory"
	}
	if errors.Is(underlying, syscall.ENOTDIR) {
		return "not_a_directory"
	}
	if errors.Is(underlying, syscall.ETIMEDOUT) {
		return "timed_out"
	}
	if errors.Is(underlying, syscall.ENOSPC) {
		return "storage_full"
	}
	if errors.Is(underlying, syscall.EDQUOT) {
		return "quota_exceeded"
	}
	if errors.Is(underlying, syscall.EFBIG) {
		return "file_too_large"
	}
	if errors.Is(underlying, syscall.EROFS) {
		return "read_only_filesystem"
	}
	return "other"
}
