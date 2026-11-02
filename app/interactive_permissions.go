package app

import (
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	chatwidget "codex_go/tui/chatwidget"
)

// interactiveLocalListPermissionProfiles mirrors the embedded app server's
// permissionProfile/list handler (appserver.RuntimeRouter.handlePermissionProfileList):
// the built-in profiles plus any `[permissions]` profiles declared in the
// effective config. Rust's TUI always discovers through the app server, so the
// embedded Go TUI answers the same catalog in-process instead of falling back to
// the legacy preset picker.
func interactiveLocalListPermissionProfiles(root *cli.RootOptions) func() ([]chatwidget.CustomPermissionProfile, bool, error) {
	cwd := interactiveSessionPickerCWD(root)
	return func() ([]chatwidget.CustomPermissionProfile, bool, error) {
		values := map[string]any{}
		if loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root)); err == nil && loaded != nil {
			values = loaded.Values
		}
		summaries, err := config.PermissionProfileSummariesFromValuesForCWD(values, cwd)
		if err != nil {
			return nil, false, err
		}
		profiles := make([]chatwidget.CustomPermissionProfile, 0, len(summaries))
		for _, summary := range summaries {
			profiles = append(profiles, chatwidget.CustomPermissionProfile{
				ID:          summary.ID,
				Description: summary.Description,
				Allowed:     summary.Allowed,
			})
		}
		return profiles, false, nil
	}
}
