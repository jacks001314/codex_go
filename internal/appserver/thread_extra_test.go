package appserver

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/internal/model"
)

func TestGoalStoreSetGetClear(t *testing.T) {
	store := NewGoalStore()
	store.SetClock(func() time.Time { return time.Unix(10, 0) })
	budget := int64(100)
	status := GoalPaused
	objective := "ship"
	set, err := store.Set(&GoalSetParams{ThreadID: "thread-a", Objective: &objective, TokenBudget: &budget, Status: &status})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if set.Goal.UpdatedAt != 10 || set.Goal.Status != GoalPaused {
		t.Fatalf("set = %#v", set)
	}
	got, err := store.Get(&GoalGetParams{ThreadID: "thread-a"})
	if err != nil || got.Goal == nil || got.Goal.Objective != "ship" {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
	cleared, err := store.Clear(&GoalClearParams{ThreadID: "thread-a"})
	if err != nil || !cleared.Cleared {
		t.Fatalf("Clear() = %#v, %v", cleared, err)
	}
}

func TestGoalStoreZeroValueIsUsable(t *testing.T) {
	var store GoalStore
	objective := "ship"
	set, err := store.Set(&GoalSetParams{ThreadID: "thread-a", Objective: &objective})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if set.Goal.Objective != "ship" || set.Goal.Status != GoalActive || set.Goal.CreatedAt == 0 {
		t.Fatalf("set = %#v", set)
	}
	got, err := store.Get(&GoalGetParams{ThreadID: "thread-a"})
	if err != nil || got.Goal == nil || got.Goal.Objective != "ship" {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
	cleared, err := store.Clear(&GoalClearParams{ThreadID: "thread-a"})
	if err != nil || !cleared.Cleared {
		t.Fatalf("Clear() = %#v, %v", cleared, err)
	}
}

func TestGoalStoreSetStatusWithoutObjective(t *testing.T) {
	store := NewGoalStore()
	store.SetClock(func() time.Time { return time.Unix(20, 0) })
	objective := "ship"
	if _, err := store.Set(&GoalSetParams{ThreadID: "thread-a", Objective: &objective}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	status := GoalBlocked
	updated, err := store.Set(&GoalSetParams{ThreadID: "thread-a", Status: &status})
	if err != nil {
		t.Fatalf("Set(status) error = %v", err)
	}
	if updated.Goal.Objective != "ship" || updated.Goal.Status != GoalBlocked {
		t.Fatalf("updated = %#v", updated.Goal)
	}
}

func TestGoalSetParamsUnmarshalTokenBudgetPresence(t *testing.T) {
	var omitted GoalSetParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a"}`), &omitted); err != nil {
		t.Fatalf("Unmarshal omitted tokenBudget error = %v", err)
	}
	if omitted.TokenBudgetSet || omitted.TokenBudget != nil {
		t.Fatalf("omitted tokenBudget decoded as set: %#v", omitted)
	}

	var cleared GoalSetParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a","tokenBudget":null}`), &cleared); err != nil {
		t.Fatalf("Unmarshal null tokenBudget error = %v", err)
	}
	if !cleared.TokenBudgetSet || cleared.TokenBudget != nil {
		t.Fatalf("null tokenBudget decoded incorrectly: %#v", cleared)
	}

	var explicit GoalSetParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a","tokenBudget":123}`), &explicit); err != nil {
		t.Fatalf("Unmarshal explicit tokenBudget error = %v", err)
	}
	if !explicit.TokenBudgetSet || explicit.TokenBudget == nil || *explicit.TokenBudget != 123 {
		t.Fatalf("explicit tokenBudget decoded incorrectly: %#v", explicit)
	}
}

