package memories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/safety"
	"codex_go/state"
	"codex_go/utils"
)

const (
	StageOneConcurrencyLimit  = 8
	StageOneJobLeaseSeconds   = int64(3600)
	StageOneRetryDelaySeconds = int64(3600)
	StageOneThreadScanLimit   = 5000
	StageOnePruneBatchSize    = 200
	PhaseTwoJobLeaseSeconds   = int64(3600)
	PhaseTwoRetryDelaySeconds = int64(3600)
	PhaseTwoHeartbeatSeconds  = 90
)

var InteractiveMemorySources = []string{"cli", "vscode", "atlas", "chatgpt"}

type StartupGuard interface {
	AllowMemoryStartup(context.Context, int64) bool
}

type StageOneExtractionRequest struct {
	Model     string
	ModelInfo model.ModelInfo
	// Version selects the extraction contract and prompts (Rust #43800);
	// empty selects v1.
	Version      config.MemoryVersion
	Instructions string
	Input        string
	OutputSchema map[string]any
	RolloutPath  string
	RolloutCWD   string
}

type StageOneExtractionResponse struct {
	RawMemory      string
	RolloutSummary string
	RolloutSlug    *string
}

type StageOneExtractor interface {
	ExtractMemory(context.Context, StageOneExtractionRequest) (StageOneExtractionResponse, error)
}

type ConsolidationRequest struct {
	Root            string
	Prompt          string
	Model           string
	ReasoningEffort string
}

type PhaseTwoConsolidator interface {
	ConsolidateMemory(context.Context, ConsolidationRequest) error
}

type ConsolidationSpawnError struct {
	Err error
}

func (e *ConsolidationSpawnError) Error() string {
	if e == nil || e.Err == nil {
		return "failed to spawn memory consolidation agent"
	}
	return e.Err.Error()
}

