package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"codex_go/appserver"
	"codex_go/config"
	"codex_go/session"
	codextea "codex_go/tui/tea"
)

func TestVoiceProjectConfigFollowsCurrentDirectoryLikeRust(t *testing.T) {
	home := t.TempDir()
	projects := []string{t.TempDir(), t.TempDir()}
	voices := []string{"cove", "viola"}
	user := "[realtime]\nvoice = \"marin\"\n"
	for i, dir := range projects {
		if err := os.MkdirAll(filepath.Join(dir, ".gcode"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.ProjectConfigPath(dir), []byte(fmt.Sprintf("[realtime]\nvoice = %q\n", voices[i])), 0600); err != nil {
			t.Fatal(err)
		}
		user += fmt.Sprintf("\n[projects.%q]\ntrust_level = \"trusted\"\n", dir)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(user), 0600); err != nil {
		t.Fatal(err)
	}
	router := appserver.NewDefaultRuntimeRouter(session.NewStore(filepath.Join(home, "sessions")), home)
	t.Cleanup(func() { _ = router.Close() })
	if err := initializeLocalTUIConnection(router.Handle, interactiveVoiceConnectionID); err != nil {
		t.Fatal(err)
	}
	current := projects[0]
	factory := func() interactiveVoiceRouter { return router }
	cwd := func() string { return current }
	settings, _, _, _ := interactiveLocalVoiceCallbacks(factory, cwd)
	for i, dir := range projects {
		current = dir
		got := settings(context.Background())
		if got.VoiceErr != "" || got.Voice != voices[i] {
			t.Fatalf("project %d settings=%#v", i, got)
		}
		picker := interactiveLocalVoiceSettings(factory, cwd)()().(codextea.VoiceSettingsMsg)
		if picker.Err != nil || picker.Current != voices[i] {
			t.Fatalf("project %d picker=%#v", i, picker)
		}
	}
	saved := interactiveLocalVoiceSaver(factory, cwd)("marin")().(codextea.VoiceSavedMsg)
	if saved.Err == nil || !saved.Saved || saved.EffectiveVoice == nil || *saved.EffectiveVoice != "viola" {
		t.Fatalf("project override save=%#v", saved)
	}
}
