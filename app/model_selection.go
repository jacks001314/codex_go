package app

import (
	"context"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/config"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Model and reasoning picker application. Rust's app consumes the picker's
// AppEvent fan-out by (a) syncing the active app-server thread's settings
// (app/thread_settings.rs sync_active_thread_model_setting /
// sync_active_thread_reasoning_setting, plus update_luna_reserve_reasoning for
// the temporary Reserve model) and (b) persisting the user-level defaults
// (app/model_defaults.rs persist_model_defaults). Go's picker only updated the
// local session state, so a selection neither survived a restart nor reached a
// remote thread; these commands close that gap.

// settingsEditsToConfigEdits converts the TUI settings edits to config edits.
// A nil value clears the key, matching Rust's clear_config_value.
func settingsEditsToConfigEdits(edits []codextea.SettingsEdit) []config.ConfigEdit {
	configEdits := make([]config.ConfigEdit, 0, len(edits))
	for _, edit := range edits {
		keyPath := strings.TrimSpace(edit.KeyPath)
		if keyPath == "" {
			continue
		}
		configEdits = append(configEdits, config.ConfigEdit{
			KeyPath:       keyPath,
			Value:         edit.Value,
			MergeStrategy: config.MergeReplace,
		})
	}
	return configEdits
}

// interactiveLocalModelSelectionCommand persists a completed picker decision for
// the embedded TUI. The embedded TUI runs exec directly and sends the selected
// model with each turn, so there is no app-server thread to sync - Rust's
// sync_active_thread_model_setting has no local counterpart here.
func interactiveLocalModelSelectionCommand(root *cli.RootOptions, decision *codextea.PickerDecision) bubbletea.Cmd {
	plan, ok := codextea.ResolveModelSelection(decision)
	if !ok || len(plan.Persist) == 0 {
		return nil
	}
	return func() bubbletea.Msg {
		result := codextea.ModelSelectionSyncMsg{}
		for _, persist := range plan.Persist {
			result.PersistLabel = persist.Label
			configEdits := settingsEditsToConfigEdits(persist.Edits)
			if len(configEdits) == 0 {
				continue
			}
			response, err := interactiveConfigService(root).BatchWrite(&config.ConfigBatchWriteParams{Edits: configEdits})
			if err != nil {
				result.PersistErr = err
				return result
			}
			if response != nil && response.Status == config.WriteOKOverridden {
				result.PersistOverridden = true
			}
		}
		return result
	}
}

// interactiveRemoteModelSelectionCommand syncs a completed picker decision to
// the remote app server's thread and persists the user-level defaults.
func interactiveRemoteModelSelectionCommand(
	ctx context.Context,
	endpoint *appserverdaemon.RemoteAppServerEndpoint,
	state *codextui.State,
	decision *codextea.PickerDecision,
) bubbletea.Cmd {
	plan, ok := codextea.ResolveModelSelection(decision)
	if !ok {
		return nil
	}
	threadID := ""
	if state != nil {
		threadID = strings.TrimSpace(state.ThreadID)
	}
	if threadID == "" {
		return nil
	}
	return func() bubbletea.Msg {
		result := codextea.ModelSelectionSyncMsg{}
		if plan.ThreadModel != nil || plan.ThreadEffort != nil {
			result.ThreadSettingsErr = interactiveRemoteApplyModelSelection(ctx, endpoint, state, threadID, plan)
		}
		for _, persist := range plan.Persist {
			result.PersistLabel = persist.Label
			overridden, err := interactiveRemoteModelSelectionWriter(ctx, endpoint)(persist.Edits)
			if err != nil {
				result.PersistErr = err
				return result
			}
			if overridden {
				result.PersistOverridden = true
			}
		}
		return result
	}
}

// interactiveRemoteApplyModelSelection sends Rust's thread/settings/update for
// the selected model and/or effort, together with the effective collaboration
// mode (Rust sends the mode with both the model and reasoning updates).
func interactiveRemoteApplyModelSelection(
	ctx context.Context,
	endpoint *appserverdaemon.RemoteAppServerEndpoint,
	state *codextui.State,
	threadID string,
	plan codextea.ModelSelectionPlan,
) error {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return err
	}
	defer client.close()
	params := appserver.SettingsUpdateParams{
		ThreadID: threadID,
		Model:    plan.ThreadModel,
		Effort:   plan.ThreadEffort,
	}
	if mode := interactiveCollaborationModeFromState(state); mode != nil {
		params.CollaborationMode = interactiveCollaborationModePayload(mode)
	}
	var response appserver.SettingsUpdateResponse
	return remoteSessionRequest(reqCtx, client, appserver.MethodThreadSettingsUpdate, params, &response)
}

// interactiveRemoteModelSelectionWriter writes the model-selection config edits
// and reports an overridden write (Rust checks WriteStatus::OkOverridden).
func interactiveRemoteModelSelectionWriter(
	ctx context.Context,
	endpoint *appserverdaemon.RemoteAppServerEndpoint,
) func(edits []codextea.SettingsEdit) (bool, error) {
	return func(edits []codextea.SettingsEdit) (bool, error) {
		configEdits := settingsEditsToConfigEdits(edits)
		if len(configEdits) == 0 {
			return false, nil
		}
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return false, err
		}
		defer client.close()
		var response config.ConfigWriteResponse
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodConfigBatchWrite, config.ConfigBatchWriteParams{
			Edits:            configEdits,
			ReloadUserConfig: true,
		}, &response); err != nil {
			return false, err
		}
		return response.Status == config.WriteOKOverridden, nil
	}
}
