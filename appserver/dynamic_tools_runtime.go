package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// errPersistentDynamicToolUpdatesDisabled mirrors Rust #57553's unsupported
// operation: a persistent thread may only replace its dynamic tools when the
// host can restore the replacement on resume.
var errPersistentDynamicToolUpdatesDisabled = errors.New(
	"The host has disabled dynamic tool updates for persistent threads.",
)

// ReplaceThreadDynamicTools replaces a thread's dynamic-tool catalog without
// starting a turn (Rust #57553 `CodexThread::replace_dynamic_tools`). The
// catalog applies to the next model step of a running turn and to every later
// turn; an empty list clears it. Persistent threads persist the replacement
// before publishing, so a failed write leaves the running catalog unchanged.
// Hosts must opt in through RuntimeServices.AllowPersistentDynamicToolUpdates.
func (r *RuntimeRouter) ReplaceThreadDynamicTools(threadID string, tools []turn.DynamicToolSpec) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return session.ErrInvalidThreadID
	}
	if err := turn.ValidateDynamicTools(tools); err != nil {
		return err
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil {
		return err
	}
	if record == nil {
		return session.ErrThreadNotFound
	}
	persistent := !runtimeRecordEphemeral(record)
	if persistent && !r.services.AllowPersistentDynamicToolUpdates {
		return errPersistentDynamicToolUpdatesDisabled
	}
	raw := rawMessagesFromDynamicToolSpecs(tools)
	if raw == nil {
		raw = []json.RawMessage{}
	}
	if persistent {
		// Persist before publishing: a failed write must not change the catalog
		// the running turn uses.
		now := time.Now().UTC()
		if err := r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
			if err := recorder.AppendDynamicToolsUpdated(threadID, raw, now); err != nil {
				return err
			}
			return recorder.Flush()
		}); err != nil {
			return err
		}
	}
	if runtimeRecordEphemeral(record) {
		// Ephemeral threads never persist a rollout, so the catalog only lives on
		// the in-memory record (Rust #57553 keeps the same split).
		record.Metadata.DynamicTools = raw
		if !r.saveEphemeralThreadRecord(record) {
			return fmt.Errorf("%w: %s", session.ErrThreadNotFound, threadID)
		}
	} else if _, err := r.runtimeUpdateThreadMetadata(session.ThreadID(threadID), &session.MetadataPatch{DynamicTools: raw}, true); err != nil {
		return err
	}
	// Publish to the running turn so its next sampling step uses the new
	// catalog. The turn's dispatcher keeps the previous executors, so calls the
	// current step already emitted still run.
	if active := r.threads.ActiveTurn(threadID); active != nil && active.Runtime != nil {
		if err := active.Runtime.ReplaceDynamicTools(tools); err != nil {
			return err
		}
	}
	return nil
}
