package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #40640: managed requirements may disable the plugins feature without
// removing the selected capability root identity. The root stays visible for
// filtering, but its executor-provided skills must not reach the model.
func TestManagedPluginsRequirementDisablesSelectedExecutorSkillsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		plugins    string
		wantSkills int
	}{
		{name: "enabled", plugins: "true", wantSkills: 1},
		{name: "disabled", plugins: "false", wantSkills: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(config.ConfigPath(home), []byte("[skills]\ninclude_instructions = true\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			requirements := "[features]\nplugins = " + testCase.plugins + "\n"
			if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
				t.Fatal(err)
			}
			capabilityRoot := t.TempDir()
			skillDir := filepath.Join(capabilityRoot, "deploy")
			if err := os.MkdirAll(skillDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: deploy\ndescription: Deploy through the selected plugin.\n---\n\nSELECTED_PLUGIN_SKILL_MARKER\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			store := session.NewStore(t.TempDir())
			configService := config.NewConfigService(home)
			agent := newRecordingRuntimeAgent("ok")
			router := NewRuntimeRouter(RuntimeServices{
				ThreadRouter: NewRouter(store),
				Config:       configService,
				Skills:       NewSkillsServiceWithOptions(&SkillsServiceOptions{Config: configService}),
				Turns:        turn.NewTurnService(),
				Agent:        agent,
				ThreadStatus: NewThreadStatusManager(),
			})
			defer router.Close()
			threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
				CWD: t.TempDir(),
				SelectedCapabilityRoots: []SelectedCapabilityRoot{{
					ID:       "selected-plugin@1",
					Location: CapabilityRootLocation{Type: CapabilityRootLocationEnvironment, EnvironmentID: "local", Path: capabilityRoot},
				}},
			}))
			if threadStart.Error != nil {
				t.Fatalf("thread start error = %+v", threadStart.Error)
			}
			threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
			record, err := router.threadRecord(session.ThreadID(threadID), true, false)
			if err != nil || record == nil {
				t.Fatalf("thread record = %#v, error = %v", record, err)
			}
			if roots := threadSelectedCapabilityRoots(record); len(roots) != 1 || roots[0].ID != "selected-plugin@1" {
				t.Fatalf("selected roots = %#v, want the preserved root identity", roots)
			}

			entries, warnings, err := router.selectedCapabilitySkillEntriesForRuntimeWithSandbox(context.Background(), threadID, nil)
			if err != nil {
				t.Fatalf("selectedCapabilitySkillEntriesForRuntime() error = %v", err)
			}
			if len(entries) != testCase.wantSkills || len(warnings) != 0 {
				t.Fatalf("entries = %#v, warnings = %#v, want %d entries", entries, warnings, testCase.wantSkills)
			}
			provider := router.executorSkillProviderForThread(threadID)
			if testCase.wantSkills == 0 && provider != nil {
				t.Fatal("executor skill provider was installed while plugins were disabled")
			}

			turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
				ThreadID: threadID,
				Prompt:   "inspect selected plugin capabilities",
			}))
			if turnStart.Error != nil {
				t.Fatalf("turn start error = %+v", turnStart.Error)
			}
			request := waitForRuntimeAgentRequest(t, agent)
			hasSkill := strings.Contains(request.Instructions, "deploy") && strings.Contains(request.Instructions, "Deploy through the selected plugin.")
			if hasSkill != (testCase.wantSkills == 1) {
				t.Fatalf("instructions skill exposure = %v, want %v:\n%s", hasSkill, testCase.wantSkills == 1, request.Instructions)
			}
		})
	}
}