func (e *ConsolidationSpawnError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewConsolidationSpawnError(err error) error {
	if err == nil {
		err = errors.New("failed to spawn memory consolidation agent")
	}
	return &ConsolidationSpawnError{Err: err}
}

type StartupPipeline struct {
	State     *state.StateRuntime
	CodexHome string
	// Version selects the memory namespace root (Rust #43797); empty selects v1.
	Version           config.MemoryVersion
	CurrentThreadID   string
	Config            config.MemoriesConfig
	StageOne          StageOneExtractor
	StageOneModel     string
	StageOneModelInfo model.ModelInfo
	PhaseTwo          PhaseTwoConsolidator
	PhaseTwoModel     string
	Guard             StartupGuard
	HeartbeatInterval time.Duration
}

type StartupReport struct {
	Pruned                 int64
	StageOneClaimed        int
	StageOneSucceeded      int
	StageOneSucceededEmpty int
	StageOneFailed         int
	PhaseTwoStatus         string
}

func (p *StartupPipeline) Run(ctx context.Context) (StartupReport, error) {
	var report StartupReport
	if p == nil || p.State == nil {
		return report, errors.New("memory startup state runtime is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	root := RootForVersion(p.CodexHome, p.Version)
	if err := EnsureLayout(root); err != nil {
		return report, fmt.Errorf("prepare memories root: %w", err)
	}
	_ = SeedExtensionInstructions(root)
	pruned, err := p.State.PruneStage1OutputsForRetention(ctx, p.Config.MaxUnusedDays, StageOnePruneBatchSize)
	if err == nil {
		report.Pruned = pruned
	}
	if p.Guard != nil && !p.Guard.AllowMemoryStartup(ctx, p.Config.MinRateLimitRemainingPercent) {
		report.PhaseTwoStatus = "skipped_rate_limit"
		return report, nil
	}
	if p.StageOne != nil {
		p.runStageOne(ctx, &report)
	}
	if p.PhaseTwo != nil {
		report.PhaseTwoStatus = p.runPhaseTwo(ctx)
	} else {
		report.PhaseTwoStatus = "skipped_unavailable"
	}
	return report, nil
}

func (p *StartupPipeline) runStageOne(ctx context.Context, report *StartupReport) {
	claims, err := p.State.ClaimStage1JobsForStartup(ctx, p.CurrentThreadID, state.Stage1StartupClaimParams{
		ScanLimit:           StageOneThreadScanLimit,
		MaxClaimed:          p.Config.MaxRolloutsPerStartup,
		MaxAgeDays:          p.Config.MaxRolloutAgeDays,
		MinRolloutIdleHours: p.Config.MinRolloutIdleHours,
		AllowedSources:      append([]string(nil), InteractiveMemorySources...),
		LeaseSeconds:        StageOneJobLeaseSeconds,
		MaxRunningJobs:      p.Config.MaxRolloutsPerStartup,
	})
	if err != nil {
		return
	}
	report.StageOneClaimed = len(claims)
	var mutex sync.Mutex
	semaphore := make(chan struct{}, StageOneConcurrencyLimit)
	var workers sync.WaitGroup
	for _, claim := range claims {
		claim := claim
		workers.Add(1)
		go func() {
			defer workers.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			outcome := p.runStageOneJob(ctx, claim)
			mutex.Lock()
			switch outcome {
			case "succeeded":
				report.StageOneSucceeded++
			case "succeeded_no_output":
				report.StageOneSucceededEmpty++
			default:
				report.StageOneFailed++
			}
			mutex.Unlock()
		}()
	}
	workers.Wait()
}

func (p *StartupPipeline) runStageOneJob(ctx context.Context, claim state.Stage1StartupClaim) string {
	contents, err := SerializeFilteredRolloutForMemory(claim.Thread.RolloutPath)
	if err != nil {
		_, _ = p.State.MarkStage1JobFailed(ctx, claim.Thread.ID, claim.OwnershipToken, err.Error(), StageOneRetryDelaySeconds)
		return "failed"
	}
	response, err := p.StageOne.ExtractMemory(ctx, StageOneExtractionRequest{
		Model:        p.StageOneModel,
		ModelInfo:    p.StageOneModelInfo,
		Version:      p.Version,
		Instructions: StageOneSystemPromptForVersion(p.Version),
		Input:        BuildStageOneInputForVersion(p.Version, p.StageOneModelInfo, claim.Thread.RolloutPath, claim.Thread.CWD, claim.Thread.GitBranch, contents),
		OutputSchema: StageOneOutputSchemaForVersion(p.Version),
		RolloutPath:  claim.Thread.RolloutPath,
		RolloutCWD:   claim.Thread.CWD,
	})
	if err != nil {
		_, _ = p.State.MarkStage1JobFailed(ctx, claim.Thread.ID, claim.OwnershipToken, err.Error(), StageOneRetryDelaySeconds)
		return "failed"
	}
	// v2 stores an empty raw memory by design; only the summary is required
	// (Rust #43800).
	emptyOutput := response.RolloutSummary == ""
	if p.Version != config.MemoryVersionV2 && response.RawMemory == "" {
		emptyOutput = true
	}
	if emptyOutput {
		updated, _ := p.State.MarkStage1JobSucceededNoOutput(ctx, claim.Thread.ID, claim.OwnershipToken)
		if updated {
			return "succeeded_no_output"
		}
		return "failed"
	}
	updated, _ := p.State.MarkStage1JobSucceeded(ctx, claim.Thread.ID, claim.OwnershipToken,
		claim.Thread.UpdatedAt.Unix(), response.RawMemory, response.RolloutSummary, response.RolloutSlug)
	if updated {
		return "succeeded"
	}
	return "failed"
}

func (p *StartupPipeline) runPhaseTwo(ctx context.Context) string {
	claim, err := p.State.TryClaimGlobalPhase2Job(ctx, p.CurrentThreadID, PhaseTwoJobLeaseSeconds)
	if err != nil {
		return "failed_claim"
	}
	if claim.Outcome != state.Phase2JobClaimed {
		return string(claim.Outcome)
	}
	fail := func(reason string) string {
		updated, _ := p.State.MarkGlobalPhase2JobFailed(ctx, claim.OwnershipToken, reason, PhaseTwoRetryDelaySeconds)
		if !updated {
			_, _ = p.State.MarkGlobalPhase2JobFailedIfUnowned(ctx, claim.OwnershipToken, reason, PhaseTwoRetryDelaySeconds)
		}
		return reason
	}
	root := RootForVersion(p.CodexHome, p.Version)
	if err := PrepareWorkspace(ctx, root); err != nil {
		return fail("failed_prepare_workspace")
	}
	selected, err := p.State.GetPhase2InputSelection(ctx, p.Config.MaxRawMemoriesForConsolidation, p.Config.MaxUnusedDays)
	if err != nil {
		return fail("failed_load_stage1_outputs")
	}
	watermark := claim.InputWatermark
	for _, output := range selected {
		if value := output.SourceUpdatedAt.Unix(); value > watermark {
			watermark = value
		}
	}
	if err := SyncRolloutSummaries(root, selected, len(selected)); err != nil {
		return fail("failed_sync_workspace_inputs")
	}
	// v2 consolidates straight into memory_summary.md without raw_memories.md
	// (Rust #43813).
	if p.Version != config.MemoryVersionV2 {
		if err := RebuildRawMemoriesFile(root, selected, len(selected)); err != nil {
			return fail("failed_sync_workspace_inputs")
		}
	}
	PruneOldExtensionResources(root, time.Now().UTC())
	diff, err := WorkspaceDiff(ctx, root)
	if err != nil {
		return fail("failed_workspace_status")
	}
	if !diff.HasChanges() && ValidateConsolidationArtifactsForVersion(root, p.Version) == nil {
		if updated, _ := p.State.MarkGlobalPhase2JobSucceeded(ctx, claim.OwnershipToken, watermark, selected); updated {
			return "succeeded_no_workspace_changes"
		}
		return "failed_mark_succeeded"
	}
	if err := WriteWorkspaceDiff(root, diff); err != nil {
		return fail("failed_workspace_diff_file")
	}
	agentCtx, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan bool, 1)
	interval := p.HeartbeatInterval
	if interval <= 0 {
		interval = PhaseTwoHeartbeatSeconds * time.Second
	}
	go p.heartbeatPhaseTwo(agentCtx, claim.OwnershipToken, interval, cancel, heartbeatDone)
	agentErr := p.PhaseTwo.ConsolidateMemory(agentCtx, ConsolidationRequest{
		Root: root, Prompt: BuildConsolidationPromptForVersion(root, p.Version), Model: p.PhaseTwoModel, ReasoningEffort: "medium",
	})
	cancel()
	lostOwnership := <-heartbeatDone
	if agentErr != nil {
		// Rust #39205: remove worker-created symlinks even when the worker
		// fails so they cannot affect files outside the workspace.
		_ = removeMemorySymlinks(root)
		var spawnErr *ConsolidationSpawnError
		if errors.As(agentErr, &spawnErr) {
			return fail("failed_spawn_agent")
		}
		return fail("failed_agent")
	}
	if lostOwnership {
		return fail("failed_agent")
	}
	if err := removeMemorySymlinks(root); err != nil {
		return fail("failed_remove_symlinks")
	}
	if err := ValidateConsolidationArtifactsForVersion(root, p.Version); err != nil {
		return fail("failed_invalid_artifacts")
	}
	owned, err := p.State.HeartbeatGlobalPhase2Job(ctx, claim.OwnershipToken, PhaseTwoJobLeaseSeconds)
	if err != nil {
		return fail("failed_confirm_ownership")
	}
	if !owned {
		return "lost_ownership"
	}
	if err := ResetWorkspaceBaseline(ctx, root); err != nil {
		return fail("failed_workspace_commit")
	}
	updated, _ := p.State.MarkGlobalPhase2JobSucceeded(ctx, claim.OwnershipToken, watermark, selected)
	if !updated {
		return "failed_mark_succeeded"
	}
	return "succeeded"
}

func (p *StartupPipeline) heartbeatPhaseTwo(ctx context.Context, token string, interval time.Duration, cancel context.CancelFunc, done chan<- bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- false
			return
		case <-ticker.C:
			owned, err := p.State.HeartbeatGlobalPhase2Job(ctx, token, PhaseTwoJobLeaseSeconds)
			if err != nil || !owned {
				cancel()
				done <- true
				return
			}
		}
	}
}

