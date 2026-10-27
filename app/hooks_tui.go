package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/session"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

func interactiveHooksReader(root *cli.RootOptions) codextea.HooksListReaderFunc {
	return func(target codextui.HooksListTarget) (appserver.HookListEntry, error) {
		cwd := strings.TrimSpace(target.CWD)
		if target.IsThread() {
			resolved := interactiveThreadHooksCWD(target.ThreadID)
			if resolved == "" {
				return appserver.HookListEntry{}, fmt.Errorf("thread not found: %s", strings.TrimSpace(target.ThreadID))
			}
			cwd = resolved
		}
		if cwd == "" {
			cwd = interactiveSessionPickerCWD(root)
		}
		router := appserver.NewRuntimeRouter(appserver.RuntimeServices{
			Config:     interactiveConfigService(root),
			DefaultCWD: cwd,
		})
		defer router.Close()
		params := codextui.BuildHooksListParams(codextui.HooksListTargetForCWD(cwd))
		raw, err := json.Marshal(params)
		if err != nil {
			return appserver.HookListEntry{}, err
		}
		response := router.Handle(&appserver.Request{
			JSONRPC: "2.0",
			ID:      appserver.IntID(1),
			Method:  appserver.MethodHooksList,
			Params:  raw,
		})
		if response == nil {
			return appserver.HookListEntry{}, errors.New("hooks/list returned no response")
		}
		if response.Error != nil {
			return appserver.HookListEntry{}, errors.New(response.Error.Message)
		}
		listed, ok := response.Result.(*appserver.HookListResponse)
		if !ok || listed == nil {
			return appserver.HookListEntry{}, errors.New("hooks/list returned an unexpected response")
		}
		return codextui.HooksListEntryFromResponse(*listed, target)
	}
}

// interactiveThreadHooksCWD resolves a thread's working directory from the
// local store. The in-process carrier has no live app-server thread registry,
// and the Go core carries no per-environment hook declarations, so a thread's
// cwd inventory is its complete hook inventory (Rust #56553).
func interactiveThreadHooksCWD(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	store := newSessionStore()
	if store == nil {
		return ""
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return ""
	}
	return strings.TrimSpace(record.Metadata.CWD)
}

func interactiveHookConfigWriter(root *cli.RootOptions) codextea.HookConfigWriteFunc {
	return func(params config.ConfigBatchWriteParams) error {
		_, err := interactiveConfigService(root).BatchWrite(&params)
		return err
	}
}
