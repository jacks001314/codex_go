package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

type originatorModelManager struct {
	model.ModelsManager
	partitions map[string]model.ModelsManager
}

func (m *originatorModelManager) ForOriginator(originator string) (model.ModelsManager, bool) {
	if m == nil {
		return nil, false
	}
	partition, ok := m.partitions[originator]
	return partition, ok
}

func originatorModelService(defaultCatalog, workCatalog, chatCatalog model.ModelsResponse) *model.ModelService {
	return model.NewModelService(&originatorModelManager{
		ModelsManager: model.NewStaticModelsManager(defaultCatalog),
		partitions: map[string]model.ModelsManager{
			"codex_work_cca": model.NewStaticModelsManager(workCatalog),
			"chatgpt_cca":    model.NewStaticModelsManager(chatCatalog),
		},
	})
}

func originatorCatalog(instructions string, applyPatch string) model.ModelsResponse {
	return model.ModelsResponse{Models: []model.ModelInfo{{
		Slug:               "origin-model",
		DisplayName:        "Origin Model",
		Visibility:         model.VisibilityList,
		SupportedInAPI:     true,
		ApplyPatchToolType: applyPatch,
		ModelMessages:      &model.ModelMessages{InstructionsTemplate: instructions},
	}}}
}

// Mirrors Rust #56284: thread startup resolves the originator catalog before
// model instructions, and the actual turn uses that catalog's instructions and
// apply_patch declaration. Unsupported callers retain the default catalog.
func TestOriginatorCatalogsControlThreadInstructionsAndToolsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models: originatorModelService(
			originatorCatalog("Default catalog instructions.", ""),
			originatorCatalog("Work catalog instructions.", "freeform"),
			originatorCatalog("Chat catalog instructions.", ""),
		),
	})
	router.SetNotificationSink(sink)

	type expectedTurn struct {
		originator    string
		instructions  string
		hasApplyPatch bool
	}
	steps := []expectedTurn{
		{originator: "codex_work_cca", instructions: "Work catalog instructions.", hasApplyPatch: true},
		{originator: "chatgpt_cca", instructions: "Chat catalog instructions.", hasApplyPatch: false},
		{originator: "unknown", instructions: "Default catalog instructions.", hasApplyPatch: false},
		{originator: "codex_work_cca", instructions: "Work catalog instructions.", hasApplyPatch: true},
	}
	for index, step := range steps {
		originator := step.originator
		threadStart := router.Handle(requestWithParams(t, IntID(int64(index*2+1)), MethodThreadStart, ThreadStartParams{
			Model:       "origin-model",
			ServiceName: &originator,
		}))
		if threadStart.Error != nil {
			t.Fatalf("%s thread start error: %+v", step.originator, threadStart.Error)
		}
		threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
		record, err := router.threadRecord(session.ThreadID(threadID), false, false)
		if err != nil || record == nil {
			t.Fatalf("%s thread record: %v", step.originator, err)
		}
		if record.Metadata.BaseInstructions != step.instructions {
			t.Fatalf("%s startup instructions = %q, want %q", step.originator, record.Metadata.BaseInstructions, step.instructions)
		}
		if record.Metadata.Originator != step.originator {
			t.Fatalf("%s persisted originator = %q", step.originator, record.Metadata.Originator)
		}

		turnStart := router.Handle(requestWithParams(t, IntID(int64(index*2+2)), MethodTurnStart, turn.TurnStartParams{
			ThreadID: threadID,
			Prompt:   "Describe the task.",
		}))
		if turnStart.Error != nil {
			t.Fatalf("%s turn start error: %+v", step.originator, turnStart.Error)
		}
		request := waitForRuntimeAgentRequest(t, agent)
		if request.Instructions != step.instructions {
			t.Fatalf("%s turn instructions = %q, want %q", step.originator, request.Instructions, step.instructions)
		}
		toolsJSON, err := json.Marshal(request.Tools)
		if err != nil {
			t.Fatalf("%s Marshal(tools) error = %v", step.originator, err)
		}
		hasApplyPatch := strings.Contains(string(toolsJSON), `"apply_patch"`)
		if hasApplyPatch != step.hasApplyPatch {
			t.Fatalf("%s apply_patch tool = %v, want %v: %s", step.originator, hasApplyPatch, step.hasApplyPatch, toolsJSON)
		}
		waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
	}
}