func StageOneOutputSchema() map[string]any {
	return StageOneOutputSchemaForVersion(config.MemoryVersionV1)
}

// StageOneOutputSchemaForVersion returns the extraction JSON schema for the
// memory version (Rust #43800): v2 requires only `rollout_summary` and
// `rollout_slug`, while v1 also requires `raw_memory`.
func StageOneOutputSchemaForVersion(version config.MemoryVersion) map[string]any {
	if version == config.MemoryVersionV2 {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"rollout_summary": map[string]any{"type": "string"},
				"rollout_slug":    map[string]any{"type": "string"},
			},
			"required":             []any{"rollout_summary", "rollout_slug"},
			"additionalProperties": false,
		}
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"rollout_summary": map[string]any{"type": "string"},
			"rollout_slug":    map[string]any{"type": []any{"string", "null"}},
			"raw_memory":      map[string]any{"type": "string"},
		},
		"required":             []any{"rollout_summary", "rollout_slug", "raw_memory"},
		"additionalProperties": false,
	}
}

func DecodeStageOneOutput(value string) (StageOneExtractionResponse, error) {
	return DecodeStageOneOutputForVersion(value, config.MemoryVersionV1)
}

// DecodeStageOneOutputForVersion parses the version's extraction contract. It
// redacts secrets before truncation (Rust #43800): v2 accepts only
// `rollout_summary` and `rollout_slug`, stores an empty raw memory, and
// truncates the redacted summary to 9,000 bytes.
func DecodeStageOneOutputForVersion(value string, version config.MemoryVersion) (StageOneExtractionResponse, error) {
	if version == config.MemoryVersionV2 {
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.DisallowUnknownFields()
		var payload struct {
			RolloutSummary string  `json:"rollout_summary"`
			RolloutSlug    *string `json:"rollout_slug"`
		}
		if err := decoder.Decode(&payload); err != nil {
			return StageOneExtractionResponse{}, err
		}
		if payload.RolloutSlug == nil {
			return StageOneExtractionResponse{}, errors.New("stage-one v2 output requires a string rollout_slug")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err == nil {
			return StageOneExtractionResponse{}, errors.New("stage-one output contains trailing JSON")
		} else if !errors.Is(err, io.EOF) {
			return StageOneExtractionResponse{}, fmt.Errorf("invalid trailing stage-one output: %w", err)
		}
		slug := safety.RedactSecrets(*payload.RolloutSlug)
		summary := safety.RedactSecrets(payload.RolloutSummary)
		summary = utils.TruncateText(summary, utils.BytesPolicy(9000))
		return StageOneExtractionResponse{RawMemory: "", RolloutSummary: summary, RolloutSlug: &slug}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var payload struct {
		RawMemory      string  `json:"raw_memory"`
		RolloutSummary string  `json:"rollout_summary"`
		RolloutSlug    *string `json:"rollout_slug"`
	}
	if err := decoder.Decode(&payload); err != nil {
		return StageOneExtractionResponse{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return StageOneExtractionResponse{}, errors.New("stage-one output contains trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return StageOneExtractionResponse{}, fmt.Errorf("invalid trailing stage-one output: %w", err)
	}
	response := StageOneExtractionResponse{
		RawMemory:      safety.RedactSecrets(payload.RawMemory),
		RolloutSummary: safety.RedactSecrets(payload.RolloutSummary),
	}
	if payload.RolloutSlug != nil {
		redacted := safety.RedactSecrets(*payload.RolloutSlug)
		response.RolloutSlug = &redacted
	}
	return response, nil
}

func SerializeFilteredRolloutForMemory(path string) (string, error) {
	lines, _, err := rollout.Load(path)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		if info, statErr := os.Stat(path); statErr != nil || info.Size() == 0 {
			return "", errors.New("empty session file")
		}
	}
	items := make([]any, 0, len(lines))
	for _, line := range lines {
		if line.Type == "inter_agent_communication" {
			var value any
			if json.Unmarshal(line.Payload, &value) == nil {
				items = append(items, value)
			}
			continue
		}
		if line.Type != "item" || len(line.Item) == 0 {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal(line.Item, &item); err != nil {
			continue
		}
		if sanitized, ok := sanitizeMemoryResponseItem(item); ok {
			items = append(items, sanitized)
		}
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return safety.RedactSecrets(string(encoded)), nil
}

func sanitizeMemoryResponseItem(item map[string]any) (map[string]any, bool) {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "message":
		role, _ := item["role"].(string)
		if role == "developer" {
			return nil, false
		}
		if role != "user" {
			return item, true
		}
		content, ok := item["content"].([]any)
		if !ok {
			return item, true
		}
		filtered := make([]any, 0, len(content))
		for _, value := range content {
			block, ok := value.(map[string]any)
			if ok && memoryExcludedContextBlock(block) {
				continue
			}
			filtered = append(filtered, value)
		}
		if len(filtered) == 0 {
			return nil, false
		}
		item["content"] = filtered
		return item, true
	case "agent_message", "local_shell_call", "function_call", "tool_search_call",
		"function_call_output", "tool_search_output", "custom_tool_call",
		"custom_tool_call_output", "web_search_call":
		return item, true
	default:
		return nil, false
	}
}

func memoryExcludedContextBlock(block map[string]any) bool {
	if block["type"] != "input_text" {
		return false
	}
	text, _ := block["text"].(string)
	trimmed := strings.TrimSpace(text)
	return markedMemoryFragment(trimmed, "# AGENTS.md instructions", "</INSTRUCTIONS>") ||
		markedMemoryFragment(trimmed, "<skill>", "</skill>")
}

func markedMemoryFragment(value, start, end string) bool {
	return len(value) >= len(start)+len(end) &&
		strings.EqualFold(value[:len(start)], start) &&
		strings.EqualFold(value[len(value)-len(end):], end)
}