func TestGoalStoreNullTokenBudgetClearsExistingBudget(t *testing.T) {
	store := NewGoalStore()
	store.SetClock(func() time.Time { return time.Unix(30, 0) })
	objective := "ship"
	budget := int64(100)
	if _, err := store.Set(&GoalSetParams{ThreadID: "thread-a", Objective: &objective, TokenBudget: &budget}); err != nil {
		t.Fatalf("Set(initial) error = %v", err)
	}

	var omitted GoalSetParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a"}`), &omitted); err != nil {
		t.Fatalf("Unmarshal omitted tokenBudget error = %v", err)
	}
	kept, err := store.Set(&omitted)
	if err != nil {
		t.Fatalf("Set(omitted) error = %v", err)
	}
	if kept.Goal.TokenBudget == nil || *kept.Goal.TokenBudget != budget {
		t.Fatalf("omitted tokenBudget should keep existing budget: %#v", kept.Goal)
	}

	var cleared GoalSetParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a","tokenBudget":null}`), &cleared); err != nil {
		t.Fatalf("Unmarshal null tokenBudget error = %v", err)
	}
	updated, err := store.Set(&cleared)
	if err != nil {
		t.Fatalf("Set(null) error = %v", err)
	}
	if updated.Goal.TokenBudget != nil {
		t.Fatalf("null tokenBudget should clear existing budget: %#v", updated.Goal)
	}
}

func TestGoalMarshalRustShape(t *testing.T) {
	data, err := json.Marshal(&Goal{
		ThreadID:        "thread-1",
		Objective:       "ship",
		Status:          GoalActive,
		TokensUsed:      12,
		TimeUsedSeconds: 3,
		CreatedAt:       10,
		UpdatedAt:       11,
	})
	if err != nil {
		t.Fatalf("Marshal Goal returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal Goal returned error: %v", err)
	}
	if _, ok := payload["tokenBudget"]; !ok || payload["tokenBudget"] != nil {
		t.Fatalf("tokenBudget should be present as null: %#v", payload)
	}
}

func TestGoalSetValidation(t *testing.T) {
	budget := int64(0)
	objective := "x"
	if _, err := NewGoalStore().Set(&GoalSetParams{ThreadID: "t", Objective: &objective, TokenBudget: &budget}); !errors.Is(err, ErrInvalidThreadExtraRequest) {
		t.Fatalf("expected invalid budget, got %v", err)
	}
	status := GoalBlocked
	if _, err := NewGoalStore().Set(&GoalSetParams{ThreadID: "t", Status: &status}); !errors.Is(err, ErrInvalidThreadExtraRequest) {
		t.Fatalf("expected missing objective error, got %v", err)
	}
}

func TestSettingsUpdateValidation(t *testing.T) {
	permissions := "workspace"
	sandbox := "read-only"
	err := (&SettingsUpdateParams{ThreadID: "t", Permissions: &permissions, SandboxPolicy: &sandbox}).Validate()
	if !errors.Is(err, ErrInvalidThreadExtraRequest) {
		t.Fatalf("expected mutually exclusive error, got %v", err)
	}
}

func TestSettingsMarshalRustShape(t *testing.T) {
	profile := "trusted"
	settings := &Settings{
		CWD:                     "/repo",
		SandboxPolicy:           "read-only",
		ActivePermissionProfile: &profile,
		Model:                   "gpt-test",
		ModelProvider:           "openai",
		MultiAgentMode:          "proactive",
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("Marshal Settings returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal Settings returned error: %v", err)
	}
	if _, ok := payload["multiAgentMode"]; ok {
		t.Fatalf("non-Rust multiAgentMode should not be emitted: %#v", payload)
	}
	if payload["serviceTier"] != nil || payload["effort"] != nil || payload["summary"] != nil || payload["personality"] != nil {
		t.Fatalf("nullable settings fields should be present as null: %#v", payload)
	}
	activeProfile, ok := payload["activePermissionProfile"].(map[string]any)
	if !ok || activeProfile["id"] != "trusted" || activeProfile["extends"] != nil {
		t.Fatalf("activePermissionProfile = %#v", payload["activePermissionProfile"])
	}
	sandboxPolicy, ok := payload["sandboxPolicy"].(map[string]any)
	if !ok || sandboxPolicy["type"] != "readOnly" || sandboxPolicy["networkAccess"] != false {
		t.Fatalf("sandboxPolicy = %#v", payload["sandboxPolicy"])
	}
	collaborationMode, ok := payload["collaborationMode"].(map[string]any)
	if !ok || collaborationMode["mode"] != string(ModeKindDefault) {
		t.Fatalf("collaborationMode = %#v", payload["collaborationMode"])
	}
}

