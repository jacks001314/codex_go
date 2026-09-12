package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/compact"
	"codex_go/config"
	"codex_go/session"
	"time"
)

// TestCompactPromptForConfigLikeRust covers Rust's compact prompt resolution:
// the config value, then the experimental compact prompt file, then the
// built-in summarization prompt.
func TestCompactPromptForConfigLikeRust(t *testing.T) {
	file := filepath.Join(t.TempDir(), "compact.md")
	if err := os.WriteFile(file, []byte("  From the file.\n"), 0o600); err != nil {
		t.Fatalf("write prompt file error = %v", err)
	}
	emptyFile := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(emptyFile, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write empty prompt file error = %v", err)
	}
	tests := []struct {
		name    string
		values  map[string]any
		want    string
		wantErr string
	}{
		{name: "nil config uses the built-in prompt", values: nil, want: compact.SummarizationPrompt},
		{name: "empty config uses the built-in prompt", values: map[string]any{}, want: compact.SummarizationPrompt},
		{
			name:   "config value is trimmed",
			values: map[string]any{"compact_prompt": "  Custom summary.  "},
			want:   "Custom summary.",
		},
		{
			name:   "blank config value falls through to the file",
			values: map[string]any{"compact_prompt": "   ", "experimental_compact_prompt_file": file},
			want:   "From the file.",
		},
		{
			name:   "file is used when no value is set",
			values: map[string]any{"experimental_compact_prompt_file": file},
			want:   "From the file.",
		},
		{
			name:   "config value wins over the file",
			values: map[string]any{"compact_prompt": "Value wins.", "experimental_compact_prompt_file": file},
			want:   "Value wins.",
		},
		{
			name:    "missing file fails",
			values:  map[string]any{"experimental_compact_prompt_file": filepath.Join(t.TempDir(), "missing.md")},
			wantErr: "failed to read experimental compact prompt file",
		},
		{
			name:    "empty file fails",
			values:  map[string]any{"experimental_compact_prompt_file": emptyFile},
			wantErr: "experimental compact prompt file is empty",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := (*config.Config)(nil)
			if testCase.values != nil {
				cfg = &config.Config{Values: testCase.values}
			}
			got, err := compactPromptForConfig(cfg)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("compactPromptForConfig() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("compactPromptForConfig() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestCompactPromptForRecordUsesTheThreadConfigLikeRust drives the resolution
// through the effective config of a thread.
func TestCompactPromptForRecordUsesTheThreadConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	record := &session.Record{
		ID:       session.ThreadID("thread-compact-prompt"),
		Metadata: session.Metadata{CWD: home, Extra: map[string]any{}},
	}
	prompt, err := router.compactPromptForRecord(record)
	if err != nil {
		t.Fatalf("compactPromptForRecord() error = %v", err)
	}
	if prompt != compact.SummarizationPrompt {
		t.Fatalf("prompt = %q, want the built-in summarization prompt", prompt)
	}

	if err := os.WriteFile(config.ConfigPath(home), []byte("compact_prompt = \"Thread summary prompt.\"\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	router = NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	prompt, err = router.compactPromptForRecord(record)
	if err != nil {
		t.Fatalf("compactPromptForRecord() error = %v", err)
	}
	if prompt != "Thread summary prompt." {
		t.Fatalf("prompt = %q, want the configured value", prompt)
	}
}

// TestCompactThreadUsesConfiguredPromptLikeRust proves the wiring: a completed
// compaction request carries the configured prompt, and the built-in
// summarization prompt when nothing is configured.
func TestCompactThreadUsesConfiguredPromptLikeRust(t *testing.T) {
	home := t.TempDir()
	run := func(t *testing.T, configBody string) string {
		t.Helper()
		if configBody != "" {
			if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
				t.Fatalf("write config error = %v", err)
			}
		}
		store := session.NewStore(t.TempDir())
		now := time.Now().UTC()
		if err := store.Create(&session.Record{
			ID: "thread-compact-prompt-wiring", SessionID: "thread-compact-prompt-wiring",
			CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{Model: "gpt-5.4", CWD: home, Extra: map[string]any{}},
			Items: []session.Item{
				{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
				{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
			},
		}); err != nil {
			t.Fatalf("Create record error = %v", err)
		}
		runner := &recordingCompactRunner{}
		router := NewRuntimeRouter(RuntimeServices{
			ThreadRouter:  NewRouter(store),
			Config:        config.NewConfigService(home),
			CompactRunner: runner,
		})
		if _, err := router.compactThread(context.Background(), &runtimeCompactRequest{
			ThreadID: "thread-compact-prompt-wiring",
			TurnID:   "turn-compact-prompt-wiring",
			Trigger:  compact.TriggerAuto,
			Reason:   compact.ReasonTokenLimit,
			Phase:    compact.PhaseMidTurn,
		}); err != nil {
			t.Fatalf("compactThread() error = %v", err)
		}
		if runner.request == nil {
			t.Fatal("compaction runner was not called")
		}
		return runner.request.Prompt
	}

	if got := run(t, ""); got != compact.SummarizationPrompt {
		t.Fatalf("prompt = %q, want the built-in summarization prompt", got)
	}
	if got := run(t, "compact_prompt = \"Configured compact prompt.\"\n"); got != "Configured compact prompt." {
		t.Fatalf("prompt = %q, want the configured value", got)
	}
}