func TestShellCommandValidation(t *testing.T) {
	if err := (&ShellCommandParams{ThreadID: "t", Command: "  "}).Validate(); !errors.Is(err, ErrInvalidThreadExtraRequest) {
		t.Fatalf("expected command error, got %v", err)
	}
}

func TestPaginateBackgroundTerminals(t *testing.T) {
	terminals := []BackgroundTerminal{{ProcessID: "a"}, {ProcessID: "b"}, {ProcessID: "c"}}
	limit := uint32(2)
	page, next, err := PaginateBackgroundTerminals(terminals, nil, &limit)
	if err != nil || len(page) != 2 || next == nil || *next != "b" {
		t.Fatalf("page1 = %#v next=%v err=%v", page, next, err)
	}
	page, next, err = PaginateBackgroundTerminals(terminals, next, &limit)
	if err != nil || len(page) != 1 || page[0].ProcessID != "c" || next != nil {
		t.Fatalf("page2 = %#v next=%v err=%v", page, next, err)
	}
	zero := uint32(0)
	page, next, err = PaginateBackgroundTerminals(terminals, nil, &zero)
	if err != nil || len(page) != 1 || page[0].ProcessID != "a" || next == nil || *next != "a" {
		t.Fatalf("limit 0 page = %#v next=%v err=%v", page, next, err)
	}
}

func TestThreadExtraServiceAddRemoveBackgroundTerminal(t *testing.T) {
	service := NewThreadExtraService()
	canceled := false
	service.AddBackgroundTerminal("thread-1", &BackgroundTerminal{
		ItemID:    "item-1",
		ProcessID: "proc-1",
		Command:   "sleep",
		CWD:       "/repo",
	})
	service.AddBackgroundTerminalWithCancel("thread-1", &BackgroundTerminal{
		ItemID:    "item-2",
		ProcessID: "proc-2",
		Command:   "sleep",
		CWD:       "/repo",
	}, func() { canceled = true })
	list, err := service.ListBackgroundTerminals(&BackgroundTerminalsListParams{ThreadID: "thread-1"})
	if err != nil || len(list.Data) != 2 || list.Data[0].ProcessID != "proc-1" || list.Data[1].ProcessID != "proc-2" {
		t.Fatalf("list after add = %#v err=%v", list, err)
	}
	osPID := uint32(12345)
	cpu := 12.5
	rss := uint64(4096)
	updated, err := service.UpdateBackgroundTerminal(&BackgroundTerminalUpdateParams{
		ThreadID:   "thread-1",
		ProcessID:  "proc-2",
		OSPID:      &osPID,
		CPUPercent: &cpu,
		RSSKB:      &rss,
	})
	if err != nil || !updated.Updated {
		t.Fatalf("UpdateBackgroundTerminal = %#v err=%v", updated, err)
	}
	osPID = 999
	list, err = service.ListBackgroundTerminals(&BackgroundTerminalsListParams{ThreadID: "thread-1"})
	if err != nil || list.Data[1].OSPID == nil || *list.Data[1].OSPID != 12345 || list.Data[1].CPUPercent == nil || *list.Data[1].CPUPercent != 12.5 || list.Data[1].RSSKB == nil || *list.Data[1].RSSKB != 4096 {
		t.Fatalf("list after update = %#v err=%v", list, err)
	}
	if !service.RemoveBackgroundTerminal("thread-1", "proc-1") {
		t.Fatalf("RemoveBackgroundTerminal returned false")
	}
	terminated, err := service.TerminateBackgroundTerminal(&BackgroundTerminalsTerminateParams{ThreadID: "thread-1", ProcessID: "proc-2"})
	if err != nil || !terminated.Terminated || !canceled {
		t.Fatalf("TerminateBackgroundTerminal = %#v canceled=%v err=%v", terminated, canceled, err)
	}
	list, err = service.ListBackgroundTerminals(&BackgroundTerminalsListParams{ThreadID: "thread-1"})
	if err != nil || len(list.Data) != 0 {
		t.Fatalf("list after remove = %#v err=%v", list, err)
	}
	missing, err := service.UpdateBackgroundTerminal(&BackgroundTerminalUpdateParams{ThreadID: "thread-1", ProcessID: "missing", OSPID: &osPID})
	if err != nil || missing.Updated {
		t.Fatalf("missing UpdateBackgroundTerminal = %#v err=%v", missing, err)
	}
}

func TestBackgroundTerminalsListResponseMarshalRequiredArray(t *testing.T) {
	data, err := json.Marshal(&BackgroundTerminalsListResponse{})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	output := string(data)
	if !strings.Contains(output, `"data":[]`) || !strings.Contains(output, `"nextCursor":null`) {
		t.Fatalf("background terminals list payload = %s", data)
	}
}

func TestThreadExtraServiceZeroValueIsUsable(t *testing.T) {
	var service ThreadExtraService
	objective := "ship"
	if _, err := service.SetGoal(&GoalSetParams{ThreadID: "thread-1", Objective: &objective}); err != nil {
		t.Fatalf("SetGoal() error = %v", err)
	}
	cwd := "/repo"
	if _, err := service.UpdateSettings(&SettingsUpdateParams{ThreadID: "thread-1", CWD: &cwd}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	settings := service.Settings("thread-1")
	if settings == nil || settings.CWD != "/repo" {
		t.Fatalf("Settings() = %#v", settings)
	}
	if _, err := service.ShellCommand(&ShellCommandParams{ThreadID: "thread-1", Command: "pwd"}); err != nil {
		t.Fatalf("ShellCommand() error = %v", err)
	}
	if history := service.shellHistory["thread-1"]; len(history) != 1 || history[0] != "pwd" {
		t.Fatalf("shellHistory = %#v", history)
	}
	service.SetBackgroundTerminals("thread-1", []BackgroundTerminal{{ProcessID: "proc-1"}})
	terminated, err := service.TerminateBackgroundTerminal(&BackgroundTerminalsTerminateParams{ThreadID: "thread-1", ProcessID: "proc-1"})
	if err != nil || !terminated.Terminated {
		t.Fatalf("TerminateBackgroundTerminal() = %#v, %v", terminated, err)
	}
}

func TestThreadExtraServiceNullServiceTierUsesDefaultRequestValue(t *testing.T) {
	var service ThreadExtraService
	tier := "priority"
	if _, err := service.UpdateSettings(&SettingsUpdateParams{
		ThreadID:    "thread-1",
		ServiceTier: &ThreadExtraOptionalString{Set: true, Value: &tier},
	}); err != nil {
		t.Fatalf("set service tier error = %v", err)
	}
	if _, err := service.UpdateSettings(&SettingsUpdateParams{
		ThreadID:    "thread-1",
		ServiceTier: &ThreadExtraOptionalString{Set: true},
	}); err != nil {
		t.Fatalf("clear service tier error = %v", err)
	}
	settings := service.Settings("thread-1")
	if settings == nil || settings.ServiceTier == nil || *settings.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("Settings().ServiceTier = %#v", settings)
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	if !strings.Contains(string(data), `"serviceTier":"`+model.ServiceTierDefaultRequestValue+`"`) {
		t.Fatalf("settings payload = %s", data)
	}
}
